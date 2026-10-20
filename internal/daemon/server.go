package daemon

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"tempo/internal/protocol"
	"tempo/internal/transport"
)

// Serve 启动 daemon 的 IPC 服务：创建平台 transport 并监听，
// 循环 Accept，每条连接在独立 goroutine 中处理一个请求并回写响应。
// Accept 失败（如监听器被关闭）时返回错误并清理 socket。
func Serve(m *Manager) error {
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
	defer srv.Close()

	m.logger.Info("IPC 监听已启动", "action", "serve")

	for {
		conn, err := srv.Accept()
		if err != nil {
			return fmt.Errorf("daemon: accept: %w", err)
		}
		go handleConn(m, conn)
	}
}

// handleConn 处理单条 IPC 连接：读取一个请求，分发到 Manager 并回写响应。
func handleConn(m *Manager, conn transport.Conn) {
	defer conn.Close()

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
	case protocol.ActionPomodoroStart, protocol.ActionPomodoroStatus, protocol.ActionPomodoroStop:
		return errResponse("pomodoro not implemented yet")
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

// errResponse 构造失败响应。
func errResponse(msg string) protocol.Response {
	return protocol.Response{OK: false, Error: msg}
}
