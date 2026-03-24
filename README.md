# keep-run

`keeprun` is a small Go CLI for turning a command into a managed background task.

It is designed for long-running, non-interactive processes such as:

- `python httpserver.py`
- local API servers
- sync workers
- scripts that should keep running after you close the terminal

`keeprun` stores task state under `~/.config/keeprun/`, starts a per-user daemon, and can restore selected tasks after login on macOS and Linux.

## Features

- Run a command in the background and track it as a named task
- Optional wall-clock lifetime such as `3d`, `12h`, or `1h30m`
- Optional restart after user login
- List tasks, stop/start them, delete them, and inspect logs
- Global default config similar to `git config`
- macOS `LaunchAgent` support
- Linux `systemd --user` support

## Limitations

- Non-interactive tasks only
- Commands are executed directly, not through a shell
- Windows is not supported in v1
- Linux support assumes `systemd --user` is available

If you need shell features such as pipes, redirects, or shell expansion, wrap the command explicitly:

```bash
keeprun sh -lc 'python app.py >> app.log 2>&1'
```

## Installation

### Requirements

- Go 1.26+
- macOS or Linux

### Install with Go

```bash
go install github.com/jingyugao/keep-run@latest
```

This installs `keeprun` into your Go bin directory, usually:

- `$(go env GOPATH)/bin`
- or `$(go env GOBIN)` if you set it explicitly

Make sure that directory is on your `PATH`.

### Build from source

```bash
git clone https://github.com/jingyugao/keep-run.git
cd keep-run
go build -o keeprun .
```

## Quick start

Run a command as a managed background task:

```bash
keeprun python httpserver.py
```

Run with a name, a 3-day lifetime, and restart after login:

```bash
keeprun --name httpserver --life 3d --run-after-restart=true python httpserver.py
```

List all tasks:

```bash
keeprun ls
```

List running tasks only:

```bash
keeprun ps
```

View logs:

```bash
keeprun logs httpserver
```

Follow logs:

```bash
keeprun logs -f httpserver
```

Stop, start, and remove a task:

```bash
keeprun stop httpserver
keeprun start httpserver
keeprun rm httpserver
```

Force remove a running task:

```bash
keeprun rm --force httpserver
```

## Command usage

Task creation supports both forms:

```bash
keeprun [run flags] <cmd> [args...]
keeprun run [run flags] -- <cmd> [args...]
```

Use `keeprun run -- ...` if your command name collides with a management subcommand such as `run`, `logs`, or `start`.

### Run flags

- `--name <name>`: optional unique task name
- `--life <duration>`: max wall-clock life such as `30m`, `12h`, `3d`, `2w`
- `--run-after-restart=true|false`: restart task after user login
- `--cwd <dir>`: working directory for the command
- `--env KEY=VALUE`: add or override an environment variable
- `--env-pass KEY`: copy a variable from the current shell into the saved task environment

### Management commands

```bash
keeprun ls
keeprun ps
keeprun start <id|name>
keeprun stop <id|name>
keeprun rm <id|name> [--force]
keeprun logs <id|name> [-f] [--lines N]
keeprun config get|set|unset|list
keeprun daemon install|uninstall|status
keeprun help
```

## Configuration

Global config is stored at:

```text
~/.config/keeprun/config.toml
```

Supported config keys:

- `defaults.life`
- `defaults.run_after_restart`
- `defaults.stop_timeout`
- `defaults.env_pass`
- `logs.tail_lines`

Built-in defaults:

```toml
[defaults]
life = ""
run_after_restart = false
stop_timeout = "10s"
env_pass = []

[logs]
tail_lines = 200
```

Examples:

```bash
keeprun config set defaults.life 3d
keeprun config set defaults.run_after_restart true
keeprun config set defaults.env_pass VIRTUAL_ENV,PYENV_VERSION
keeprun config set logs.tail_lines 500
keeprun config list
```

## Data layout

`keeprun` stores runtime data under:

```text
~/.config/keeprun/
```

Important paths:

- `~/.config/keeprun/config.toml`
- `~/.config/keeprun/tasks/<task-id>.json`
- `~/.config/keeprun/logs/<task-id>.log`
- `~/.config/keeprun/run/daemon.sock`
- `~/.config/keeprun/run/daemon.pid`

## Daemon behavior

- The first mutating command installs or starts the per-user daemon automatically
- Tasks marked with `--run-after-restart=true` are restarted after the next user login
- `life` is a wall-clock deadline, not accumulated runtime
- When a task expires, it is stopped and marked `expired`
- Logs are stored as combined stdout/stderr with timestamps

You can also manage the daemon explicitly:

```bash
keeprun daemon install
keeprun daemon status
keeprun daemon uninstall
```

Platform integration:

- macOS: `~/Library/LaunchAgents/com.keeprun.daemon.plist`
- Linux: `~/.config/systemd/user/keeprund.service`

## Examples

Run a Python HTTP server for 3 days:

```bash
keeprun --name httpserver --life 3d python httpserver.py
```

Run a project command from a specific directory:

```bash
keeprun --name api --cwd /path/to/project ./bin/api-server
```

Pass through virtualenv information:

```bash
keeprun --name worker --env-pass VIRTUAL_ENV --env-pass PATH python worker.py
```

Use config defaults so later runs are shorter:

```bash
keeprun config set defaults.life 3d
keeprun config set defaults.run_after_restart true
keeprun --name job python job.py
```
