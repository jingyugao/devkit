# devkit

`devkit` is a multi-tool Go repository. The current binaries are:

- `keeprun`: run and supervise long-lived local commands in the background
- `xrun`: launch database/queue CLIs with stored connection profiles and encrypted secrets

## Installation

### Requirements

- Go 1.26+
- macOS or Linux

### Install with Go

```bash
go install github.com/jingyugao/devkit/cmd/keeprun@latest
go install github.com/jingyugao/devkit/cmd/xrun@latest
```

### Build from Source

```bash
git clone https://github.com/jingyugao/devkit.git
cd devkit
go build -o keeprun ./cmd/keeprun
go build -o xrun ./cmd/xrun
```

## keeprun

`keeprun` turns a non-interactive command into a managed background task.

### Features

- Run a command in the background and track it as a named task
- Optional wall-clock lifetime such as `3d`, `12h`, or `1h30m`
- Automatic restart after process exit
- Automatic task rehydration when the daemon starts again
- List tasks, stop/start them, remove them, and inspect logs
- Global default config similar to `git config`
- macOS `LaunchAgent` support
- Linux `systemd --user` support

### Important Restrictions

- Only non-interactive commands are supported
- Commands are executed directly, not through a shell
- Windows is not supported in v1
- Linux support assumes `systemd --user` is available
- Built-in `keeprun` subcommand names are reserved and cannot be launched as managed child commands, even with `keeprun run -- ...`

If you need shell features such as pipes, redirects, or shell expansion, wrap the command explicitly:

```bash
keeprun sh -lc 'python app.py >> app.log 2>&1'
```

### Usage

Task creation supports both forms:

```bash
keeprun [run flags] <cmd> [args...]
keeprun run [run flags] -- <cmd> [args...]
```

Run flags:

- `--name <name>`: optional unique task name
- `--life <duration>`: max wall-clock life such as `30m`, `12h`, `3d`, `2w`
- `--cwd <dir>`: working directory for the command
- `--env KEY=VALUE`: add or override an environment variable
- `--env-pass KEY`: copy a variable from the current shell into the saved task environment

Management commands:

```bash
keeprun ls
keeprun ps
keeprun start <id|name>
keeprun start --all
keeprun stop <id|name>
keeprun rm <id|name> [--force]
keeprun logs <id|name> [-f] [--lines N]
keeprun config get|set|unset|list
keeprun daemon install|uninstall|status
keeprun help
```

### Config and Data

Global config is stored at `~/.config/keeprun/config.toml`.

Built-in defaults:

```toml
[defaults]
life = ""
stop_timeout = "10s"
env_pass = []

[logs]
tail_lines = 200
```

Runtime data lives under `~/.config/keeprun/`:

- `config.toml`
- `tasks/<task-id>.json`
- `logs/<task-id>.log`
- `run/daemon.sock`
- `run/daemon.pid`

### Examples

```bash
keeprun --name httpserver --life 3d python httpserver.py
keeprun --name api --cwd /path/to/project ./bin/api-server
keeprun --name worker --env-pass VIRTUAL_ENV --env-pass PATH python worker.py
keeprun config set defaults.life 3d
keeprun daemon status
```

## xrun

`xrun` stores connection profiles and secrets, then launches supported CLIs with the right authentication context.

### v1 Scope

- Supported profile types: `mysql`, `mongo`, `redis`
- Supported tools: `mysql`, `mongosh`, `redis-cli`
- Profile metadata is stored in `~/.config/xrun/profiles.toml`
- Secrets are stored in `~/.config/xrun/secrets.enc`
- The encrypted secrets file uses a master key stored in the OS keyring

Linux requires a working Secret Service-compatible keyring. There is no plaintext or passphrase fallback in v1.

### Commands

```bash
xrun add <profile> --type mysql|mongo|redis --host HOST [options]
xrun list
xrun rm <profile>
xrun exec <profile> -- <tool> [args...]
xrun test <profile> [--tool <tool>]
```

Common `xrun add` options:

- `--type <mysql|mongo|redis>`
- `--host <host>`
- `--port <port>`
- `--username <username>`
- `--database <name>`
- `--tls`
- `--tls-ca-file <path>`
- `--secret-stdin`
- `--secret-env <ENV_NAME>`

Mongo-only option:

- `--auth-database <name>`

MySQL-only option:

- `--socket <path>`

### Examples

Add a MySQL profile and read the password from a terminal prompt:

```bash
xrun add user_db --type mysql --host 127.0.0.1 --port 3306 --username app --database users
```

Add a Redis profile from an environment variable:

```bash
export REDIS_PASSWORD='secret'
xrun add cache --type redis --host 127.0.0.1 --port 6379 --username default --secret-env REDIS_PASSWORD
```

List saved profiles:

```bash
xrun list
```

Run a CLI with the stored profile:

```bash
xrun exec user_db -- mysql -e 'SELECT NOW()'
xrun exec cache -- redis-cli PING
xrun exec docs -- mongosh
```

Validate a stored profile:

```bash
xrun test user_db
xrun test cache --tool redis-cli
```
