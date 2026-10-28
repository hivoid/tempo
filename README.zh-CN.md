# tempo

一个跨平台命令行计时器，带常驻 daemon、精确暂停/继续、规则配置和番茄钟原生支持。

`tempo` 采用单二进制双模式：`tempo daemon` 常驻后台持有计时状态，`tempo <command>` 作为轻量客户端通过 IPC 与 daemon 通信。关闭终端不影响计时。

支持平台：**Linux**（Arch Linux + Plasma 6 验证）与 **Windows**。

[English](README.md)

## 特性

- **CLI 管理**：启动、暂停、继续、查看、停止，全部通过命令行完成
- **精确暂停/继续**：暂停保留剩余时长，继续从剩余时间恢复，不重置
- **脱离终端**：Linux 由 systemd user service 托管，Windows 由 Windows Service 托管，不使用 `nohup`
- **规则配置**：TOML 预置规则（计时与番茄钟两类），运行时 `--rule` 引用
- **自定义结束提醒与结束事件**：计时结束默认弹出提醒，可指定任意命令替代；还可为计时到点、番茄钟每阶段与整个序列终止配置结束事件
- **番茄钟原生支持**：专注/短休/长休自动循环，参数可配置；每阶段结束弹出「继续 / 停止 / 暂停」确认框，支持手动推进
- **结构化日志**：标准库 `log/slog`，控制台与文件双写，路径可配置

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
net start tempo
```

以服务模式运行时进程位于 Session 0，其创建的任何窗口对用户不可见：确认弹窗改经 `WTSSendMessage` 显示在活动控制台会话，通知由服务在用户会话中拉起的辅助进程投递。

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

计时结束时默认弹出结束提醒（有交互组件时为需点击的提醒框，否则回退桌面通知）。关闭终端后 daemon 继续计时，到点照常提醒。

## 命令参考

```
tempo daemon [--config PATH]                  前台运行 daemon（调试用）
tempo version                                 打印版本号
tempo install-service                         Linux 提示安装 systemd unit；Windows 安装 Service
tempo uninstall-service                       Linux 提示手动卸载步骤；Windows 卸载 Service

tempo start --name|-n NAME [-t DUR] [--rule|-r RULE] [--finish-notify|-f CMD] [--finish|-F CMD]
tempo list
tempo pause NAME
tempo resume NAME
tempo stop NAME

tempo pomo start --name|-n NAME [--work|-w 25m] [--short|-s 5m] [--long|-l 15m]
                 [--cycle|-c 4] [--rule|-r RULE] [--finish-each|-e CMD]
                 [--finish-sequence|-S CMD]
tempo pomo status [NAME]                      等待确认时追加 (waiting)
tempo pomo next NAME                          推进等待确认的下一阶段
tempo pomo stop [NAME]
```

### 选项说明

| 选项                                               | 说明                                                                               |
| -------------------------------------------------- | ---------------------------------------------------------------------------------- |
| `--name` / `-n`                                    | 任务名称，后续管理使用，必填                                                       |
| `-t`                                               | 时长，Go duration 格式，如 `25m`、`1h30m`                                          |
| `--rule` / `-r`                                    | 引用配置文件中的预置规则                                                           |
| `--finish-notify` / `-f`                           | 计时结束的自定义提醒命令，覆盖规则中的 `finish_notify`；留空使用平台默认提醒       |
| `--finish` / `-F`                                  | 计时结束时执行一次的结束事件命令；留空（默认）不执行                              |
| `--work` / `-w`、`--short` / `-s`、`--long` / `-l` | 番茄钟各阶段时长                                                                   |
| `--cycle` / `-c`                                   | 每多少个专注后进入长休，默认 4                                                     |
| `--finish-each` / `-e`                             | 每个番茄钟阶段结束时执行的结束事件命令；留空（默认）不执行                        |
| `--finish-sequence` / `-S`                         | 整个番茄钟序列终止时执行一次的结束事件命令；留空（默认）不执行                    |

## 配置

配置文件位置（`tempo.toml`，旧版名为 `rules.toml`，升级时请重命名）：

- Linux：`~/.config/tempo/tempo.toml`（`$XDG_CONFIG_HOME` 优先）
- Windows：`%APPDATA%\tempo\tempo.toml`

配置文件仅由 daemon 读取，CLI 一律经 IPC 交互。Windows 服务模式有一处特殊情况：进程由 SCM 以 LocalSystem 账户启动（不带任何命令行参数），其 `%APPDATA%` 指向服务账户自己的 profile 而非当前用户，因此服务实际读取：

`C:\Windows\System32\config\systemprofile\AppData\Roaming\tempo\tempo.toml`

服务模式下请把 `tempo.toml` 放到该目录（目录不存在时先创建）；当前用户的 `%APPDATA%\tempo\tempo.toml` 仅在前台调试运行 `tempo daemon` 时生效。

示例：

```toml
# 顶层键必须写在任何 [table] 段之前（TOML 语法约束）。
log_path = "/var/log/tempo/tempo.log" # 留空或省略时使用平台默认路径

[pomodoro] # 番茄钟全局默认参数
work = "25m"
short_break = "5m"
long_break = "15m"
cycle = 4

[rules.tea] # 计时规则：含 duration，供 tempo start 使用
duration = "10m"
# finish_notify = "..." # 自定义计时结束提醒命令；留空使用平台默认提醒
# finish = "..."        # 计时结束事件命令，计时到点执行一次；留空或 false 不执行

[rules.deep] # 番茄钟规则：含 work/short_break/long_break/cycle 之一，供 tempo pomo start 使用
work = "50m"
short_break = "10m"
long_break = "30m"
cycle = 2
finish_each = "paplay /usr/share/sounds/freedesktop/stereo/complete.oga" # 每个阶段结束时执行；留空或 false 不执行
finish_sequence = "backup.sh" # 整个序列终止时执行一次；留空或 false 不执行
```

- 计时规则用 `finish_notify` 自定义结束提醒（留空使用平台默认提醒），用 `finish` 追加结束事件——计时到点执行一次的命令。
- 番茄钟规则有两个可独立配置的结束事件（不同于提醒、没有默认动作）：`finish_each` 在每个阶段结束时执行，`finish_sequence` 在整个序列终止时执行一次；留空或 `false` 均不执行。
- 旧选项 `finish_on` 与 `finish_command` 键已移除，配置中仍出现（或结束事件键用错规则类型）时会报错并提示迁移。
- CLI 参数显式传入时覆盖规则配置：`tempo start` 用 `--finish-notify/-f` 与 `--finish/-F`，`tempo pomo start` 用 `--finish-each/-e` 与 `--finish-sequence/-S`。
- 规则分两类：计时规则（含 `duration`，由 `tempo start --rule` 引用）与番茄钟规则（含 `work`/`short_break`/`long_break`/`cycle` 之一，由 `tempo pomo start --rule` 引用）；两类字段互斥、共用同一命名空间，引用未定义的规则或类型不匹配时返回错误。
- 番茄钟规则未填写的字段回退到 `[pomodoro]` 全局默认，再回退到内置默认值；CLI 显式参数优先级最高。
- `log_path` 指定 daemon 日志路径，留空或省略时使用平台默认路径（见[日志](#日志)）。

daemon 启动时一次性加载配置，修改配置文件后需重启服务才能生效：

```bash
# Linux
systemctl --user restart tempo

# Windows
net stop tempo
net start tempo
```

### 自定义结束提醒与结束事件示例

`--finish-notify` 替代默认结束提醒：设置了它就不再弹默认提醒。

```bash
# Linux：弹出必须点击关闭的对话框（替代默认提醒）
tempo start --name work -t 25m --finish-notify "kdialog --msgbox '25 分钟到了'"

# Windows：弹出消息框（替代默认提醒）
tempo start --name work -t 25m --finish-notify "powershell -Command \"Add-Type -AssemblyName PresentationFramework; [System.Windows.MessageBox]::Show('25 分钟到了')\""
```

`--finish` 是结束事件：到点在默认提醒之外额外执行一次（未设置提醒时默认提醒照常出现）。

```bash
# Linux：计时到点弹默认提醒，同时执行自定义命令
tempo start --name work -t 25m --finish "backup.sh"
```

番茄钟没有结束提醒定制（阶段切换由确认弹窗提示），只有结束事件：`--finish-each` 每个阶段结束时执行，`--finish-sequence` 整个序列终止时执行一次；留空（默认）不执行。注意每个阶段结束本就会弹确认框，`--finish-each` 适合提示音、写日志等附加动作，不要用它再发一遍通知。

```bash
# 每个阶段结束播放提示音，整个序列终止时执行备份
tempo pomo start --name focus --work 30m --short 5m --finish-each "paplay /usr/share/sounds/freedesktop/stereo/complete.oga" --finish-sequence "backup.sh"
```

## 番茄钟

```bash
tempo pomo start --name focus --work 30m --short 5m --long 20m --cycle 3

# 或引用番茄钟预置规则（见配置一节）
tempo pomo start --name writing -r deep
tempo pomo status
tempo pomo next focus
tempo pomo stop
```

阶段切换需要确认：每个阶段结束时弹出「继续 / 停止 / 暂停」三选对话框 —— Linux 依次尝试 kdialog、zenity，Windows 使用 TaskDialog —— 选择后才进入下一阶段：

- **继续**：立即开始下一阶段，循环 专注 → 短休 → 专注 → … → 长休 → 重新开始
- **停止**：终止整个序列
- **暂停**：序列进入等待状态并保留进度，之后可用 `tempo pomo next focus` 手动推进

无交互工具可用时（无桌面环境）发送通知并自动继续，不中断序列。

确认弹窗的等待时限为 5 分钟：超时未响应时关闭弹窗，序列静默进入等待状态（与选择「暂停」一致，不发送通知、不自动继续），之后用 `tempo pomo next` 推进或 `tempo pomo stop` 终止。Linux 上仅当组件无法运行（如无法连接显示服务）时才回退到下一个组件，未响应不触发回退；Windows 服务模式下消息框到时自动关闭。

`status` 显示当前阶段、已完成专注数和剩余时间；等待确认时追加 `(waiting)`。省略可选的 `NAME` 时作用于唯一活动序列；无活动番茄钟时输出 `no active pomodoro`：

```
name=focus, phase=work, completed=1/4, remaining=0s (waiting)
```

暂停/继续作用于当前阶段任务，周期进度不丢失。

## 日志

默认日志路径：

- Linux：`~/.local/state/tempo/tempo.log`（`$XDG_STATE_HOME` 优先；systemd 托管时同时由 journald 收集 stdout）
- Windows：`%USERPROFILE%\.local\state\tempo\tempo.log`

在 `tempo.toml` 中设置 `log_path` 可写入其他位置。配置的路径不可用（如无写入权限）时，daemon 直接报错退出，不会静默回退到默认路径。

Windows 服务模式下默认路径同样受账户影响：LocalSystem 的 `%USERPROFILE%` 解析为 `C:\Windows\System32\config\systemprofile`，未配置 `log_path` 时默认日志将落到 `C:\Windows\System32\config\systemprofile\.local\state\tempo\tempo.log`；如需自定义，请在服务账户的配置文件中设置 `log_path`（见[配置](#配置)）。

记录 daemon 启停、任务状态变更、番茄钟阶段转换、通知发送结果和 IPC 错误。

## 项目结构

```
tempo/
├── main.go                            CLI 入口 + daemon 启动入口
├── main_linux.go / main_windows.go    平台入口：服务模式检测、install-service
├── internal/
│   ├── protocol/                      请求/响应结构
│   ├── config/                        TOML 配置
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

# 构建带版本号的二进制（由 `tempo version` 报告）
go build -ldflags "-X main.version=v1.0.0" -o tempo .

# 交叉编译 Windows
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.version=v1.0.0" -o tempo.exe .
```

平台特定代码通过 `//go:build linux` 与 `//go:build windows` 隔离。

## 许可

MIT
