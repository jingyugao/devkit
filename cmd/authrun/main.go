package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"github.com/jingyugao/devkit/internal/authrun/adapter"
	"github.com/jingyugao/devkit/internal/authrun/keyring"
	"github.com/jingyugao/devkit/internal/authrun/profile"
	"github.com/jingyugao/devkit/internal/authrun/store"
)

type profileStore interface {
	Add(profile.Profile) error
	Get(string) (profile.Profile, error)
	List() ([]profile.Profile, error)
	Delete(string) error
}

type secretStore interface {
	Put(context.Context, string, store.Secret) error
	Get(context.Context, string) (store.Secret, error)
	Delete(context.Context, string) error
}

type adapterRegistry interface {
	PrepareExec(profile.Profile, store.Secret, string, []string) (adapter.Prepared, error)
	PrepareTest(profile.Profile, store.Secret, string) (adapter.Prepared, error)
	DefaultTool(profile.Type) (string, error)
}

type app struct {
	profiles   profileStore
	secrets    secretStore
	adapters   adapterRegistry
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	environ    func() []string
	execCmd    func(string, ...string) *exec.Cmd
	isTTY      func() bool
	readSecret func() (string, error)
}

type secretInput struct {
	stdin       bool
	envName     string
	filePath    string
	allowPrompt bool
	required    bool
	label       string
}

func main() {
	if err := newDefaultApp().run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(exitCode(err))
	}
}

func newDefaultApp() *app {
	manager := keyring.NewManager()
	return &app{
		profiles: profile.NewDefaultStore(),
		secrets:  store.NewDefaultStore(manager),
		adapters: adapter.NewRegistry(),
		stdin:    os.Stdin,
		stdout:   os.Stdout,
		stderr:   os.Stderr,
		environ:  os.Environ,
		execCmd:  exec.Command,
		isTTY: func() bool {
			return term.IsTerminal(int(os.Stdin.Fd()))
		},
		readSecret: func() (string, error) {
			fmt.Fprint(os.Stderr, "Secret: ")
			secret, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return "", err
			}
			return strings.TrimRight(string(secret), "\r\n"), nil
		},
	}
}

func (a *app) run(args []string) error {
	if len(args) == 0 {
		a.printHelp()
		return nil
	}

	switch args[0] {
	case "add":
		return a.runAdd(args[1:])
	case "list":
		return a.runList()
	case "rm":
		return a.runRemove(args[1:])
	case "exec":
		return a.runExec(args[1:])
	case "test":
		return a.runTest(args[1:])
	case "help":
		a.printHelp()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func (a *app) runAdd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("add requires exactly one profile name")
	}
	name := args[0]
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	profileType := fs.String("type", "", "")
	host := fs.String("host", "", "")
	port := fs.Int("port", 0, "")
	username := fs.String("username", "", "")
	database := fs.String("database", "", "")
	authDatabase := fs.String("auth-database", "", "")
	namespace := fs.String("namespace", "", "")
	cluster := fs.String("cluster", "", "")
	contextName := fs.String("context", "", "")
	server := fs.String("server", "", "")
	tlsEnabled := fs.Bool("tls", false, "")
	tlsCAFile := fs.String("tls-ca-file", "", "")
	insecureSkipTLSVerify := fs.Bool("insecure-skip-tls-verify", false, "")
	socket := fs.String("socket", "", "")
	secretFromStdin := fs.Bool("secret-stdin", false, "")
	secretEnv := fs.String("secret-env", "", "")
	privateKeyFile := fs.String("private-key-file", "", "")
	privateKeyEnv := fs.String("private-key-env", "", "")
	privateKeyStdin := fs.Bool("private-key-stdin", false, "")
	passphraseEnv := fs.String("passphrase-env", "", "")
	passphraseStdin := fs.Bool("passphrase-stdin", false, "")
	publicKeyFile := fs.String("public-key-file", "", "")
	knownHostsFile := fs.String("known-hosts-file", "", "")
	caFile := fs.String("ca-file", "", "")
	clientCertFile := fs.String("client-cert-file", "", "")
	clientKeyFile := fs.String("client-key-file", "", "")
	clientKeyEnv := fs.String("client-key-env", "", "")
	clientKeyStdin := fs.Bool("client-key-stdin", false, "")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := validateSingleStdinSource(*secretFromStdin, *privateKeyStdin, *passphraseStdin, *clientKeyStdin); err != nil {
		return err
	}

	publicKey, err := readOptionalFile(*publicKeyFile)
	if err != nil {
		return err
	}
	knownHosts, err := readOptionalFile(*knownHostsFile)
	if err != nil {
		return err
	}
	certificateAuthority, err := readOptionalFile(*caFile)
	if err != nil {
		return err
	}
	clientCertificate, err := readOptionalFile(*clientCertFile)
	if err != nil {
		return err
	}

	p := profile.Profile{
		Name:                  name,
		Type:                  profile.Type(*profileType),
		Host:                  *host,
		Port:                  *port,
		Username:              *username,
		Database:              *database,
		AuthDatabase:          *authDatabase,
		Namespace:             *namespace,
		Cluster:               *cluster,
		Context:               *contextName,
		Server:                *server,
		TLS:                   *tlsEnabled,
		TLSCAFile:             *tlsCAFile,
		CertificateAuthority:  certificateAuthority,
		ClientCertificate:     clientCertificate,
		InsecureSkipTLSVerify: *insecureSkipTLSVerify,
		Socket:                *socket,
		PublicKey:             publicKey,
		KnownHosts:            knownHosts,
	}.Normalized()

	if err := p.Validate(); err != nil {
		return err
	}
	if _, err := a.profiles.Get(p.Name); err == nil {
		return fmt.Errorf("profile %q already exists", p.Name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}

	secret, err := a.buildSecret(
		p.Type,
		secretInput{
			stdin:       *secretFromStdin,
			envName:     *secretEnv,
			allowPrompt: true,
			required:    false,
			label:       "secret",
		},
		secretInput{
			stdin:    *privateKeyStdin,
			envName:  *privateKeyEnv,
			filePath: *privateKeyFile,
			label:    "private key",
		},
		secretInput{
			stdin:   *passphraseStdin,
			envName: *passphraseEnv,
			label:   "passphrase",
		},
		secretInput{
			stdin:    *clientKeyStdin,
			envName:  *clientKeyEnv,
			filePath: *clientKeyFile,
			label:    "client key",
		},
		clientCertificate != "",
	)
	if err != nil {
		return err
	}

	if err := a.profiles.Add(p); err != nil {
		return err
	}
	if err := a.secrets.Put(context.Background(), p.Name, secret); err != nil {
		_ = a.profiles.Delete(p.Name)
		return err
	}

	fmt.Fprintf(a.stdout, "added\t%s\t%s\n", p.Name, p.Type)
	return nil
}

func (a *app) runList() error {
	profiles, err := a.profiles.List()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTYPE\tTARGET\tUSERNAME\tDETAIL\tTLS")
	for _, p := range profiles {
		target := p.Host
		switch {
		case p.Type == profile.TypeKube:
			target = p.Server
		case p.Port > 0:
			target = fmt.Sprintf("%s:%d", p.Host, p.Port)
		}

		detail := "-"
		switch p.Type {
		case profile.TypeMongo:
			detail = p.Database
			if p.AuthDatabase != "" {
				detail = p.Database + " (auth:" + p.AuthDatabase + ")"
			}
		case profile.TypeMySQL, profile.TypeRedis:
			if p.Database != "" {
				detail = p.Database
			}
		case profile.TypeSSH:
			detail = "ssh"
			if p.KnownHosts != "" {
				detail = "known-hosts"
			}
		case profile.TypeKube:
			detail = p.Namespace
			if detail == "" {
				detail = p.Context
			}
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%t\n",
			p.Name,
			p.Type,
			target,
			p.Username,
			detail,
			p.TLS,
		)
	}
	return tw.Flush()
}

func (a *app) runRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("rm requires exactly one profile name")
	}
	name := strings.TrimSpace(args[0])
	if err := a.profiles.Delete(name); err != nil {
		return err
	}
	if err := a.secrets.Delete(context.Background(), name); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "removed\t%s\n", name)
	return nil
}

func (a *app) runExec(args []string) error {
	before, after, found := splitArgs(args)
	if !found {
		return fmt.Errorf("exec requires `-- <tool> [args...]`")
	}
	if len(before) != 1 {
		return fmt.Errorf("exec requires exactly one profile name before `--`")
	}
	if len(after) == 0 {
		return fmt.Errorf("exec requires a tool after `--`")
	}

	p, secret, err := a.loadProfileAndSecret(before[0])
	if err != nil {
		return err
	}
	prepared, err := a.adapters.PrepareExec(p, secret, after[0], after[1:])
	if err != nil {
		return err
	}
	return a.runPrepared(prepared)
}

func (a *app) runTest(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("test requires exactly one profile name")
	}
	name := args[0]
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tool := fs.String("tool", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	p, secret, err := a.loadProfileAndSecret(name)
	if err != nil {
		return err
	}
	prepared, err := a.adapters.PrepareTest(p, secret, *tool)
	if err != nil {
		return err
	}
	return a.runPrepared(prepared)
}

func (a *app) loadProfileAndSecret(name string) (profile.Profile, store.Secret, error) {
	p, err := a.profiles.Get(name)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	secret, err := a.secrets.Get(context.Background(), name)
	if err != nil {
		return profile.Profile{}, store.Secret{}, err
	}
	return p, secret, nil
}

func (a *app) runPrepared(prepared adapter.Prepared) error {
	if prepared.Cleanup != nil {
		defer prepared.Cleanup()
	}

	cmd := a.execCmd(prepared.Path, prepared.Args...)
	cmd.Stdin = a.stdin
	cmd.Stdout = a.stdout
	cmd.Stderr = a.stderr
	cmd.Env = append(append([]string{}, a.environ()...), prepared.Env...)
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func (a *app) buildSecret(profileType profile.Type, primary, privateKey, passphrase, clientKey secretInput, hasClientCertificate bool) (store.Secret, error) {
	var secret store.Secret

	switch profileType {
	case profile.TypeMySQL, profile.TypeMongo, profile.TypeRedis:
		value, err := a.resolveSecretInput(primary)
		if err != nil {
			return store.Secret{}, err
		}
		if value == "" {
			return store.Secret{}, fmt.Errorf("%s profiles require a password secret", profileType)
		}
		secret.Password = value
	case profile.TypeSSH:
		value, err := a.resolveSecretInput(privateKey)
		if err != nil {
			return store.Secret{}, err
		}
		if value == "" {
			return store.Secret{}, fmt.Errorf("ssh profiles require a private key via --private-key-file, --private-key-env, or --private-key-stdin")
		}
		secret.PrivateKey = value
		secret.Passphrase, err = a.resolveSecretInput(passphrase)
		if err != nil {
			return store.Secret{}, err
		}
	case profile.TypeKube:
		token, err := a.resolveSecretInput(primary)
		if err != nil {
			return store.Secret{}, err
		}
		keyValue, err := a.resolveSecretInput(clientKey)
		if err != nil {
			return store.Secret{}, err
		}
		switch {
		case token != "" && (keyValue != "" || hasClientCertificate):
			return store.Secret{}, fmt.Errorf("kube profile cannot mix token auth with client certificate auth")
		case token != "":
			secret.Token = token
		case keyValue != "" || hasClientCertificate:
			if !hasClientCertificate || keyValue == "" {
				return store.Secret{}, fmt.Errorf("kube client certificate auth requires both --client-cert-file and one client key source")
			}
			secret.ClientKey = keyValue
		default:
			return store.Secret{}, fmt.Errorf("kube profiles require a token secret or client certificate auth material")
		}
	default:
		return store.Secret{}, fmt.Errorf("unsupported profile type %q", profileType)
	}

	return secret, nil
}

func (a *app) resolveSecretInput(input secretInput) (string, error) {
	sources := 0
	if input.stdin {
		sources++
	}
	if input.envName != "" {
		sources++
	}
	if input.filePath != "" {
		sources++
	}
	if sources > 1 {
		return "", fmt.Errorf("%s accepts only one source", input.label)
	}
	if sources == 0 {
		if input.allowPrompt && a.isTTY() {
			value, err := a.readSecret()
			if err != nil {
				return "", err
			}
			if strings.TrimSpace(value) == "" && input.required {
				return "", fmt.Errorf("%s cannot be empty", input.label)
			}
			return strings.TrimRight(value, "\r\n"), nil
		}
		if input.required {
			return "", fmt.Errorf("%s is required", input.label)
		}
		return "", nil
	}

	var value string
	switch {
	case input.stdin:
		data, err := io.ReadAll(a.stdin)
		if err != nil {
			return "", err
		}
		value = string(data)
	case input.envName != "":
		envName := strings.TrimSpace(input.envName)
		envValue, ok := os.LookupEnv(envName)
		if !ok {
			return "", fmt.Errorf("environment variable %q is unset", envName)
		}
		value = envValue
	case input.filePath != "":
		data, err := os.ReadFile(strings.TrimSpace(input.filePath))
		if err != nil {
			return "", fmt.Errorf("read %s file: %w", input.label, err)
		}
		value = string(data)
	}

	if strings.TrimSpace(value) == "" {
		if input.required {
			return "", fmt.Errorf("%s cannot be empty", input.label)
		}
		return "", nil
	}
	return strings.TrimRight(value, "\r\n"), nil
}

func validateSingleStdinSource(sources ...bool) error {
	count := 0
	for _, source := range sources {
		if source {
			count++
		}
	}
	if count > 1 {
		return fmt.Errorf("only one stdin-backed secret source can be used per add command")
	}
	return nil
}

func readOptionalFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

func splitArgs(args []string) ([]string, []string, bool) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}

func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func (a *app) printHelp() {
	commands := []string{
		"authrun add <profile> --type mysql|mongo|redis|ssh|kube [options]",
		"authrun list",
		"authrun rm <profile>",
		"authrun exec <profile> -- <tool> [args...]",
		"authrun test <profile> [--tool <tool>]",
	}
	sort.Strings(commands)
	fmt.Fprintln(a.stdout, "authrun commands:")
	for _, command := range commands {
		fmt.Fprintln(a.stdout, " ", command)
	}
}
