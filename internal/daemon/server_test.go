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

	for _, action := range []string{protocol.ActionPomodoroStart, protocol.ActionPomodoroStatus, protocol.ActionPomodoroStop} {
		resp := dispatch(m, protocol.Request{Action: action, Name: "p"})
		if resp.OK || resp.Error != "pomodoro not implemented yet" {
			t.Errorf("%s 应返回未实现错误，得到: %+v", action, resp)
		}
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
