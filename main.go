// Command tempo 是跨平台 CLI 计时器，单二进制双模式：daemon 常驻计时，CLI 作为轻量客户端。
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"
	"time"

	"tempo/internal/client"
	"tempo/internal/config"
	"tempo/internal/daemon"
	"tempo/internal/logging"
	"tempo/internal/notify"
	"tempo/internal/platform"
	"tempo/internal/protocol"
)

// version 由构建时通过 -ldflags "-X main.version=..." 注入，未注入时显示 dev。
var version = "dev"

const usage = `tempo — CLI 计时器

用法：
  tempo daemon                                  前台运行 daemon（调试）
  tempo version                                 打印版本号
  tempo install-service                         安装系统服务（Windows 实际安装；Linux 打印手动安装提示）
  tempo uninstall-service                       卸载系统服务（Windows 实际卸载；Linux 打印手动卸载提示）
  tempo start --name|-n NAME [-t DUR] [--rule|-r RULE] [--finish|-f CMD]
  tempo list                                    列出所有活动任务
  tempo pause NAME
  tempo resume NAME
  tempo stop NAME
  tempo pomo start --name|-n NAME [--work|-w 25m] [--short|-s 5m] [--long|-l 15m]
                    [--cycle|-c 4] [--rule|-r RULE] [--finish-on each|sequence]
  tempo pomo status [NAME]                      查看当前阶段与周期进度
  tempo pomo next NAME                          推进等待确认的下一阶段
  tempo pomo stop [NAME]                        终止番茄钟序列
`

// serviceName 是系统服务的注册名，安装与运行必须使用同一名称。
const serviceName = "tempo"

// runAsServiceIfNeeded 处理单二进制双模式（CONTEXT.md §5.7）：Windows 上进程由
// SCM 启动时以服务身份运行 daemon 并阻塞至服务停止，返回 (true, err)；其余情况
// 返回 (false, nil)，main 继续 CLI 流程。平台差异全部收在 internal/platform 内，
// 因此本函数与 main.go 其余代码一样不含 build tag，可单独通过静态检查。
func runAsServiceIfNeeded() (bool, error) {
	inService, err := platform.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("检测服务运行模式: %w", err)
	}
	if !inService {
		return false, nil
	}
	return true, platform.RunAsService(serviceName, func(stop <-chan struct{}) error {
		return runDaemon(defaultConfigPath(), stop)
	})
}

// installService 安装系统服务：Windows 上把当前二进制注册为 Windows Service
// （需要管理员权限）；Linux 上打印 systemd unit 手动安装提示。具体行为由
// internal/platform 按平台实现决定。
func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件: %w", err)
	}
	return platform.InstallService(serviceName, "tempo daemon", exePath)
}

// uninstallService 卸载系统服务：Windows 上删除已注册的 Windows Service
// （需要管理员权限，服务运行中会先请求停止并等待至多 10 秒）；Linux 上打印
// systemd unit 手动卸载步骤。具体行为由 internal/platform 按平台实现决定。
func uninstallService() error {
	return platform.RemoveService(serviceName)
}

func main() {
	// 单二进制双模式（CONTEXT.md §5.7）：Windows 上进程由 SCM 启动时进入服务
	// 运行循环，不按 CLI 处理；Linux 上该检查恒为 false。
	if handled, err := runAsServiceIfNeeded(); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "tempo:", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "daemon":
		err = runDaemonCmd(os.Args[2:])
	case "start":
		err = runStart(os.Args[2:])
	case "list":
		err = runList(os.Args[2:])
	case "pause", "resume", "stop":
		err = runNamed(os.Args[1], os.Args[2:])
	case "pomo", "pomodoro": // pomodoro 为隐藏别名，保持向后兼容，不写入 usage
		err = handlePomodoro(os.Args[2:])
	case "__notify": // 隐藏辅助命令：Windows 服务在用户会话中拉起以显示通知，不写入 usage
		err = runHiddenNotify(os.Args[2:])
	case "install-service":
		err = installService()
	case "uninstall-service":
		err = uninstallService()
	case "version":
		fmt.Printf("tempo %s\n", version)
		return
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "tempo: 未知子命令 %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		if errors.Is(err, client.ErrDaemonNotRunning) {
			fmt.Fprintln(os.Stderr, "daemon not running")
		} else {
			fmt.Fprintln(os.Stderr, "tempo:", err)
		}
		os.Exit(1)
	}
}

// runDaemonCmd 处理 daemon 子命令：解析 --config 后进入 daemon 启动逻辑。
func runDaemonCmd(args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "规则配置 TOML 路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return runDaemon(*configPath, nil)
}

// runDaemon 启动 daemon：初始化日志、加载配置并启动 IPC 服务。
// 由 daemon 子命令（前台调试，stop 为 nil）与 Windows 服务模式共用：
// stop 关闭后 IPC 服务停止接受新连接并优雅返回（Linux 由 systemd 终止进程，
// 不经过该路径）。
func runDaemon(configPath string, stop <-chan struct{}) error {
	logger, err := logging.Init(defaultLogPath())
	if err != nil {
		return err
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger.Info("配置已加载", "action", "load_config")

	mgr := daemon.NewManager(cfg, notify.NewNotifier(), logger)
	return daemon.ServeWithShutdown(mgr, stop)
}

// runStart 启动计时任务。
func runStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	var name, rule, duration, finish string
	fs.StringVar(&name, "name", "", "任务名称")
	fs.StringVar(&name, "n", "", "任务名称（简写）")
	fs.StringVar(&rule, "rule", "", "预置规则名称")
	fs.StringVar(&rule, "r", "", "预置规则名称（简写）")
	fs.StringVar(&duration, "t", "", "计时时长，如 25m")
	fs.StringVar(&finish, "finish", "", "自定义结束命令")
	fs.StringVar(&finish, "f", "", "自定义结束命令（简写）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("start: 必须通过 --name/-n 指定任务名称")
	}

	return callAndReport(protocol.Request{
		Action: protocol.ActionStart,
		Name:   name,
		Args: map[string]string{
			"rule":     rule,
			"duration": duration,
			"finish":   finish,
		},
	})
}

// runList 列出所有活动任务，用 tabwriter 对齐输出。
func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	resp, err := client.Call(protocol.Request{Action: protocol.ActionList})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	// 逐行写失败无法补救，底层写入错误由 Flush 统一返回，显式忽略以满足 errcheck。
	_, _ = fmt.Fprintln(w, "NAME\tSTATE\tREMAINING\tRULE")
	for _, t := range resp.Tasks {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Name, t.State, t.Remaining, t.Rule)
	}
	return w.Flush()
}

// runNamed 处理 pause/resume/stop 这三个只需任务名的子命令。
func runNamed(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("%s: 用法: tempo %s NAME", action, action)
	}

	var act string
	switch action {
	case "pause":
		act = protocol.ActionPause
	case "resume":
		act = protocol.ActionResume
	default:
		act = protocol.ActionStop
	}
	return callAndReport(protocol.Request{Action: act, Name: fs.Arg(0)})
}

// handlePomodoro 分发 pomo 子命令：start/status/next/stop。主命令名为 pomo，
// "pomodoro" 作为隐藏别名由 main 的 switch 一并映射到本函数。
func handlePomodoro(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pomo: 用法: tempo pomo {start|status|next|stop}")
	}
	switch args[0] {
	case "start":
		return runPomodoroStart(args[1:])
	case "status":
		return runPomodoroStatus(args[1:])
	case "next":
		return runPomodoroNext(args[1:])
	case "stop":
		return runPomodoroStop(args[1:])
	default:
		return fmt.Errorf("pomo: 未知子命令 %q，用法: tempo pomo {start|status|next|stop}", args[0])
	}
}

// runPomodoroStart 启动番茄钟序列。时长与 cycle 留空表示未设置，
// 由 daemon 按 [pomodoro] 配置与内置默认值补全（PRD F17）。
func runPomodoroStart(args []string) error {
	fs := flag.NewFlagSet("pomo start", flag.ExitOnError)
	var name, rule, finishOn, work, short, long, cycle string
	fs.StringVar(&name, "name", "", "番茄钟名称")
	fs.StringVar(&name, "n", "", "番茄钟名称（简写）")
	fs.StringVar(&rule, "rule", "", "预置规则名称")
	fs.StringVar(&rule, "r", "", "预置规则名称（简写）")
	fs.StringVar(&finishOn, "finish-on", "", "自定义命令执行时机: each | sequence（缺省 each）")
	fs.StringVar(&work, "work", "", "专注时长，如 25m")
	fs.StringVar(&work, "w", "", "专注时长（简写）")
	fs.StringVar(&short, "short", "", "短休时长，如 5m")
	fs.StringVar(&short, "s", "", "短休时长（简写）")
	fs.StringVar(&long, "long", "", "长休时长，如 15m")
	fs.StringVar(&long, "l", "", "长休时长（简写）")
	fs.StringVar(&cycle, "cycle", "", "一个长休周期包含的专注次数")
	fs.StringVar(&cycle, "c", "", "一个长休周期包含的专注次数（简写）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("pomo start: 必须通过 --name/-n 指定番茄钟名称")
	}

	return callAndReport(protocol.Request{
		Action: protocol.ActionPomodoroStart,
		Name:   name,
		Args: map[string]string{
			"work":      work,
			"short":     short,
			"long":      long,
			"cycle":     cycle,
			"rule":      rule,
			"finish_on": finishOn,
		},
	})
}

// runPomodoroStatus 查询番茄钟状态。可选位置参数 NAME 指定序列；省略时由 daemon
// 解析唯一活动序列，无活动番茄钟时输出 no active pomodoro，命中时输出
// name=NAME, phase=..., completed=N/M, remaining=DUR。
func runPomodoroStatus(args []string) error {
	fs := flag.NewFlagSet("pomo status", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("pomo status: 用法: tempo pomo status [NAME]")
	}

	resp, err := client.Call(protocol.Request{
		Action: protocol.ActionPomodoroStatus,
		Name:   fs.Arg(0),
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	if resp.Pomodoro == nil {
		fmt.Println("no active pomodoro")
		return nil
	}
	p := resp.Pomodoro
	suffix := ""
	if p.Waiting {
		// 阶段结束被暂停，需要 tempo pomo next 手动推进。
		suffix = " (waiting)"
	}
	fmt.Printf("name=%s, phase=%s, completed=%d/%d, remaining=%s%s\n",
		p.Name, p.Phase, p.Completed, p.Cycle, p.Remaining, suffix)
	return nil
}

// runPomodoroNext 推进处于 waiting 状态的番茄钟到下一阶段：阶段结束确认被拒绝
// 或无法弹窗后的手动恢复入口。
func runPomodoroNext(args []string) error {
	fs := flag.NewFlagSet("pomo next", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("pomo next: 用法: tempo pomo next NAME")
	}

	return callAndReport(protocol.Request{
		Action: protocol.ActionPomodoroNext,
		Name:   fs.Arg(0),
	})
}

// runPomodoroStop 终止番茄钟序列。可选位置参数 NAME 指定序列；省略时由 daemon
// 解析唯一活动序列，与普通 stop 一样走 IPC。
func runPomodoroStop(args []string) error {
	fs := flag.NewFlagSet("pomo stop", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("pomo stop: 用法: tempo pomo stop [NAME]")
	}

	return callAndReport(protocol.Request{
		Action: protocol.ActionPomodoroStop,
		Name:   fs.Arg(0),
	})
}

// runHiddenNotify 实现隐藏辅助命令 __notify：由 Windows 服务（Session 0）在
// 活动用户会话中拉起，负责在用户桌面显示通知。参数依次为 title、message 与以
// 毫秒计的建议显示时长，均为固定位置参数（内部命令，不对外承诺稳定性）；
// 子命令名须与 internal/notify/notify_windows.go 的 hiddenNotifyCmd 保持一致。
func runHiddenNotify(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("__notify: 用法: tempo __notify TITLE MESSAGE DURATION_MS")
	}
	durationMs, err := strconv.Atoi(args[2])
	if err != nil {
		return fmt.Errorf("__notify: 解析显示时长 %q: %w", args[2], err)
	}
	return notify.NewNotifier().Notify(args[0], args[1], time.Duration(durationMs)*time.Millisecond)
}

// callAndReport 发送请求并按响应输出结果，失败响应转为错误返回。
func callAndReport(req protocol.Request) error {
	resp, err := client.Call(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Println("ok")
	return nil
}

// defaultConfigPath 返回默认规则配置路径：$XDG_CONFIG_HOME/tempo/rules.toml。
func defaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "rules.toml"
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "tempo", "rules.toml")
}

// defaultLogPath 返回默认日志路径：$XDG_STATE_HOME/tempo/tempo.log，回退 ~/.local/state/tempo/tempo.log。
func defaultLogPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "tempo", "tempo.log")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "tempo.log"
	}
	return filepath.Join(home, ".local", "state", "tempo", "tempo.log")
}
