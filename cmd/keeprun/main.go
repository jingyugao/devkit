package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jingyugao/devkit/internal/client"
	"github.com/jingyugao/devkit/internal/config"
	"github.com/jingyugao/devkit/internal/daemon"
	"github.com/jingyugao/devkit/internal/daemonctl"
	"github.com/jingyugao/devkit/internal/durationutil"
	"github.com/jingyugao/devkit/internal/ipc"
	"github.com/jingyugao/devkit/internal/task"
)

var reservedSubcommands = map[string]struct{}{
	"config":  {},
	"daemon":  {},
	"help":    {},
	"logs":    {},
	"ls":      {},
	"ps":      {},
	"rm":      {},
	"run":     {},
	"start":   {},
	"stop":    {},
	"version": {},
}

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func normalizeInterspersedFlags(args []string, valueFlags map[string]bool) ([]string, error) {
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			name, _, hasValue := strings.Cut(arg, "=")
			flags = append(flags, arg)
			if valueFlags[name] && !hasValue {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("flag needs an argument: %s", name)
				}
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positionals = append(positionals, arg)
	}

	return append(flags, positionals...), nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	first := args[0]
	if _, ok := reservedSubcommands[first]; ok {
		switch first {
		case "run":
			return runCreate(args[1:])
		case "ls":
			return runList(false)
		case "ps":
			return runList(true)
		case "start":
			return runStartStop("start", args[1:])
		case "stop":
			return runStartStop("stop", args[1:])
		case "rm":
			return runRemove(args[1:])
		case "logs":
			return runLogs(args[1:])
		case "config":
			return runConfig(args[1:])
		case "daemon":
			return runDaemon(args[1:])
		case "version":
			fmt.Println("keeprun dev")
			return nil
		case "help":
			printHelp()
			return nil
		}
	}
	return runCreate(args)
}

func runCreate(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	req, err := parseRunRequest(cfg, args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := daemonctl.Ensure(ctx, true); err != nil {
		return err
	}
	record, err := client.New().CreateTask(ctx, req)
	if err != nil {
		return err
	}
	fmt.Printf("%s\t%s\t%s\n", record.DisplayID(), displayName(record), record.State.RuntimeState)
	return nil
}

func runList(runningOnly bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := daemonctl.Ensure(ctx, false); err != nil {
		return err
	}
	records, err := client.New().ListTasks(ctx, runningOnly)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tPID\tRESTARTS\tEXPIRES\tCOMMAND")
	for _, record := range records {
		expires := "-"
		if record.Spec.ExpiresAt != nil {
			expires = record.Spec.ExpiresAt.Format(time.RFC3339)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			record.DisplayID(),
			displayName(record),
			record.State.RuntimeState,
			record.State.PID,
			record.State.RestartCount,
			expires,
			strings.Join(record.Spec.Argv, " "),
		)
	}
	return tw.Flush()
}

func runStartStop(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *all {
		if action != "start" {
			return fmt.Errorf("--all is only supported for start")
		}
		if len(fs.Args()) != 0 {
			return fmt.Errorf("start --all does not take a task id or name")
		}
		return runStartAll()
	}
	if len(fs.Args()) != 1 {
		return fmt.Errorf("%s requires a task id or name", action)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	persist := action == "start"
	if err := daemonctl.Ensure(ctx, persist); err != nil {
		return err
	}
	c := client.New()
	var (
		record task.Record
		err    error
	)
	if action == "start" {
		record, err = c.StartTask(ctx, fs.Args()[0])
	} else {
		record, err = c.StopTask(ctx, fs.Args()[0])
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s\t%s\t%s\n", record.DisplayID(), displayName(record), record.State.RuntimeState)
	return nil
}

func runStartAll() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := daemonctl.Ensure(ctx, true); err != nil {
		return err
	}
	records, err := client.New().StartAll(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tRESTARTS")
	for _, record := range records {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n",
			record.DisplayID(),
			displayName(record),
			record.State.RuntimeState,
			record.State.RestartCount,
		)
	}
	return tw.Flush()
}

func runRemove(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	force := fs.Bool("force", false, "")
	normalizedArgs, err := normalizeInterspersedFlags(args, nil)
	if err != nil {
		return err
	}
	if err := fs.Parse(normalizedArgs); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return fmt.Errorf("rm requires a task id or name")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := daemonctl.Ensure(ctx, false); err != nil {
		return err
	}
	return client.New().RemoveTask(ctx, fs.Args()[0], *force)
}

func runLogs(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	follow := fs.Bool("f", false, "")
	lines := fs.Int("lines", cfg.Logs.TailLines, "")
	normalizedArgs, err := normalizeInterspersedFlags(args, map[string]bool{"--lines": true})
	if err != nil {
		return err
	}
	if err := fs.Parse(normalizedArgs); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return fmt.Errorf("logs requires a task id or name")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := daemonctl.Ensure(ctx, false); err != nil {
		return err
	}
	reader, err := client.New().Logs(ctx, fs.Args()[0], *follow, *lines)
	if err != nil {
		return err
	}
	defer reader.Close()
	_, err = io.Copy(os.Stdout, reader)
	return err
}

func runConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config requires get, set, unset, or list")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch args[0] {
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("config get requires a key")
		}
		value, err := config.Get(cfg, args[1])
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("config set requires a key and value")
		}
		if err := config.Set(&cfg, args[1], args[2]); err != nil {
			return err
		}
		return config.Save(cfg)
	case "unset":
		if len(args) != 2 {
			return fmt.Errorf("config unset requires a key")
		}
		if err := config.Unset(&cfg, args[1]); err != nil {
			return err
		}
		return config.Save(cfg)
	case "list":
		keys := config.Keys()
		sort.Strings(keys)
		for _, key := range keys {
			value, err := config.Get(cfg, key)
			if err != nil {
				return err
			}
			fmt.Printf("%s=%s\n", key, value)
		}
		return nil
	default:
		return fmt.Errorf("unknown config command %q", args[0])
	}
}

func runDaemon(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("daemon requires install, uninstall, status, or serve")
	}
	switch args[0] {
	case "serve":
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		server, err := daemon.New(cfg)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return server.Run(ctx)
	case "install":
		if err := daemonctl.InstallService(); err != nil {
			return err
		}
		fmt.Println("installed")
		return nil
	case "uninstall":
		if err := daemonctl.UninstallService(); err != nil {
			return err
		}
		fmt.Println("uninstalled")
		return nil
	case "status":
		status, err := daemonctl.ServiceStatus()
		if err != nil {
			fmt.Printf("service status unavailable: %v\n", err)
		} else {
			fmt.Printf("installed=%t running=%t\n", status.Installed, status.Running)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.New().Ping(ctx); err != nil {
			fmt.Println("daemon=unreachable")
		} else {
			fmt.Println("daemon=reachable")
		}
		return nil
	default:
		return fmt.Errorf("unknown daemon command %q", args[0])
	}
}

func buildTaskEnv(cfg config.Config, envPass []string, envVars []string) map[string]string {
	result := map[string]string{}
	baseKeys := []string{"PATH", "HOME", "SHELL", "USER", "LANG", "TERM"}
	for _, key := range baseKeys {
		if value, ok := os.LookupEnv(key); ok {
			result[key] = value
		}
	}
	for _, item := range os.Environ() {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.HasPrefix(parts[0], "LC_") {
			result[parts[0]] = parts[1]
		}
	}
	for _, key := range cfg.Defaults.EnvPass {
		if value, ok := os.LookupEnv(key); ok {
			result[key] = value
		}
	}
	for _, key := range envPass {
		if value, ok := os.LookupEnv(key); ok {
			result[key] = value
		}
	}
	for _, assignment := range envVars {
		parts := strings.SplitN(assignment, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		result[parts[0]] = parts[1]
	}
	return result
}

func parseRunRequest(cfg config.Config, args []string) (ipc.CreateTaskRequest, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "")
	life := fs.String("life", cfg.Defaults.Life, "")
	cwd := fs.String("cwd", "", "")
	var envVars multiFlag
	var envPass multiFlag
	fs.Var(&envVars, "env", "")
	fs.Var(&envPass, "env-pass", "")
	if err := fs.Parse(args); err != nil {
		return ipc.CreateTaskRequest{}, err
	}
	argv := fs.Args()
	if len(argv) == 0 {
		return ipc.CreateTaskRequest{}, fmt.Errorf("missing command")
	}
	if reservedCommandName(argv[0]) {
		return ipc.CreateTaskRequest{}, fmt.Errorf("command %q is reserved by keeprun", filepath.Base(argv[0]))
	}
	if *cwd == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			return ipc.CreateTaskRequest{}, err
		}
		*cwd = currentDir
	}
	if _, err := os.Stat(*cwd); err != nil {
		return ipc.CreateTaskRequest{}, fmt.Errorf("invalid cwd: %w", err)
	}
	if *life != "" {
		if _, err := durationutil.Parse(*life); err != nil {
			return ipc.CreateTaskRequest{}, err
		}
	}
	return ipc.CreateTaskRequest{
		Name: *name,
		Argv: argv,
		Cwd:  *cwd,
		Env:  buildTaskEnv(cfg, envPass, envVars),
		Life: *life,
	}, nil
}

func reservedCommandName(command string) bool {
	_, ok := reservedSubcommands[filepath.Base(command)]
	return ok
}

func displayName(record task.Record) string {
	if record.Spec.Name != "" {
		return record.Spec.Name
	}
	return "-"
}

func printHelp() {
	fmt.Println(`keeprun commands:
  keeprun [run flags] <cmd> [args...]
  keeprun run [flags] -- <cmd> [args...]
  keeprun ls
  keeprun ps
  keeprun start <id|name>
  keeprun start --all
  keeprun stop <id|name>
  keeprun rm <id|name> [--force]
  keeprun logs <id|name> [-f] [--lines N]
  keeprun config get|set|unset|list
  keeprun daemon install|uninstall|status`)
}
