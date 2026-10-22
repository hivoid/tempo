package daemon

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"tempo/internal/protocol"
	"tempo/internal/transport"
)

// Serve 启动 daemon 的 IPC 服务：创建平台 transport 并监听，
// 循环 Accept，每条连接在独立 goroutine 中处理一个请求并回写响应。
// Accept 失败（如监听器被关闭）时返回错误并清理 socket。
func Serve(m *Manager) error {
	return ServeWithShutdown(m, nil)
}

// ServeWithShutdown 与 Serve 相同，但 stop 关闭后停止接受新连接并返回 nil，
// 供 Windows 服务模式在收到 SCM 停止请求时中断 Accept 循环（Linux 由 systemd
// 直接终止进程，无需该机制）；stop 为 nil 时行为与 Serve 一致。
// 关闭监听器会使阻塞中的 Accept 返回错误，据 shuttingDown 标志区分主动关闭
// 与真实故障；已在处理的请求随返回断开，CLI 侧有 2 秒超时兜底。
// 依赖 Listener 的 Close 与 Accept 可并发调用（net.Listener 标准语义）。
func ServeWithShutdown(m *Manager, stop <-chan struct{}) error {
	if m == nil {
		return errors.New("daemon: manager 不能为空")
	}
	if m.logger == nil {
		m.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	srv := transport.NewServer()
	if err := srv.Listen(); err != nil {
		return fmt.Errorf("daemon: 监听 IPC: %w", err)
	}

	// 收到停止信号后置位标志并关闭监听器解除 Accept 阻塞；两条退出路径
	// （主动关闭/真实故障）互斥，监听器至多被关闭一次。
	var shuttingDown atomic.Bool
	if stop != nil {
		go func() {
			<-stop
			shuttingDown.Store(true)
			_ = srv.Close()
		}()
	}

	m.logger.Info("IPC 监听已启动", "action", "serve")

	for {
		conn, err := srv.Accept()
		if err != nil {
			if shuttingDown.Load() {
				m.logger.Info("IPC 服务已停止", "action", "serve_stop")
				return nil
			}
			// 真实故障：清理 socket 后返回错误。
			_ = srv.Close()
			return fmt.Errorf("daemon: accept: %w", err)
		}
		go handleConn(m, conn)
	}
}

// handleConn 处理单条 IPC 连接：读取一个请求，分发到 Manager 并回写响应。
func handleConn(m *Manager, conn transport.Conn) {
	// 处理完毕关闭连接，错误无法补救，显式忽略以满足 errcheck。
	defer func() { _ = conn.Close() }()

	req, err := conn.ReadRequest()
	if err != nil {
		if errors.Is(err, io.EOF) {
			// 客户端未发送数据即关闭连接，无需响应。
			return
		}
		m.logger.Error("读取 IPC 请求失败", "action", "read_request", "error", err)
		// 尽力回写错误响应，避免客户端等待悬挂。
		_ = conn.WriteResponse(errResponse("读取请求失败: " + err.Error()))
		return
	}

	resp := dispatch(m, req)
	if err := conn.WriteResponse(resp); err != nil {
		m.logger.Error("写 IPC 响应失败", "action", req.Action, "error", err)
	}
}

// dispatch 按请求 action 分发到对应处理逻辑，成功时返回 OK 响应。
func dispatch(m *Manager, req protocol.Request) protocol.Response {
	switch req.Action {
	case protocol.ActionStart:
		return dispatchStart(m, req)
	case protocol.ActionList:
		return protocol.Response{OK: true, Tasks: m.List()}
	case protocol.ActionPause:
		if err := m.Pause(req.Name); err != nil {
			m.logger.Error("暂停任务失败", "task", req.Name, "action", "pause", "error", err)
			return errResponse(err.Error())
		}
		return protocol.Response{OK: true}
	case protocol.ActionResume:
		if err := m.Resume(req.Name); err != nil {
			m.logger.Error("继续任务失败", "task", req.Name, "action", "resume", "error", err)
			return errResponse(err.Error())
		}
		return protocol.Response{OK: true}
	case protocol.ActionStop:
		if err := m.Stop(req.Name); err != nil {
			m.logger.Error("停止任务失败", "task", req.Name, "action", "stop", "error", err)
			return errResponse(err.Error())
		}
		return protocol.Response{OK: true}
	case protocol.ActionPomodoroStart:
		return dispatchPomodoroStart(m, req)
	case protocol.ActionPomodoroStatus:
		return dispatchPomodoroStatus(m, req)
	case protocol.ActionPomodoroStop:
		return dispatchPomodoroStop(m, req)
	case protocol.ActionPomodoroNext:
		if err := m.PomodoroNext(req.Name); err != nil {
			m.logger.Error("推进番茄钟失败", "task", req.Name, "action", "pomodoro_next", "error", err)
			return errResponse(err.Error())
		}
		return protocol.Response{OK: true}
	default:
		return errResponse(fmt.Sprintf("未知 action %q", req.Action))
	}
}

// dispatchStart 处理 start 请求：从 Args 读取 rule/duration/finish 启动计时任务。
func dispatchStart(m *Manager, req protocol.Request) protocol.Response {
	if req.Name == "" {
		return errResponse("任务名称不能为空")
	}
	rule := req.Args["rule"]
	duration := req.Args["duration"]
	finish := req.Args["finish"]

	if err := m.Start(req.Name, rule, duration, finish); err != nil {
		m.logger.Error("启动任务失败", "task", req.Name, "rule", rule, "action", "start", "error", err)
		return errResponse(err.Error())
	}
	return protocol.Response{OK: true}
}

// dispatchPomodoroStart 处理 pomodoro_start 请求：从 Args 读取 work/short/long/cycle/
// rule/finish_on 构造 PomodoroOptions。时长与 cycle 留空表示未设置，由 daemon 按
// [pomodoro] 配置与内置默认值补全；提供但解析失败时返回错误。
func dispatchPomodoroStart(m *Manager, req protocol.Request) protocol.Response {
	if req.Name == "" {
		return errResponse("番茄钟名称不能为空")
	}
	args := req.Args

	opts := PomodoroOptions{
		Rule:     args["rule"],
		FinishOn: args["finish_on"],
	}
	var err error
	if opts.Work, err = argDuration(args, "work"); err != nil {
		return errResponse(err.Error())
	}
	if opts.ShortBreak, err = argDuration(args, "short"); err != nil {
		return errResponse(err.Error())
	}
	if opts.LongBreak, err = argDuration(args, "long"); err != nil {
		return errResponse(err.Error())
	}
	if opts.Cycle, err = argCycle(args); err != nil {
		return errResponse(err.Error())
	}

	if err := m.PomodoroStart(req.Name, opts); err != nil {
		m.logger.Error("启动番茄钟失败", "task", req.Name, "action", "pomodoro_start", "error", err)
		return errResponse(err.Error())
	}
	return protocol.Response{OK: true}
}

// dispatchPomodoroStatus 处理 pomodoro_status 请求：Name 为空时解析唯一活动序列，
// 无活动番茄钟时返回 OK 且不带 Pomodoro 字段，CLI 据此输出 no active pomodoro。
func dispatchPomodoroStatus(m *Manager, req protocol.Request) protocol.Response {
	name, err := resolvePomodoroName(m, req.Name)
	if err != nil {
		return errResponse(err.Error())
	}
	if name == "" {
		return protocol.Response{OK: true}
	}
	info, err := m.PomodoroStatus(name)
	if err != nil {
		m.logger.Error("查询番茄钟状态失败", "task", name, "action", "pomodoro_status", "error", err)
		return errResponse(err.Error())
	}
	return protocol.Response{OK: true, Pomodoro: info}
}

// dispatchPomodoroStop 处理 pomodoro_stop 请求：Name 为空时解析唯一活动序列，
// 无活动番茄钟时报 no active pomodoro。
func dispatchPomodoroStop(m *Manager, req protocol.Request) protocol.Response {
	name, err := resolvePomodoroName(m, req.Name)
	if err != nil {
		return errResponse(err.Error())
	}
	if name == "" {
		return errResponse("no active pomodoro")
	}
	if err := m.PomodoroStop(name); err != nil {
		m.logger.Error("停止番茄钟失败", "task", name, "action", "pomodoro_stop", "error", err)
		return errResponse(err.Error())
	}
	return protocol.Response{OK: true}
}

// resolvePomodoroName 解析目标番茄钟名称：显式指定时原样返回；为空时返回唯一活动
// 序列的名称，无活动序列返回空串（由调用方决定提示方式），多个活动序列时要求指定。
func resolvePomodoroName(m *Manager, name string) (string, error) {
	if name != "" {
		return name, nil
	}
	names := m.PomodoroNames()
	switch len(names) {
	case 0:
		return "", nil
	case 1:
		return names[0], nil
	default:
		return "", fmt.Errorf("存在多个活动番茄钟，请指定名称: %s", strings.Join(names, ", "))
	}
}

// argDuration 解析 Args 中 key 的时长；缺失或为空返回 0，表示未设置、由默认值补全。
func argDuration(args map[string]string, key string) (time.Duration, error) {
	raw := args[key]
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("解析 %s %q: %w", key, raw, err)
	}
	return d, nil
}

// argCycle 解析 Args 中的 cycle；缺失或为空返回 0，表示未设置、由默认值补全。
func argCycle(args map[string]string) (int, error) {
	raw := args["cycle"]
	if raw == "" {
		return 0, nil
	}
	c, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("解析 cycle %q: %w", raw, err)
	}
	return c, nil
}

// errResponse 构造失败响应。
func errResponse(msg string) protocol.Response {
	return protocol.Response{OK: false, Error: msg}
}
