//go:build linux

package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tempo/internal/protocol"
	"tempo/internal/transport"
)

// startTestServer 启动监听并把每条连接交给 handleConn 处理，返回 socket 路径。
// 通过设置 XDG_RUNTIME_DIR 使 transport.NewServer() 在本测试的临时目录内创建 socket，
// 避免与真实 daemon 或其他测试冲突。
func startTestServer(t *testing.T, m *Manager) string {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	srv := transport.NewServer()
	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	go func() {
		for {
			conn, err := srv.Accept()
			if err != nil {
				return
			}
			go handleConn(m, conn)
		}
	}()
	return srv.Address()
}

// exchange 向 daemon 发送一行 JSON 请求并读取一行 JSON 响应。
func exchange(t *testing.T, sockPath, line string) protocol.Response {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// 测试结束后关闭连接，错误无法补救，显式忽略以满足 errcheck。
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	respLine, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(bytes.TrimRight(respLine, "\n"), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	return resp
}

func TestHandleConnRoundtrip(t *testing.T) {
	m := NewManager(nil, nil, nil)
	sockPath := startTestServer(t, m)

	resp := exchange(t, sockPath, `{"action":"start","name":"tea","args":{"duration":"10s"}}`)
	if !resp.OK {
		t.Fatalf("start 应成功: %+v", resp)
	}

	resp = exchange(t, sockPath, `{"action":"list"}`)
	if !resp.OK || len(resp.Tasks) != 1 || resp.Tasks[0].Name != "tea" || resp.Tasks[0].State != "running" {
		t.Fatalf("list 响应异常: %+v", resp)
	}
}

func TestHandleConnInvalidJSON(t *testing.T) {
	m := NewManager(nil, nil, nil)
	sockPath := startTestServer(t, m)
	resp := exchange(t, sockPath, "这不是 JSON")
	if resp.OK || !strings.Contains(resp.Error, "读取请求失败") {
		t.Errorf("非法请求应返回错误响应，得到: %+v", resp)
	}
}

func TestDispatchLifecycle(t *testing.T) {
	m := NewManager(nil, nil, nil)

	resp := dispatch(m, protocol.Request{
		Action: protocol.ActionStart,
		Name:   "tea",
		Args:   map[string]string{"duration": "10s", "rule": "", "finish": ""},
	})
	if !resp.OK {
		t.Fatalf("start 失败: %s", resp.Error)
	}

	if resp = dispatch(m, protocol.Request{Action: protocol.ActionList}); !resp.OK ||
		len(resp.Tasks) != 1 || resp.Tasks[0].State != "running" {
		t.Fatalf("list 响应异常: %+v", resp)
	}

	for _, tc := range []struct{ action, wantState string }{
		{protocol.ActionPause, "paused"},
		{protocol.ActionResume, "running"},
	} {
		if resp = dispatch(m, protocol.Request{Action: tc.action, Name: "tea"}); !resp.OK {
			t.Fatalf("%s 失败: %s", tc.action, resp.Error)
		}
		if resp = dispatch(m, protocol.Request{Action: protocol.ActionList}); !resp.OK ||
			resp.Tasks[0].State != tc.wantState {
			t.Fatalf("%s 后状态 = %q, want %q", tc.action, resp.Tasks[0].State, tc.wantState)
		}
	}

	if resp = dispatch(m, protocol.Request{Action: protocol.ActionStop, Name: "tea"}); !resp.OK {
		t.Fatalf("stop 失败: %s", resp.Error)
	}
	if resp = dispatch(m, protocol.Request{Action: protocol.ActionList}); len(resp.Tasks) != 0 {
		t.Fatalf("stop 后应无任务: %+v", resp.Tasks)
	}
}

func TestDispatchErrors(t *testing.T) {
	m := NewManager(nil, nil, nil)

	if resp := dispatch(m, protocol.Request{Action: protocol.ActionStart, Name: ""}); resp.OK ||
		resp.Error != "任务名称不能为空" {
		t.Errorf("空名称 start 应失败，得到: %+v", resp)
	}

	resp := dispatch(m, protocol.Request{Action: "no-such-action"})
	if resp.OK || !strings.Contains(resp.Error, "未知 action") {
		t.Errorf("未知 action 应返回错误，得到: %+v", resp)
	}

	resp = dispatch(m, protocol.Request{Action: protocol.ActionPause, Name: "ghost"})
	if resp.OK || !strings.Contains(resp.Error, "不存在") {
		t.Errorf("暂停不存在任务应报错，得到: %+v", resp)
	}
}

// TestDispatchPomodoroLifecycle 验证 pomodoro start/status/stop 的分发链路，
// 含 Name 为空时对唯一活动序列的解析。
func TestDispatchPomodoroLifecycle(t *testing.T) {
	m := NewManager(nil, nil, nil)

	if resp := dispatch(m, protocol.Request{
		Action: protocol.ActionPomodoroStart,
		Name:   "focus",
		Args: map[string]string{
			"work": "10s", "short": "5s", "long": "15s", "cycle": "2",
			"rule": "", "finish_on": "sequence",
		},
	}); !resp.OK {
		t.Fatalf("pomodoro start 失败: %s", resp.Error)
	}

	// Name 为空时应解析到唯一活动序列。
	resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus})
	if !resp.OK || resp.Pomodoro == nil {
		t.Fatalf("pomodoro status 响应异常: %+v", resp)
	}
	if resp.Pomodoro.Phase != phaseWork || resp.Pomodoro.Completed != 0 || resp.Pomodoro.Cycle != 2 {
		t.Errorf("PomodoroInfo = %+v, want phase=work completed=0 cycle=2", resp.Pomodoro)
	}

	if resp = dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStop}); !resp.OK {
		t.Fatalf("pomodoro stop 失败: %s", resp.Error)
	}

	// 无活动番茄钟：status 返回 OK 且无 Pomodoro 字段；stop 报 no active pomodoro。
	resp = dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus})
	if !resp.OK || resp.Pomodoro != nil {
		t.Errorf("无活动番茄钟时 status 应 OK 且无 Pomodoro 字段: %+v", resp)
	}
	resp = dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStop})
	if resp.OK || resp.Error != "no active pomodoro" {
		t.Errorf("无活动番茄钟时 stop 应报 no active pomodoro，得到: %+v", resp)
	}
}

// TestDispatchPomodoroErrors 验证参数校验与名称解析失败的错误路径。
func TestDispatchPomodoroErrors(t *testing.T) {
	m := NewManager(nil, nil, nil)

	if resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStart, Name: ""}); resp.OK ||
		resp.Error != "番茄钟名称不能为空" {
		t.Errorf("空名称 pomodoro start 应失败，得到: %+v", resp)
	}

	if resp := dispatch(m, protocol.Request{
		Action: protocol.ActionPomodoroStart,
		Name:   "focus",
		Args:   map[string]string{"work": "abc"},
	}); resp.OK || !strings.Contains(resp.Error, "解析 work") {
		t.Errorf("非法 work 应失败，得到: %+v", resp)
	}

	if resp := dispatch(m, protocol.Request{
		Action: protocol.ActionPomodoroStart,
		Name:   "focus",
		Args:   map[string]string{"cycle": "x"},
	}); resp.OK || !strings.Contains(resp.Error, "解析 cycle") {
		t.Errorf("非法 cycle 应失败，得到: %+v", resp)
	}

	if resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus, Name: "ghost"}); resp.OK ||
		!strings.Contains(resp.Error, "不存在") {
		t.Errorf("不存在的番茄钟 status 应失败，得到: %+v", resp)
	}
}

func TestServeRejectsNilManager(t *testing.T) {
	if err := Serve(nil); err == nil {
		t.Error("Serve(nil) 应返回错误")
	}
}

func TestServeReturnsErrorWhenListenFails(t *testing.T) {
	// 指向不存在的目录使 net.Listen 失败。
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(t.TempDir(), "missing", "nested"))
	if err := Serve(NewManager(nil, nil, nil)); err == nil {
		t.Error("监听失败时 Serve 应返回错误")
	}
}

// waitForPomodoroWaiting 轮询直到指定番茄钟的 status 显示 waiting。
func waitForPomodoroWaiting(t *testing.T, m *Manager, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus, Name: name})
		if resp.OK && resp.Pomodoro != nil && resp.Pomodoro.Waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 %s 进入 waiting 超时", name)
}

// TestDispatchPomodoroNext 验证 pomodoro_next 分发：阶段结束确认被拒进入 waiting
// 后推进成功、waiting 清除；未知名称返回错误。
func TestDispatchPomodoroNext(t *testing.T) {
	overrideAskConfirm(t, false)
	m := NewManager(nil, nil, nil)

	if resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroNext, Name: "ghost"}); resp.OK ||
		!strings.Contains(resp.Error, "不存在") {
		t.Errorf("推进不存在的番茄钟应报错，得到: %+v", resp)
	}

	if resp := dispatch(m, protocol.Request{
		Action: protocol.ActionPomodoroStart,
		Name:   "focus",
		Args:   map[string]string{"work": "20ms", "cycle": "2"},
	}); !resp.OK {
		t.Fatalf("pomodoro start 失败: %s", resp.Error)
	}

	waitForPomodoroWaiting(t, m, "focus", 2*time.Second)

	if resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroNext, Name: "focus"}); !resp.OK {
		t.Fatalf("pomodoro next 失败: %s", resp.Error)
	}

	resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus, Name: "focus"})
	if !resp.OK || resp.Pomodoro == nil || resp.Pomodoro.Waiting {
		t.Fatalf("next 后 status 应无 waiting: %+v", resp)
	}
	if resp.Pomodoro.Phase != phaseShortBreak {
		t.Errorf("next 后阶段 = %q, want short_break", resp.Pomodoro.Phase)
	}

	if resp = dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStop, Name: "focus"}); !resp.OK {
		t.Fatalf("pomodoro stop 失败: %s", resp.Error)
	}
}
