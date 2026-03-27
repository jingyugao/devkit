package adapter

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type sshAdapter struct{}

func (sshAdapter) ProfileType() profile.Type {
	return profile.TypeSSH
}

func (sshAdapter) Tools() []string {
	return []string{"ssh", "scp", "sftp"}
}

func (sshAdapter) DefaultTool() string {
	return "ssh"
}

func (sshAdapter) PrepareExec(p profile.Profile, secret store.Secret, binary string, userArgs []string) (Prepared, error) {
	tool := filepath.Base(binary)
	common, env, cleanup, err := sshCommon(p, secret)
	if err != nil {
		return Prepared{}, err
	}
	destination := fmt.Sprintf("%s@%s", p.Username, p.Host)

	var args []string
	switch tool {
	case "ssh":
		options, remote := splitSSHExecArgs(userArgs, destination, p.Host)
		args = append(args, common...)
		args = append(args, options...)
		args = append(args, destination)
		args = append(args, remote...)
	case "sftp":
		args = append(common, userArgs...)
		args = append(args, destination)
	case "scp":
		args = append(common, userArgs...)
	default:
		return Prepared{}, fmt.Errorf("unsupported ssh tool %q", tool)
	}

	return Prepared{
		Path:    binary,
		Args:    args,
		Env:     env,
		Cleanup: cleanup,
	}, nil
}

func (sshAdapter) PrepareTest(p profile.Profile, secret store.Secret, binary string) (Prepared, error) {
	tool := filepath.Base(binary)
	common, env, cleanup, err := sshCommon(p, secret)
	if err != nil {
		return Prepared{}, err
	}
	destination := fmt.Sprintf("%s@%s", p.Username, p.Host)

	switch tool {
	case "", "ssh":
		return Prepared{
			Path:    "ssh",
			Args:    append(append(common, destination), "true"),
			Env:     env,
			Cleanup: cleanup,
		}, nil
	case "sftp":
		batchPath, batchCleanup, err := writeTempFile("authrun-sftp-test-*.batch", "quit\n")
		if err != nil {
			if cleanup != nil {
				_ = cleanup()
			}
			return Prepared{}, err
		}
		return Prepared{
			Path:    binary,
			Args:    append(append(common, "-b", batchPath), destination),
			Env:     env,
			Cleanup: combineCleanups(batchCleanup, cleanup),
		}, nil
	case "scp":
		if cleanup != nil {
			_ = cleanup()
		}
		return Prepared{}, fmt.Errorf("authrun test does not support scp; use ssh or sftp")
	default:
		if cleanup != nil {
			_ = cleanup()
		}
		return Prepared{}, fmt.Errorf("unsupported ssh tool %q", tool)
	}
}

func sshCommon(p profile.Profile, secret store.Secret) ([]string, []string, func() error, error) {
	if strings.TrimSpace(secret.PrivateKey) == "" {
		return nil, nil, nil, fmt.Errorf("ssh profile %q requires a private key secret", p.Name)
	}

	keyPath, keyCleanup, err := writeTempFile("authrun-ssh-key-*", secret.PrivateKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create ssh private key: %w", err)
	}
	cleanups := []func() error{keyCleanup}

	if p.PublicKey != "" {
		_, pubCleanup, err := writeTempFile(filepath.Base(keyPath)+".pub-*", p.PublicKey)
		if err != nil {
			_ = keyCleanup()
			return nil, nil, nil, fmt.Errorf("create ssh public key: %w", err)
		}
		cleanups = append(cleanups, pubCleanup)
	}

	args := []string{
		"-i", keyPath,
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityAgent=none",
		"-o", fmt.Sprintf("Port=%d", p.Port),
	}

	if p.KnownHosts != "" {
		knownHostsPath, knownHostsCleanup, err := writeTempFile("authrun-known-hosts-*", p.KnownHosts)
		if err != nil {
			_ = combineCleanups(cleanups...)()
			return nil, nil, nil, fmt.Errorf("create known_hosts file: %w", err)
		}
		cleanups = append(cleanups, knownHostsCleanup)
		args = append(args,
			"-o", "StrictHostKeyChecking=yes",
			"-o", "UserKnownHostsFile="+knownHostsPath,
		)
	}

	var env []string
	if secret.Passphrase != "" {
		script := "#!/bin/sh\nprintf '%s\\n' " + shellSingleQuote(secret.Passphrase) + "\n"
		askpassPath, askpassCleanup, err := writeTempFile("authrun-askpass-*", script)
		if err != nil {
			_ = combineCleanups(cleanups...)()
			return nil, nil, nil, fmt.Errorf("create ssh askpass script: %w", err)
		}
		cleanups = append(cleanups, askpassCleanup)
		env = append(env,
			"DISPLAY=authrun:0",
			"SSH_ASKPASS="+askpassPath,
			"SSH_ASKPASS_REQUIRE=force",
		)
	}

	return args, env, combineCleanups(cleanups...), nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func splitSSHExecArgs(userArgs []string, destination, host string) ([]string, []string) {
	options := make([]string, 0, len(userArgs))
	destSeen := false

	for i := 0; i < len(userArgs); i++ {
		arg := userArgs[i]
		if arg == "--" {
			return options, append([]string{}, userArgs[i+1:]...)
		}
		if !destSeen && isSSHDestinationArg(arg, destination, host) {
			destSeen = true
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			options = append(options, arg)
			if sshOptionConsumesArg(arg) && i+1 < len(userArgs) {
				i++
				options = append(options, userArgs[i])
			}
			continue
		}
		return options, append([]string{}, userArgs[i:]...)
	}

	return options, nil
}

func isSSHDestinationArg(arg, destination, host string) bool {
	value := strings.TrimSpace(arg)
	return value == destination || value == host
}

func sshOptionConsumesArg(arg string) bool {
	switch arg {
	case "-b", "-c", "-D", "-E", "-e", "-F", "-I", "-i", "-J", "-L", "-l", "-m", "-O", "-o", "-p", "-Q", "-R", "-S", "-W", "-w":
		return true
	default:
		return false
	}
}
