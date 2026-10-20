# tempo

A cross-platform command-line timer with a resident daemon, precise pause/resume, rule-based configuration, and native Pomodoro support.

`tempo` uses a single binary with two modes: `tempo daemon` runs in the background and owns the timer state, while `tempo <command>` acts as a lightweight client that talks to the daemon over IPC. Closing the terminal does not affect timing.

Supported platforms: **Linux** (verified on Arch Linux + Plasma 6) and **Windows**.

[中文文档](README.zh-CN.md)

## Features

- **CLI management**: start, pause, resume, list, and stop — all from the command line
- **Precise pause/resume**: pausing preserves the remaining time; resuming continues from it without resetting
- **Detached from the terminal**: managed by a systemd user service on Linux and a Windows Service on Windows — no `nohup`
- **Rule configuration**: preset rules (duration + finish command) in TOML, referenced at runtime with `--rule`
- **Custom finish actions**: desktop notification by default, or any command you specify
- **Native Pomodoro**: automatic work / short break / long break cycling with configurable parameters
- **Structured logging**: standard library `log/slog`, written to both console and file

## Installation

### Build from source

Requires Go ≥ 1.21.

```bash
git clone <repo-url> tempo
cd tempo
go build -o tempo .
```

Put the binary on your PATH:

```bash
# Linux
mkdir -p ~/.local/bin
cp tempo ~/.local/bin/

# Windows: place tempo.exe in any directory on your PATH
```

### Enable the daemon

**Linux (systemd user service)**:

```bash
mkdir -p ~/.config/systemd/user
cp contrib/tempo-daemon.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now tempo-daemon
```

**Windows**:

```powershell
tempo install-service
```

## Quick start

```bash
# Start a 10-minute timer
tempo start --name tea -t 10m

# List all tasks
tempo list

# Pause / resume / stop
tempo pause tea
tempo resume tea
tempo stop tea
```

When the timer finishes, a desktop notification is sent by default. After closing the terminal, the daemon keeps timing and still notifies on completion.

## Command reference

```
tempo daemon                                  Run the daemon in the foreground (debugging)
tempo install-service                         Linux: print systemd unit install instructions; Windows: install the Service

tempo start --name NAME [-t DUR] [--rule RULE] [--finish CMD]
tempo list
tempo pause NAME
tempo resume NAME
tempo stop NAME

tempo pomodoro start --name NAME [--work 25m] [--short 5m] [--long 15m] [--cycle 4] [--rule RULE]
tempo pomodoro status
tempo pomodoro stop
```

### Options

| Option                          | Description                                                                |
| ------------------------------- | -------------------------------------------------------------------------- |
| `--name`                        | Task name, used for later management; required                             |
| `-t`                            | Duration in Go duration format, e.g. `25m`, `1h30m`                        |
| `--rule`                        | Reference a preset rule from the config file                               |
| `--finish`                      | Custom command to run on completion; overrides the rule's `finish_command` |
| `--work` / `--short` / `--long` | Pomodoro phase durations                                                   |
| `--cycle`                       | Number of work phases before a long break; default 4                       |

## Configuration

Config file location:

- Linux: `~/.config/timer/rules.toml`
- Windows: `%APPDATA%\timer\rules.toml`

Example:

```toml
[rules.pomodoro]
duration = "25m"
finish_command = "notify-send -t 30000 'Pomodoro finished' 'Take a break'"
finish_on = "each"

[rules.tea]
duration = "10m"

[pomodoro]
work = "25m"
short_break = "5m"
long_break = "15m"
cycle = 4
```

- When `finish_command` is empty, the platform default notification is used.
- `finish_on` controls whether the custom command runs on every phase (`each`) or only when the whole sequence ends (`sequence`) in Pomodoro mode; default `each`.
- `[pomodoro]` provides global defaults, overridable by CLI flags on `tempo pomodoro start`.

### Custom finish command examples

```bash
# Linux: modal dialog that must be clicked to dismiss
tempo start --name work -t 25m --finish "kdialog --msgbox '25 minutes are up'"

# Windows: message box
tempo start --name work -t 25m --finish "powershell -Command \"Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show('25 minutes are up')\""
```

## Pomodoro

```bash
tempo pomodoro start --name focus --work 30m --short 5m --long 20m --cycle 3
tempo pomodoro status
tempo pomodoro stop
```

Phases switch automatically: work → short break → work → … → long break → repeat. `status` shows the current phase, completed work count, and remaining time.

Pause/resume applies to the current phase; cycle progress is preserved.

## Logging

- Linux: `~/.local/state/timer/timer.log` (stdout is also collected by journald when run under systemd)
- Windows: `%LOCALAPPDATA%\timer\timer.log`

Logged events include daemon start/stop, task state changes, Pomodoro phase transitions, notification results, and IPC errors.

## Project structure

```
tempo/
├── main.go                  CLI entry + daemon entry
├── internal/
│   ├── protocol/            Request/response structures
│   ├── config/              TOML rule configuration
│   ├── logging/             slog initialization
│   ├── daemon/              Tasks, manager, Pomodoro, request dispatch
│   ├── transport/           IPC (Linux Unix socket / Windows Named Pipe)
│   ├── notify/              Desktop notifications (notify-send / Toast / MessageBoxW)
│   └── platform/            Platform hosting (systemd / Windows Service)
└── contrib/
    └── tempo-daemon.service  Linux systemd unit
```

## Development

```bash
go build ./...
go test ./...
go vet ./...

# Cross-compile for Windows
GOOS=windows GOARCH=amd64 go build ./...
```

Platform-specific code is isolated with `//go:build linux` and `//go:build windows`.

## License

MIT

### 需要同步的改动

中文 `README.md` 顶部也需加一行英文链接，保持双向可跳转：

```markdown
# tempo

...（原有内容）

[English](README.md)
```
