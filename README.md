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
- **Native Pomodoro**: work / short break / long break cycling with configurable parameters; each phase end pops a Continue / Stop / Pause dialog, with manual advance supported
- **Structured logging**: standard library `log/slog`, written to both console and file

## Installation

### Build from source

Requires Go ≥ 1.27 (matching go.mod).

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
cp contrib/tempo.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now tempo
```

The unit binds the daemon to the graphical session (`graphical-session.target`), so it starts and stops with your desktop session and restarts automatically on failure. `ExecStart` expects the binary at `~/.local/bin/tempo`; adjust it if you installed the binary elsewhere. On headless machines (tty / SSH only), replace `graphical-session.target` with `default.target`.

Check status and follow the logs:

```bash
systemctl --user status tempo
journalctl --user -u tempo -f
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

tempo start --name|-n NAME [-t DUR] [--rule|-r RULE] [--finish|-f CMD]
tempo list
tempo pause NAME
tempo resume NAME
tempo stop NAME

tempo pomo start --name|-n NAME [--work|-w 25m] [--short|-s 5m] [--long|-l 15m]
                 [--cycle|-c 4] [--rule|-r RULE] [--finish-on each|sequence]
tempo pomo status [NAME]                      appends (waiting) while waiting for confirmation
tempo pomo next NAME                          advance the pending next phase
tempo pomo stop [NAME]
```

### Options

| Option                          | Description                                                                |
| ------------------------------- | -------------------------------------------------------------------------- |
| `--name` / `-n`                 | Task name, used for later management; required                             |
| `-t`                            | Duration in Go duration format, e.g. `25m`, `1h30m`                        |
| `--rule` / `-r`                 | Reference a preset rule from the config file                               |
| `--finish` / `-f`               | Custom command to run on completion; overrides the rule's `finish_command` |
| `--work` / `-w`, `--short` / `-s`, `--long` / `-l` | Pomodoro phase durations                                |
| `--cycle` / `-c`                | Number of work phases before a long break; default 4                       |
| `--finish-on`                   | When the Pomodoro custom command runs: `each` (every phase) or `sequence` (whole sequence); default `each` |

## Configuration

Config file location:

- Linux: `~/.config/tempo/rules.toml` (`$XDG_CONFIG_HOME` takes precedence)
- Windows: `%APPDATA%\tempo\rules.toml`

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
- `[pomodoro]` provides global defaults, overridable by CLI flags on `tempo pomo start`.

### Custom finish command examples

```bash
# Linux: modal dialog that must be clicked to dismiss
tempo start --name work -t 25m --finish "kdialog --msgbox '25 minutes are up'"

# Windows: message box
tempo start --name work -t 25m --finish "powershell -Command \"Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show('25 minutes are up')\""
```

## Pomodoro

```bash
tempo pomo start --name focus --work 30m --short 5m --long 20m --cycle 3
tempo pomo status
tempo pomo stop
```

Phase transitions require confirmation: at the end of each phase a Continue / Stop / Pause dialog pops up — kdialog, then zenity, are tried on Linux, and TaskDialog is used on Windows — and the next phase starts only after you choose:

- **Continue**: start the next phase immediately, cycling work → short break → work → … → long break → repeat
- **Stop**: terminate the whole sequence
- **Pause**: the sequence enters waiting with progress preserved; advance it later with `tempo pomo next focus`

When no interactive tool is available (headless environments), a notification is sent and the sequence auto-continues without interruption.

`status` shows the current phase, completed work count, and remaining time; waiting state appends `(waiting)`:

```
phase=work, completed=1/4, remaining=0s (waiting)
```

Pause/resume applies to the current phase; cycle progress is preserved.

## Logging

- Linux: `~/.local/state/tempo/tempo.log` (`$XDG_STATE_HOME` takes precedence; stdout is also collected by journald when run under systemd)
- Windows: `%USERPROFILE%\.local\state\tempo\tempo.log`

Logged events include daemon start/stop, task state changes, Pomodoro phase transitions, notification results, and IPC errors.

## Project structure

```
tempo/
├── main.go                            CLI entry + daemon entry
├── main_linux.go / main_windows.go    Platform entries: service mode detection, install-service
├── internal/
│   ├── protocol/                      Request/response structures
│   ├── config/                        TOML rule configuration
│   ├── logging/                       slog initialization
│   ├── client/                        IPC client (CLI side)
│   ├── daemon/                        Tasks, manager, Pomodoro, request dispatch, phase confirmation dialog
│   ├── transport/                     IPC (Linux Unix socket / Windows Named Pipe)
│   ├── notify/                        Desktop notifications (notify-send / Toast / MessageBoxW)
│   └── platform/                      Platform hosting (systemd / Windows Service)
└── contrib/
    └── tempo.service  Linux systemd unit
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
