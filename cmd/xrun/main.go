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

	"github.com/jingyugao/devkit/internal/xrun/adapter"
	"github.com/jingyugao/devkit/internal/xrun/keyring"
	"github.com/jingyugao/devkit/internal/xrun/profile"
	"github.com/jingyugao/devkit/internal/xrun/store"
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
	PrepareExec(profile.Profile, string, string, []string) (adapter.Prepared, error)
	PrepareTest(profile.Profile, string, string) (adapter.Prepared, error)
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
	tlsEnabled := fs.Bool("tls", false, "")
	tlsCAFile := fs.String("tls-ca-file", "", "")
	socket := fs.String("socket", "", "")
	secretFromStdin := fs.Bool("secret-stdin", false, "")
	secretEnv := fs.String("secret-env", "", "")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	secret, err := a.resolveSecretSource(*secretFromStdin, *secretEnv)
	if err != nil {
		return err
	}

	p := profile.Profile{
		Name:         name,
		Type:         profile.Type(*profileType),
		Host:         *host,
		Port:         *port,
		Username:     *username,
		Database:     *database,
		AuthDatabase: *authDatabase,
		TLS:          *tlsEnabled,
		TLSCAFile:    *tlsCAFile,
		Socket:       *socket,
	}.Normalized()

	if err := p.Validate(); err != nil {
		return err
	}
	if _, err := a.profiles.Get(p.Name); err == nil {
		return fmt.Errorf("profile %q already exists", p.Name)
	} else if !errors.Is(err, profile.ErrNotFound) {
		return err
	}

	if err := a.profiles.Add(p); err != nil {
		return err
	}
	if err := a.secrets.Put(context.Background(), p.Name, store.Secret{Password: secret}); err != nil {
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
	fmt.Fprintln(tw, "NAME\tTYPE\tHOST\tPORT\tUSERNAME\tDATABASE\tTLS")
	for _, p := range profiles {
		database := p.Database
		if p.Type == profile.TypeMongo && p.AuthDatabase != "" {
			database = p.Database + " (auth:" + p.AuthDatabase + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%t\n",
			p.Name,
			p.Type,
			p.Host,
			p.Port,
			p.Username,
			database,
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
	prepared, err := a.adapters.PrepareExec(p, secret.Password, after[0], after[1:])
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
	prepared, err := a.adapters.PrepareTest(p, secret.Password, *tool)
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

func (a *app) resolveSecretSource(secretFromStdin bool, secretEnv string) (string, error) {
	switch {
	case secretFromStdin && secretEnv != "":
		return "", fmt.Errorf("choose only one of --secret-stdin or --secret-env")
	case secretFromStdin:
		data, err := io.ReadAll(a.stdin)
		if err != nil {
			return "", err
		}
		secret := strings.TrimRight(string(data), "\r\n")
		if secret == "" {
			return "", fmt.Errorf("secret from stdin is empty")
		}
		return secret, nil
	case secretEnv != "":
		value, ok := os.LookupEnv(secretEnv)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("environment variable %q is empty or unset", secretEnv)
		}
		return value, nil
	case a.isTTY():
		secret, err := a.readSecret()
		if err != nil {
			return "", err
		}
		if secret == "" {
			return "", fmt.Errorf("secret cannot be empty")
		}
		return secret, nil
	default:
		return "", fmt.Errorf("provide --secret-stdin, --secret-env, or run in a terminal for interactive secret entry")
	}
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
		"xrun add <profile> --type mysql|mongo|redis --host HOST [options]",
		"xrun list",
		"xrun rm <profile>",
		"xrun exec <profile> -- <tool> [args...]",
		"xrun test <profile> [--tool <tool>]",
	}
	sort.Strings(commands)
	fmt.Fprintln(a.stdout, "xrun commands:")
	for _, command := range commands {
		fmt.Fprintln(a.stdout, " ", command)
	}
}
