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
	"github.com/jingyugao/devkit/internal/authrun/importer"
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
	ProfileTypeForTool(string) (profile.Type, error)
	PrepareKubeAggregateExec([]profile.Profile, []store.Secret, string, []string) (adapter.Prepared, error)
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
	cmdOutput  importer.CommandOutput
	isTTY      func() bool
	readSecret func() (string, error)
}

type secretInput struct {
	stdin                   bool
	envName                 string
	filePath                string
	allowPrompt             bool
	required                bool
	preserveTrailingNewline bool
	label                   string
}

var errNoToolShortcut = errors.New("no tool shortcut")

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
		cmdOutput: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		},
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
	case "import":
		return a.runImport(args[1:])
	case "ls", "list":
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
		if err := a.runToolShortcut(args); err != nil {
			if errors.Is(err, errNoToolShortcut) {
				return fmt.Errorf("unknown command %q", args[0])
			}
			return err
		}
		return nil
	}
}

func (a *app) runImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("import requires a target: ssh, kube, mysql, or all")
	}

	switch args[0] {
	case "ssh":
		return a.runImportSSH(args[1:])
	case "kube":
		return a.runImportKube(args[1:])
	case "mysql":
		return a.runImportMySQL(args[1:])
	case "all":
		return a.runImportAll(args[1:])
	default:
		return fmt.Errorf("unknown import target %q", args[0])
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
			stdin:                   *privateKeyStdin,
			envName:                 *privateKeyEnv,
			filePath:                *privateKeyFile,
			preserveTrailingNewline: true,
			label:                   "private key",
		},
		secretInput{
			stdin:   *passphraseStdin,
			envName: *passphraseEnv,
			label:   "passphrase",
		},
		secretInput{
			stdin:                   *clientKeyStdin,
			envName:                 *clientKeyEnv,
			filePath:                *clientKeyFile,
			preserveTrailingNewline: true,
			label:                   "client key",
		},
		clientCertificate != "",
	)
	if err != nil {
		return err
	}

	if err := a.saveProfileAndSecret(p, secret); err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "added\t%s\t%s\n", p.Name, p.Type)
	return nil
}

func (a *app) runImportSSH(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("import ssh requires a profile name or user@host target")
	}
	if looksLikeSSHImportTarget(args[0]) {
		return a.runImportSSHCommand(args)
	}

	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("import-ssh", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	alias := fs.String("host", "", "")
	configPath := fs.String("config", "", "")
	knownHostsPath := fs.String("known-hosts-file", "", "")
	passphraseEnv := fs.String("passphrase-env", "", "")
	passphraseStdin := fs.Bool("passphrase-stdin", false, "")
	passphrasePrompt := fs.Bool("passphrase-prompt", false, "")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := validateSingleStdinSource(*passphraseStdin); err != nil {
		return err
	}
	if _, err := a.profiles.Get(name); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}

	p, secret, err := importer.ImportSSH(importer.SSHInput{
		Name:           name,
		Alias:          *alias,
		ConfigPath:     *configPath,
		KnownHostsPath: *knownHostsPath,
	})
	if err != nil {
		return err
	}
	secret.Passphrase, err = a.resolveSecretInput(secretInput{
		stdin:       *passphraseStdin,
		envName:     *passphraseEnv,
		allowPrompt: *passphrasePrompt,
		required:    false,
		label:       "passphrase",
	})
	if err != nil {
		return err
	}
	if err := a.saveProfileAndSecret(p, secret); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "imported\t%s\t%s\n", p.Name, p.Type)
	return nil
}

func (a *app) runImportSSHCommand(args []string) error {
	target := strings.TrimSpace(args[0])
	raw, err := parseSSHImportCommandArgs(args[1:])
	if err != nil {
		return err
	}
	if err := validateSingleStdinSource(raw.passphraseStdin); err != nil {
		return err
	}

	p, secret, err := importer.ImportSSHCommand(importer.SSHCommandInput{
		Target:         target,
		Args:           raw.sshArgs,
		KnownHostsPath: raw.knownHostsPath,
	}, a.cmdOutput)
	if err != nil {
		return err
	}
	if _, err := a.profiles.Get(p.Name); err == nil {
		return fmt.Errorf("profile %q already exists", p.Name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}

	secret.Passphrase, err = a.resolveSecretInput(secretInput{
		stdin:       raw.passphraseStdin,
		envName:     raw.passphraseEnv,
		allowPrompt: raw.passphrasePrompt,
		required:    false,
		label:       "passphrase",
	})
	if err != nil {
		return err
	}
	if err := a.saveProfileAndSecret(p, secret); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "imported\t%s\t%s\n", p.Name, p.Type)
	return nil
}

func (a *app) runImportKube(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("import-kube requires exactly one profile name")
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("import-kube", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	contextName := fs.String("context", "", "")
	kubeconfigPath := fs.String("kubeconfig", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if _, err := a.profiles.Get(name); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}

	p, secret, err := importer.ImportKube(importer.KubeInput{
		Name:           name,
		Context:        *contextName,
		KubeconfigPath: *kubeconfigPath,
	})
	if err != nil {
		return err
	}
	if err := a.saveProfileAndSecret(p, secret); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "imported\t%s\t%s\n", p.Name, p.Type)
	return nil
}

func (a *app) runImportMySQL(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("import-mysql requires exactly one profile name")
	}
	name := strings.TrimSpace(args[0])

	fs := flag.NewFlagSet("import-mysql", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	loginPath := fs.String("login-path", "", "")
	database := fs.String("database", "", "")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if _, err := a.profiles.Get(name); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}

	p, secret, err := importer.ImportMySQL(importer.MySQLInput{
		Name:      name,
		LoginPath: *loginPath,
		Database:  *database,
	}, a.cmdOutput)
	if err != nil {
		return err
	}
	if err := a.saveProfileAndSecret(p, secret); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "imported\t%s\t%s\n", p.Name, p.Type)
	return nil
}

func (a *app) runImportAll(args []string) error {
	fs := flag.NewFlagSet("import-all", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	sshSpec := fs.String("ssh", "", "")
	sshConfig := fs.String("ssh-config", "", "")
	sshKnownHosts := fs.String("ssh-known-hosts-file", "", "")
	sshPassphraseEnv := fs.String("ssh-passphrase-env", "", "")
	sshPassphraseStdin := fs.Bool("ssh-passphrase-stdin", false, "")
	sshPassphrasePrompt := fs.Bool("ssh-passphrase-prompt", false, "")
	kubeSpec := fs.String("kube", "", "")
	kubeconfig := fs.String("kubeconfig", "", "")
	mysqlSpec := fs.String("mysql", "", "")
	mysqlLoginPath := fs.String("login-path", "", "")
	mysqlDatabase := fs.String("mysql-database", "", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := validateSingleStdinSource(*sshPassphraseStdin); err != nil {
		return err
	}

	imports := 0
	created := make([]string, 0, 3)
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_ = a.profiles.Delete(created[i])
			_ = a.secrets.Delete(context.Background(), created[i])
		}
	}

	if strings.TrimSpace(*sshSpec) != "" {
		imports++
		profileName, alias, err := parseImportSpec(*sshSpec)
		if err != nil {
			return fmt.Errorf("parse --ssh: %w", err)
		}
		if alias == "" {
			alias = profileName
		}
		if err := a.ensureProfileMissing(profileName); err != nil {
			return err
		}
		p, secret, err := importer.ImportSSH(importer.SSHInput{
			Name:           profileName,
			Alias:          alias,
			ConfigPath:     *sshConfig,
			KnownHostsPath: *sshKnownHosts,
		})
		if err != nil {
			return err
		}
		secret.Passphrase, err = a.resolveSecretInput(secretInput{
			stdin:       *sshPassphraseStdin,
			envName:     *sshPassphraseEnv,
			allowPrompt: *sshPassphrasePrompt,
			required:    false,
			label:       "passphrase",
		})
		if err != nil {
			return err
		}
		if err := a.saveProfileAndSecret(p, secret); err != nil {
			return err
		}
		created = append(created, p.Name)
		fmt.Fprintf(a.stdout, "imported\t%s\t%s\n", p.Name, p.Type)
	}

	if strings.TrimSpace(*kubeSpec) != "" {
		imports++
		profileName, contextName, err := parseImportSpec(*kubeSpec)
		if err != nil {
			rollback()
			return fmt.Errorf("parse --kube: %w", err)
		}
		if err := a.ensureProfileMissing(profileName); err != nil {
			rollback()
			return err
		}
		p, secret, err := importer.ImportKube(importer.KubeInput{
			Name:           profileName,
			Context:        contextName,
			KubeconfigPath: *kubeconfig,
		})
		if err != nil {
			rollback()
			return err
		}
		if err := a.saveProfileAndSecret(p, secret); err != nil {
			rollback()
			return err
		}
		created = append(created, p.Name)
		fmt.Fprintf(a.stdout, "imported\t%s\t%s\n", p.Name, p.Type)
	}

	if strings.TrimSpace(*mysqlSpec) != "" {
		imports++
		profileName := strings.TrimSpace(*mysqlSpec)
		if err := a.ensureProfileMissing(profileName); err != nil {
			rollback()
			return err
		}
		p, secret, err := importer.ImportMySQL(importer.MySQLInput{
			Name:      profileName,
			LoginPath: *mysqlLoginPath,
			Database:  *mysqlDatabase,
		}, a.cmdOutput)
		if err != nil {
			rollback()
			return err
		}
		if err := a.saveProfileAndSecret(p, secret); err != nil {
			rollback()
			return err
		}
		created = append(created, p.Name)
		fmt.Fprintf(a.stdout, "imported\t%s\t%s\n", p.Name, p.Type)
	}

	if imports == 0 {
		return fmt.Errorf("import-all requires at least one of --ssh, --kube, or --mysql")
	}
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
		case p.Host != "" && p.Port > 0:
			target = fmt.Sprintf("%s:%d", p.Host, p.Port)
		case p.Type == profile.TypeMySQL && p.MySQLLoginPath != "":
			target = "login-path:" + p.MySQLLoginPath
		}

		detail := "-"
		switch p.Type {
		case profile.TypeMongo:
			detail = p.Database
			if p.AuthDatabase != "" {
				detail = p.Database + " (auth:" + p.AuthDatabase + ")"
			}
		case profile.TypeMySQL:
			if p.Database != "" {
				detail = p.Database
			}
			if p.MySQLLoginPath != "" {
				if detail == "-" {
					detail = "login-path:" + p.MySQLLoginPath
				} else {
					detail = detail + " (login-path:" + p.MySQLLoginPath + ")"
				}
			}
		case profile.TypeRedis:
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

func (a *app) saveProfileAndSecret(p profile.Profile, secret store.Secret) error {
	if err := a.profiles.Add(p); err != nil {
		return err
	}
	if err := a.secrets.Put(context.Background(), p.Name, secret); err != nil {
		_ = a.profiles.Delete(p.Name)
		return err
	}
	return nil
}

func (a *app) ensureProfileMissing(name string) error {
	if _, err := a.profiles.Get(name); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}
	return nil
}

func parseImportSpec(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", fmt.Errorf("value cannot be empty")
	}
	name, source, hasSource := strings.Cut(value, ":")
	name = strings.TrimSpace(name)
	source = strings.TrimSpace(source)
	if name == "" {
		return "", "", fmt.Errorf("profile name cannot be empty")
	}
	if hasSource && source == "" {
		return "", "", fmt.Errorf("source name cannot be empty")
	}
	return name, source, nil
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

func (a *app) runToolShortcut(args []string) error {
	if len(args) == 0 {
		return errNoToolShortcut
	}
	tool := args[0]
	profileType, err := a.adapters.ProfileTypeForTool(tool)
	if err != nil {
		if errors.Is(err, adapter.ErrUnsupportedTool) {
			return errNoToolShortcut
		}
		return err
	}
	if tool == "ssh" {
		return a.runSSHShortcut(args[1:])
	}

	profiles, err := a.profiles.List()
	if err != nil {
		return err
	}
	matches := make([]profile.Profile, 0, len(profiles))
	for _, item := range profiles {
		if item.Type == profileType {
			matches = append(matches, item)
		}
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("no %s profile configured for tool %q", profileType, tool)
	case 1:
		secret, err := a.secrets.Get(context.Background(), matches[0].Name)
		if err != nil {
			return err
		}
		prepared, err := a.adapters.PrepareExec(matches[0], secret, tool, args[1:])
		if err != nil {
			return err
		}
		return a.runPrepared(prepared)
	default:
		if profileType == profile.TypeKube && tool == "k9s" {
			secrets := make([]store.Secret, 0, len(matches))
			for _, item := range matches {
				secret, err := a.secrets.Get(context.Background(), item.Name)
				if err != nil {
					return err
				}
				secrets = append(secrets, secret)
			}
			prepared, err := a.adapters.PrepareKubeAggregateExec(matches, secrets, tool, args[1:])
			if err != nil {
				return err
			}
			return a.runPrepared(prepared)
		}
		names := make([]string, 0, len(matches))
		for _, item := range matches {
			names = append(names, item.Name)
		}
		sort.Strings(names)
		return fmt.Errorf("tool %q matches multiple %s profiles (%s); use `authrun exec <profile> -- %s`", tool, profileType, strings.Join(names, ", "), tool)
	}
}

func (a *app) runSSHShortcut(args []string) error {
	parsed, err := parseSSHShortcutArgs(args)
	if err != nil {
		return err
	}
	if !parsed.hasDestination {
		return a.runPrepared(adapter.Prepared{
			Path: "ssh",
			Args: append([]string{}, args...),
		})
	}
	if p, secret, err := a.loadProfileAndSecret(parsed.destination); err == nil && p.Type == profile.TypeSSH {
		prepared, err := a.adapters.PrepareExec(p, secret, "ssh", args[1:])
		if err != nil {
			return err
		}
		return a.runPrepared(prepared)
	} else if err != nil && !errors.Is(err, profile.ErrNotFound) && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return a.runPrepared(adapter.Prepared{
		Path: "ssh",
		Args: append([]string{}, args...),
	})
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
	if input.filePath != "" && input.preserveTrailingNewline {
		return value, nil
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

type sshImportCommandArgs struct {
	sshArgs          []string
	knownHostsPath   string
	passphraseEnv    string
	passphraseStdin  bool
	passphrasePrompt bool
}

func looksLikeSSHImportTarget(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "-") {
		return false
	}
	return strings.Contains(value, "@")
}

func parseSSHImportCommandArgs(args []string) (sshImportCommandArgs, error) {
	var parsed sshImportCommandArgs

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--passphrase-stdin":
			parsed.passphraseStdin = true
		case arg == "--passphrase-prompt":
			parsed.passphrasePrompt = true
		case strings.HasPrefix(arg, "--passphrase-env="):
			parsed.passphraseEnv = strings.TrimSpace(strings.TrimPrefix(arg, "--passphrase-env="))
		case arg == "--passphrase-env":
			if i+1 >= len(args) {
				return sshImportCommandArgs{}, fmt.Errorf("--passphrase-env requires a value")
			}
			i++
			parsed.passphraseEnv = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--known-hosts-file="):
			parsed.knownHostsPath = strings.TrimSpace(strings.TrimPrefix(arg, "--known-hosts-file="))
		case arg == "--known-hosts-file":
			if i+1 >= len(args) {
				return sshImportCommandArgs{}, fmt.Errorf("--known-hosts-file requires a value")
			}
			i++
			parsed.knownHostsPath = strings.TrimSpace(args[i])
		default:
			parsed.sshArgs = append(parsed.sshArgs, arg)
		}
	}

	return parsed, nil
}

type sshShortcutArgs struct {
	hasDestination bool
	destination    string
	resolveArgs    []string
}

func parseSSHShortcutArgs(args []string) (sshShortcutArgs, error) {
	var parsed sshShortcutArgs

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !parsed.hasDestination && isSSHCLIOption(arg) {
			parsed.resolveArgs = append(parsed.resolveArgs, arg)
			if sshCLIOptionConsumesValue(arg) {
				if i+1 >= len(args) {
					return sshShortcutArgs{}, fmt.Errorf("ssh option %q requires a value", arg)
				}
				i++
				parsed.resolveArgs = append(parsed.resolveArgs, args[i])
			}
			continue
		}
		if !parsed.hasDestination {
			parsed.destination = arg
			parsed.hasDestination = true
			continue
		}
		break
	}

	return parsed, nil
}

func isSSHCLIOption(arg string) bool {
	return strings.HasPrefix(arg, "-") && arg != "-"
}

func sshCLIOptionConsumesValue(arg string) bool {
	switch arg {
	case "-b", "-c", "-D", "-E", "-e", "-F", "-I", "-i", "-J", "-L", "-l", "-m", "-O", "-o", "-p", "-Q", "-R", "-S", "-W", "-w":
		return true
	default:
		return false
	}
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
		"authrun import all [--ssh <profile[:host]>] [--kube <profile[:context]>] [--mysql <profile>] [--login-path <login-path>]",
		"authrun import kube <profile> [--context <context>] [--kubeconfig <path>]",
		"authrun import mysql <profile> [--login-path <login-path>] [--database <name>]",
		"authrun import ssh <profile> [--host <alias>] [--config <path>]",
		"authrun import ssh <user@host> [ssh options]",
		"authrun ls",
		"authrun rm <profile>",
		"authrun exec <profile> -- <tool> [args...]",
		"authrun test <profile> [--tool <tool>]",
	}
	sort.Strings(commands)
	fmt.Fprintln(a.stdout, "authrun commands:")
	for _, command := range commands {
		fmt.Fprintln(a.stdout, " ", command)
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "tool shortcuts:")
	fmt.Fprintln(a.stdout, "  authrun mysql [args...]")
	fmt.Fprintln(a.stdout, "  authrun k9s")
	fmt.Fprintln(a.stdout, "  authrun ssh [ssh args...]")
	fmt.Fprintln(a.stdout, "  authrun k9s merges all imported kube profiles into a temporary kubeconfig")
	fmt.Fprintln(a.stdout, "  authrun kubectl [args...] remains explicit when multiple kube profiles exist")
	fmt.Fprintln(a.stdout, "  authrun ssh [ssh args...] is compatible with ssh command syntax")
	fmt.Fprintln(a.stdout, "  authrun ssh <stored-profile-name> uses authrun-managed ssh secrets")
}
