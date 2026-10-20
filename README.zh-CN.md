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
- **番茄钟原生支持**：专注/短休/长休自动循环，参数可配置
- **结构化日志**：标准库 `log/slog`，控制台与文件双写

## 安装

### 从源码构建

需要 Go ≥ 1.21。

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
cp contrib/tempo-daemon.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now tempo-daemon
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

tempo start --name NAME [-t DUR] [--rule RULE] [--finish CMD]
tempo list
tempo pause NAME
tempo resume NAME
tempo stop NAME

tempo pomo start --name NAME [--work 25m] [--short 5m] [--long 15m] [--cycle 4] [--rule RULE]
tempo pomo status
tempo pomo stop
```

### 选项说明

| 选项                            | 说明                                                      |
| ------------------------------- | --------------------------------------------------------- |
| `--name`                        | 任务名称，后续管理使用，必填                              |
| `-t`                            | 时长，Go duration 格式，如 `25m`、`1h30m`                 |
| `--rule`                        | 引用配置文件中的预置规则                                  |
| `--finish`                      | 计时结束时执行的自定义命令，覆盖规则中的 `finish_command` |
| `--work` / `--short` / `--long` | 番茄钟各阶段时长                                          |
| `--cycle`                       | 每多少个专注后进入长休，默认 4                            |

## 配置

配置文件位置：

- Linux：`~/.config/timer/rules.toml`
- Windows：`%APPDATA%\timer\rules.toml`

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
- `[pomo]` 为全局默认参数，可被 `tempo pomo start` 的 CLI 参数覆盖。

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
tempo pomo stop
```

阶段自动切换：专注 → 短休 → 专注 → … → 长休 → 循环。`status` 输出当前阶段、已完成专注数和剩余时间。

暂停/继续作用于当前阶段任务，周期进度不丢失。

## 日志

- Linux：`~/.local/state/timer/timer.log`（systemd 托管时同时由 journald 收集 stdout）
- Windows：`%LOCALAPPDATA%\timer\timer.log`

记录 daemon 启停、任务状态变更、番茄钟阶段转换、通知发送结果和 IPC 错误。

## 项目结构

```
tempo/
├── main.go                  CLI 入口 + daemon 启动入口
├── internal/
│   ├── protocol/            请求/响应结构
│   ├── config/              TOML 规则配置
│   ├── logging/             slog 初始化
│   ├── daemon/              任务、管理器、番茄钟、请求分发
│   ├── transport/           IPC（Linux Unix socket / Windows Named Pipe）
│   ├── notify/              桌面提醒（notify-send / Toast / MessageBoxW）
│   └── platform/            平台托管（systemd / Windows Service）
└── contrib/
    └── tempo-daemon.service  Linux systemd unit
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
