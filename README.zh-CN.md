# tempo

一个跨平台命令行计时器，带常驻 daemon、精确暂停/继续、规则配置和番茄钟原生支持。

`tempo` 采用单二进制双模式：`tempo daemon` 常驻后台持有计时状态，`tempo <command>` 作为轻量客户端通过 IPC 与 daemon 通信。关闭终端不影响计时。

支持平台：**Linux**（Arch Linux + Plasma 6 验证）与 **Windows**。

[English](README.md)

## 特性

- **CLI 管理**：启动、暂停、继续、查看、停止，全部通过命令行完成
- **精确暂停/继续**：暂停保留剩余时长，继续从剩余时间恢复，不重置
- **脱离终端**：Linux 由 systemd user service 托管，Windows 由 Windows Service 托管，不使用 `nohup`
- **规则配置**：TOML 预置规则（时长 + 结束命令），运行时 `--rule` 引用
- **自定义结束动作**：默认桌面提醒，也可指定任意命令
- **番茄钟原生支持**：专注/短休/长休自动循环，参数可配置；每阶段结束弹出「继续 / 停止 / 暂停」确认框，支持手动推进
- **结构化日志**：标准库 `log/slog`，控制台与文件双写

## 安装

### 从源码构建

需要 Go ≥ 1.27（与 go.mod 一致）。

```bash
git clone <repo-url> tempo
cd tempo
go build -o tempo .
```

将二进制放到 PATH 中：

```bash
# Linux
mkdir -p ~/.local/bin
cp tempo ~/.local/bin/

# Windows：将 tempo.exe 放入 PATH 中任意目录
```

### 启用 daemon

**Linux（systemd user service）**：

```bash
mkdir -p ~/.config/systemd/user
cp contrib/tempo.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now tempo
```

单元将 daemon 绑定到图形会话（`graphical-session.target`），服务随桌面会话启停，异常退出时自动重启。`ExecStart` 默认指向 `~/.local/bin/tempo`，如二进制安装在其他位置请相应修改。无图形会话的环境（纯 tty / SSH）可将 `graphical-session.target` 替换为 `default.target`。

查看服务状态与日志：

```bash
systemctl --user status tempo
journalctl --user -u tempo -f
```

**Windows**：

```powershell
tempo install-service
```

## 快速开始

```bash
# 启动一个 10 分钟的计时
tempo start --name tea -t 10m

# 查看所有任务
tempo list

# 暂停 / 继续 / 停止
tempo pause tea
tempo resume tea
tempo stop tea
```

计时结束时默认发送桌面通知。关闭终端后 daemon 继续计时，到点照常提醒。

## 命令参考

```
tempo daemon                                  前台运行 daemon（调试用）
tempo install-service                         Linux 提示安装 systemd unit；Windows 安装 Service

tempo start --name|-n NAME [-t DUR] [--rule|-r RULE] [--finish|-f CMD]
tempo list
tempo pause NAME
tempo resume NAME
tempo stop NAME

tempo pomo start --name|-n NAME [--work|-w 25m] [--short|-s 5m] [--long|-l 15m]
                 [--cycle|-c 4] [--rule|-r RULE] [--finish-on each|sequence]
tempo pomo status [NAME]                      等待确认时追加 (waiting)
tempo pomo next NAME                          推进等待确认的下一阶段
tempo pomo stop [NAME]
```

### 选项说明

| 选项                            | 说明                                                      |
| ------------------------------- | --------------------------------------------------------- |
| `--name` / `-n`                 | 任务名称，后续管理使用，必填                              |
| `-t`                            | 时长，Go duration 格式，如 `25m`、`1h30m`                 |
| `--rule` / `-r`                 | 引用配置文件中的预置规则                                  |
| `--finish` / `-f`               | 计时结束时执行的自定义命令，覆盖规则中的 `finish_command` |
| `--work` / `-w`、`--short` / `-s`、`--long` / `-l` | 番茄钟各阶段时长                       |
| `--cycle` / `-c`                | 每多少个专注后进入长休，默认 4                            |
| `--finish-on`                   | 番茄钟自定义命令的执行时机：`each`（每阶段）或 `sequence`（整个序列）；默认 `each` |

## 配置

配置文件位置：

- Linux：`~/.config/tempo/rules.toml`（`$XDG_CONFIG_HOME` 优先）
- Windows：`%APPDATA%\tempo\rules.toml`

示例：

```toml
[rules.pomodoro]
duration = "25m"
finish_command = "notify-send -t 30000 '番茄钟结束' '休息一下'"
finish_on = "each"

[rules.tea]
duration = "10m"

[pomodoro]
work = "25m"
short_break = "5m"
long_break = "15m"
cycle = 4
```

- `finish_command` 为空时使用平台默认提醒。
- `finish_on` 控制番茄钟中自定义命令在每阶段（`each`）还是整个序列（`sequence`）结束时执行，默认 `each`。
- `[pomodoro]` 为全局默认参数，可被 `tempo pomo start` 的 CLI 参数覆盖。

### 自定义结束命令示例

```bash
# Linux：弹出必须点击关闭的对话框
tempo start --name work -t 25m --finish "kdialog --msgbox '25 分钟到了'"

# Windows：弹出消息框
tempo start --name work -t 25m --finish "powershell -Command \"Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show('25 分钟到了')\""
```

## 番茄钟

```bash
tempo pomo start --name focus --work 30m --short 5m --long 20m --cycle 3
tempo pomo status
tempo pomo next focus
tempo pomo stop
```

阶段切换需要确认：每个阶段结束时弹出「继续 / 停止 / 暂停」三选对话框 —— Linux 依次尝试 kdialog、zenity，Windows 使用 TaskDialog —— 选择后才进入下一阶段：

- **继续**：立即开始下一阶段，循环 专注 → 短休 → 专注 → … → 长休 → 重新开始
- **停止**：终止整个序列
- **暂停**：序列进入等待状态并保留进度，之后可用 `tempo pomo next focus` 手动推进

无交互工具可用时（无桌面环境）发送通知并自动继续，不中断序列。

`status` 显示当前阶段、已完成专注数和剩余时间；等待确认时追加 `(waiting)`：

```
phase=work, completed=1/4, remaining=0s (waiting)
```

暂停/继续作用于当前阶段任务，周期进度不丢失。

## 日志

- Linux：`~/.local/state/tempo/tempo.log`（`$XDG_STATE_HOME` 优先；systemd 托管时同时由 journald 收集 stdout）
- Windows：`%USERPROFILE%\.local\state\tempo\tempo.log`

记录 daemon 启停、任务状态变更、番茄钟阶段转换、通知发送结果和 IPC 错误。

## 项目结构

```
tempo/
├── main.go                            CLI 入口 + daemon 启动入口
├── main_linux.go / main_windows.go    平台入口：服务模式检测、install-service
├── internal/
│   ├── protocol/                      请求/响应结构
│   ├── config/                        TOML 规则配置
│   ├── logging/                       slog 初始化
│   ├── client/                        IPC 客户端（CLI 侧）
│   ├── daemon/                        任务、管理器、番茄钟、请求分发、阶段确认弹窗
│   ├── transport/                     IPC（Linux Unix socket / Windows Named Pipe）
│   ├── notify/                        桌面提醒（notify-send / Toast / MessageBoxW）
│   └── platform/                      平台托管（systemd / Windows Service）
└── contrib/
    └── tempo.service  Linux systemd unit
```

## 开发

```bash
go build ./...
go test ./...
go vet ./...

# 交叉编译 Windows
GOOS=windows GOARCH=amd64 go build ./...
```

平台特定代码通过 `//go:build linux` 与 `//go:build windows` 隔离。

## 许可

MIT
