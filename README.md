# tempo

A cross-platform command-line timer with a resident daemon, precise pause/resume, rule-based configuration, and native Pomodoro support.

`tempo` uses a single binary with two modes: `tempo daemon` runs in the background and owns the timer state, while `tempo <command>` acts as a lightweight client that talks to the daemon over IPC. Closing the terminal does not affect timing.

Supported platforms: **Linux** (verified on Arch Linux + Plasma 6) and **Windows**.

[中文文档](README.zh-CN.md)

## Features

- **CLI management**: start, pause, resume, list, and stop — all from the command line
- **Precise pause/resume**: pausing preserves the remaining time; resuming continues from it without resetting
- **Detached from the terminal**: managed by a systemd user service on Linux and a Windows Service on Windows — no `nohup`
- **Rule configuration**: preset timer and Pomodoro rules in TOML, referenced at runtime with `--rule`
- **Custom finish notification and events**: timer completion pops a reminder by default, replaceable with any command; finish events additionally run commands on timer completion, at every Pomodoro phase end, and on sequence stop
- **Native Pomodoro**: work / short break / long break cycling with configurable parameters; each phase end pops a Continue / Stop / Pause dialog, with manual advance supported
- **Structured logging**: standard library `log/slog`, written to both console and file with configurable path

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
net start tempo
```

When run as a service, the process lives in Session 0 where no window it creates is visible. Confirmation dialogs are therefore displayed in the active console session via `WTSSendMessage`, and notifications are delivered by a helper process the service launches in the user session.

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

When the timer finishes, a finish reminder pops up by default: a click-to-dismiss dialog when an interactive tool is available, falling back to a plain desktop notification otherwise. After closing the terminal, the daemon keeps timing and still reminds on completion.

## Command reference

```
tempo daemon [--config PATH]                  Run the daemon in the foreground (debugging)
tempo version                                 Print the version
tempo install-service                         Linux: print systemd unit install instructions; Windows: install the Service
tempo uninstall-service                       Linux: print systemd unit uninstall instructions; Windows: remove the Service

tempo start --name|-n NAME [-t DUR] [--rule|-r RULE] [--finish-notify|-f CMD] [--finish|-F CMD]
tempo list
tempo pause NAME
tempo resume NAME
tempo stop NAME

tempo pomo start --name|-n NAME [--work|-w 25m] [--short|-s 5m] [--long|-l 15m]
                 [--cycle|-c 4] [--rule|-r RULE] [--finish-each|-e CMD]
                 [--finish-sequence|-S CMD]
tempo pomo status [NAME]                      appends (waiting) while waiting for confirmation
tempo pomo next NAME                          advance the pending next phase
tempo pomo stop [NAME]
```

### Options

| Option                                             | Description                                                                                                |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `--name` / `-n`                                    | Task name, used for later management; required                                                             |
| `-t`                                               | Duration in Go duration format, e.g. `25m`, `1h30m`                                                        |
| `--rule` / `-r`                                    | Reference a preset rule from the config file                                                               |
| `--finish-notify` / `-f`                           | Custom notification command on completion; overrides the rule's `finish_notify`; empty uses the platform default |
| `--finish` / `-F`                                  | Finish-event command run once when the timer completes; empty (default) runs nothing                       |
| `--work` / `-w`, `--short` / `-s`, `--long` / `-l` | Pomodoro phase durations                                                                                   |
| `--cycle` / `-c`                                   | Number of work phases before a long break; default 4                                                       |
| `--finish-each` / `-e`                             | Finish-event command run at the end of every Pomodoro phase; empty (default) runs nothing                  |
| `--finish-sequence` / `-S`                         | Finish-event command run once when the Pomodoro sequence is stopped; empty (default) runs nothing          |

## Configuration

Config file location (`tempo.toml`, previously named `rules.toml` — rename it when upgrading):

- Linux: `~/.config/tempo/tempo.toml` (`$XDG_CONFIG_HOME` takes precedence)
- Windows: `%APPDATA%\tempo\tempo.toml`

The config file is only read by the daemon; the CLI talks over IPC only. One Windows special case: in service mode the process is started by the SCM under the LocalSystem account (with no command-line arguments), whose `%APPDATA%` points at the service account's own profile instead of yours — the service therefore reads:

`C:\Windows\System32\config\systemprofile\AppData\Roaming\tempo\tempo.toml`

Put `tempo.toml` there for service mode (create the directory if it does not exist); `%APPDATA%\tempo\tempo.toml` under your own account only applies when running `tempo daemon` in the foreground for debugging.

Example:

```toml
# Top-level keys must precede any [table] section (TOML syntax).
log_path = "/var/log/tempo/tempo.log" # empty or omitted: platform default path

[pomodoro] # global Pomodoro defaults
work = "25m"
short_break = "5m"
long_break = "15m"
cycle = 4

[rules.tea] # timer rule: has duration, for `tempo start`
duration = "10m"
# finish_notify = "..." # custom finish notification command; empty uses the platform default
# finish = "..."        # finish-event command run once when the timer completes; empty or false runs nothing

[rules.deep] # Pomodoro rule: has work/short_break/long_break/cycle, for `tempo pomo start`
work = "50m"
short_break = "10m"
long_break = "30m"
cycle = 2
finish_each = "paplay /usr/share/sounds/freedesktop/stereo/complete.oga" # finish event at every phase end; empty or false runs nothing
finish_sequence = "backup.sh" # finish event run once when the sequence is stopped; empty or false runs nothing
```

- Timer rules customize the finish reminder with `finish_notify` (empty: platform default reminder) and may add a finish event with `finish` — a command run once when the timer completes.
- Pomodoro rules instead have two independently configurable finish events, distinct from reminders and without a default action: `finish_each` runs at the end of every phase and `finish_sequence` runs once when the sequence is stopped; an empty value or `false` runs nothing.
- The old `finish_on` option and `finish_command` key have been removed; a config still containing them (or finish-event keys in the wrong rule kind) is rejected with a migration hint.
- CLI flags override rule presets: `--finish-notify/-f` and `--finish/-F` for `tempo start`, `--finish-each/-e` and `--finish-sequence/-S` for `tempo pomo start`.
- Rules are either timer rules (with `duration`, referenced by `tempo start --rule`) or Pomodoro rules (with any of `work`/`short_break`/`long_break`/`cycle`, referenced by `tempo pomo start --rule`); the two kinds are mutually exclusive and share a single namespace. Referencing an undefined rule or a rule of the wrong kind returns an error.
- Unset fields of a Pomodoro rule fall back to the `[pomodoro]` section, then to built-in defaults; explicit CLI flags take precedence over both.
- `log_path` sets the daemon log file path; empty or omitted uses the platform default (see [Logging](#logging)).

The daemon loads the config once at startup. After editing the config file, restart the service for changes to take effect:

```bash
# Linux
systemctl --user restart tempo

# Windows
net stop tempo
net start tempo
```

### Custom finish notification and finish-event examples

`--finish-notify` replaces the default finish reminder: when set, the default reminder no longer pops up.

```bash
# Linux: modal dialog that must be clicked to dismiss (replaces the default reminder)
tempo start --name work -t 25m --finish-notify "kdialog --msgbox '25 minutes are up'"

# Windows: message box (replaces the default reminder)
tempo start --name work -t 25m --finish-notify "powershell -Command \"Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show('25 minutes are up')\""
```

`--finish` is the finish event: it runs once when the timer completes, in addition to the default reminder (with no reminder command, the default reminder still appears).

```bash
# Linux: the default reminder pops up, and the custom command runs alongside
tempo start --name work -t 25m --finish "backup.sh"
```

The Pomodoro has no finish-reminder customization (phase switches are prompted by the confirmation dialog) — only finish events: `--finish-each` runs at every phase end and `--finish-sequence` runs once when the sequence is stopped; empty (default) runs nothing. Note that every phase end already pops the confirmation dialog, so `--finish-each` suits extra actions like playing a sound or writing a log — not for sending yet another notification.

```bash
# Play a sound at every phase end; run a backup when the sequence is stopped
tempo pomo start --name focus --work 30m --short 5m --finish-each "paplay /usr/share/sounds/freedesktop/stereo/complete.oga" --finish-sequence "backup.sh"
```

## Pomodoro

```bash
tempo pomo start --name focus --work 30m --short 5m --long 20m --cycle 3

# Or reference a Pomodoro rule preset (see Configuration)
tempo pomo start --name writing -r deep
tempo pomo status
tempo pomo stop
```

Phase transitions require confirmation: at the end of each phase a Continue / Stop / Pause dialog pops up — kdialog, then zenity, are tried on Linux, and TaskDialog is used on Windows — and the next phase starts only after you choose:

- **Continue**: start the next phase immediately, cycling work → short break → work → … → long break → repeat
- **Stop**: terminate the whole sequence
- **Pause**: the sequence enters waiting with progress preserved; advance it later with `tempo pomo next focus`

When no interactive tool is available (headless environments), a notification is sent and the sequence auto-continues without interruption.

Dialog waits are capped at 5 minutes: when the deadline passes with no response, the dialog is closed and the sequence silently enters waiting — no notification is sent and nothing auto-continues; advance it later with `tempo pomo next` or terminate it with `tempo pomo stop`. On Linux the fallback chain only moves to the next tool when the current one fails to run (e.g. no display service), not when you simply do not respond; in Windows service mode the message box auto-closes at the deadline.

`status` shows the current phase, completed work count, and remaining time; waiting state appends `(waiting)`. Omit the optional `NAME` to target the only active sequence; when nothing is running it prints `no active pomodoro`:

```
name=focus, phase=work, completed=1/4, remaining=0s (waiting)
```

Pause/resume applies to the current phase; cycle progress is preserved.

## Logging

The default log path is:

- Linux: `~/.local/state/tempo/tempo.log` (`$XDG_STATE_HOME` takes precedence; stdout is also collected by journald when run under systemd)
- Windows: `%USERPROFILE%\.local\state\tempo\tempo.log`

Set `log_path` in `tempo.toml` to write to a different location. When the configured path is unavailable (e.g. permission denied), the daemon exits with an error instead of silently falling back.

In Windows service mode the default path is subject to the same account rule: LocalSystem's `%USERPROFILE%` resolves to `C:\Windows\System32\config\systemprofile`, so without `log_path` the default log lands in `C:\Windows\System32\config\systemprofile\.local\state\tempo\tempo.log`. Set `log_path` in the service account's config (see [Configuration](#configuration)) to write elsewhere.

Logged events include daemon start/stop, task state changes, Pomodoro phase transitions, notification results, and IPC errors.

## Project structure

```
tempo/
├── main.go                            CLI entry + daemon entry
├── main_linux.go / main_windows.go    Platform entries: service mode detection, install-service
├── internal/
│   ├── protocol/                      Request/response structures
│   ├── config/                        TOML configuration
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

# Build with a version string (reported by `tempo version`)
go build -ldflags "-X main.version=v1.0.0" -o tempo .

# Cross-compile for Windows
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=v1.0.0" -o tempo.exe .
```

Platform-specific code is isolated with `//go:build linux` and `//go:build windows`.

## License

MIT
