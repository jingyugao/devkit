# devkit

`devkit` is a multi-tool Go repository. The current binaries are:

- `keeprun`: run and supervise long-lived local commands in the background
- `authrun`: launch CLIs with stored connection profiles and encrypted secrets

## Requirements

- Go 1.26+
- macOS or Linux

## Installation

Install the binaries with Go:

```bash
go install github.com/jingyugao/devkit/cmd/keeprun@latest
go install github.com/jingyugao/devkit/cmd/authrun@latest
```

Or build them from source:

```bash
git clone https://github.com/jingyugao/devkit.git
cd devkit
go build -o keeprun ./cmd/keeprun
go build -o authrun ./cmd/authrun
```

## keeprun

`keeprun` turns a non-interactive command into a managed background task.

### Features

- Run a command in the background and track it as a named task
- Optional wall-clock lifetime such as `3d`, `12h`, or `1h30m`
- Automatic restart after process exit
- Automatic task rehydration when the daemon starts again
- List tasks, stop/start them, remove them, and inspect logs
- Short task IDs for easier day-to-day task management
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

### Quick Start

Run a command as a managed background task:

```bash
keeprun python httpserver.py
```

Run with a name and a 3-day lifetime:

```bash
keeprun --name httpserver --life 3d python httpserver.py
```

List and inspect tasks:

```bash
keeprun ls
keeprun ps
keeprun logs httpserver
keeprun logs -f httpserver
```

Stop, start, and remove a task:

```bash
keeprun stop httpserver
keeprun start httpserver
keeprun start --all
keeprun rm httpserver
```

### Command Usage

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

Task references accept a task name, full task ID, or a unique short ID prefix.

### Configuration

Global config is stored at:

```text
~/.config/keeprun/config.toml
```

Supported config keys:

- `defaults.life`
- `defaults.stop_timeout`
- `defaults.env_pass`
- `logs.tail_lines`

Built-in defaults:

```toml
[defaults]
life = ""
stop_timeout = "10s"
env_pass = []

[logs]
tail_lines = 200
```

Examples:

```bash
keeprun config set defaults.life 3d
keeprun config set defaults.env_pass VIRTUAL_ENV,PYENV_VERSION
keeprun config set logs.tail_lines 500
keeprun config list
```

### Data Layout

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

### Daemon Behavior

- The first mutating command installs or starts the per-user daemon automatically
- Tasks restart automatically after process exit unless you stop them manually
- Tasks with `desired_state=running` are started automatically when the daemon starts again
- `life` is a wall-clock deadline, not accumulated runtime
- When a task expires, it is stopped and marked `expired`
- Logs are stored as combined stdout/stderr with timestamps
- `keeprun ls` shows the short task ID plus restart counts

You can also manage the daemon explicitly:

```bash
keeprun daemon install
keeprun daemon status
keeprun daemon uninstall
```

Platform integration:

- macOS: `~/Library/LaunchAgents/com.keeprun.daemon.plist`
- Linux: `~/.config/systemd/user/keeprund.service`

## authrun

`authrun` stores connection profiles and secrets, then launches supported CLIs with the right authentication context.

### v1 Scope

- Supported profile types: `mysql`, `mongo`, `redis`, `ssh`, `kube`
- Supported tools: `mysql`, `mongosh`, `redis-cli`, `ssh`, `scp`, `sftp`, `kubectl`, `k9s`
- Profile metadata is stored in `~/.config/authrun/profiles.toml`
- Secrets are stored in `~/.config/authrun/secrets.enc`
- The encrypted secrets file uses a master key stored in the OS keyring
- `authrun k9s` builds a temporary merged kubeconfig from all imported kube profiles
- `authrun kubectl ...` stays explicit when multiple kube profiles exist; use `authrun exec <profile> -- kubectl ...`
- Public SSH and Kubernetes material such as known-hosts entries, client certs, and cluster CA data can live in profile metadata while private keys, passphrases, tokens, and client keys stay encrypted

Linux requires a working Secret Service-compatible keyring. There is no plaintext or passphrase fallback in v1.

### Commands

```bash
authrun add <profile> --type mysql|mongo|redis|ssh|kube [options]
authrun import all [--ssh <profile[:host]>] [--kube <profile[:context]>] [--mysql <profile>] [--login-path <login-path>]
authrun import ssh <profile> [--host <alias>] [--config <path>]
authrun import ssh <user@host> [ssh options]
authrun import kube <profile> [--context <context>] [--kubeconfig <path>]
authrun import mysql <profile> [--login-path <login-path>] [--database <name>]
authrun ls
authrun rm <profile>
authrun exec <profile> -- <tool> [args...]
authrun test <profile> [--tool <tool>]
```

### Common `authrun add` Options

- `--type <mysql|mongo|redis|ssh|kube>`
- `--host <host>`
- `--port <port>`
- `--username <username>`
- `--database <name>`
- `--namespace <name>`
- `--tls`
- `--tls-ca-file <path>`
- `--secret-stdin`
- `--secret-env <ENV_NAME>`

Backend-specific options:

- Mongo: `--auth-database <name>`
- MySQL: `--socket <path>`
- SSH:
  - `--private-key-file <path>` or `--private-key-env <ENV_NAME>` or `--private-key-stdin`
  - `--passphrase-env <ENV_NAME>` or `--passphrase-stdin`
  - `--public-key-file <path>`
  - `--known-hosts-file <path>`
- Kubernetes:
  - `--server <https://api-server>`
  - `--cluster <name>`
  - `--context <name>`
  - `--namespace <name>`
  - `--ca-file <path>`
  - `--insecure-skip-tls-verify`
  - token auth via `--secret-env`, `--secret-stdin`, or interactive prompt
  - client cert auth via `--client-cert-file <path>` plus `--client-key-file <path>` or `--client-key-env <ENV_NAME>` or `--client-key-stdin`

### Examples

Add a MySQL profile and read the password from a terminal prompt:

```bash
authrun add user_db --type mysql --host 127.0.0.1 --port 3306 --username app --database users
```

Add a Redis profile from an environment variable:

```bash
export REDIS_PASSWORD='secret'
authrun add cache --type redis --host 127.0.0.1 --port 6379 --username default --secret-env REDIS_PASSWORD
```

Add an SSH profile from a private key file:

```bash
authrun add shell --type ssh --host ssh.example.com --username ops --private-key-file ~/.ssh/id_ed25519 --known-hosts-file ~/.ssh/known_hosts
```

Import an SSH profile from your local `~/.ssh/config`:

```bash
authrun import ssh shell --host devbox
authrun test shell
```

Import a raw SSH target directly:

```bash
authrun import ssh wsl@aliyun.gaojingyu.site -oPort=23456 -i ~/.ssh/yuebai
authrun import ssh root@aliyun.gaojingyu.site -i ~/.ssh/yuebai
```

Add a Kubernetes profile using a bearer token:

```bash
export KUBE_TOKEN='secret-token'
authrun add dev-cluster --type kube --server https://k8s.example.com:6443 --namespace dev --cluster dev --context dev --secret-env KUBE_TOKEN
```

Import a Kubernetes context from your local kubeconfig:

```bash
authrun import kube dev-cluster --context dev
authrun test dev-cluster
```

Import a MySQL login path from your local `mysql_config_editor` config:

```bash
authrun import mysql doris --login-path doris
authrun test doris
```

Import SSH, Kubernetes, and MySQL in one command:

```bash
authrun import all \
  --ssh shell:devbox \
  --kube dev-cluster:dev \
  --mysql doris \
  --login-path doris
```

List saved profiles:

```bash
authrun ls
```

Run a CLI with the stored profile:

```bash
authrun exec user_db -- mysql -e 'SELECT NOW()'
authrun exec cache -- redis-cli PING
authrun exec docs -- mongosh
authrun exec shell -- ssh uname -a
authrun exec shell -- sftp
authrun exec dev-cluster -- kubectl get pods -A
authrun exec dev-cluster -- k9s
```

Shortcut mode is also available:

```bash
authrun mysql -e 'SELECT NOW()'
authrun k9s
authrun ssh root@aliyun.gaojingyu.site
```

`authrun k9s` merges all imported kube profiles into a temporary kubeconfig. `authrun kubectl ...` still requires an explicit profile if more than one kube profile is configured.
`authrun ssh [ssh args...]` is compatible with normal `ssh` syntax. Raw targets like `authrun ssh root@aliyun.gaojingyu.site` are passed through to native SSH resolution. If you want authrun-managed SSH secrets, use `authrun exec <profile> -- ssh` or `authrun ssh <stored-profile-name>`.

Validate a stored profile:

```bash
authrun test user_db
authrun test cache --tool redis-cli
authrun test shell
authrun test dev-cluster
```
