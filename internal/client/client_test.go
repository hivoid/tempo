//go:build linux

package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"tempo/internal/protocol"
)

// overrideSocketPath 把 socketPathFn 指向 path，测试结束后恢复。
func overrideSocketPath(t *testing.T, path string) {
	t.Helper()
	old := socketPathFn
	socketPathFn = func() string { return path }
	t.Cleanup(func() { socketPathFn = old })
}

// startFakeDaemon 在 path 上监听，并为每条连接调用 handler。
func startFakeDaemon(t *testing.T, path string, handler func(c net.Conn)) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("启动假 daemon: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go handler(c)
		}
	}()
}

func TestCallRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.sock")
	startFakeDaemon(t, path, func(c net.Conn) {
		// 关闭失败无法补救，显式忽略以满足 errcheck。
		defer func() { _ = c.Close() }()
		var req protocol.Request
		if err := json.NewDecoder(bufio.NewReader(c)).Decode(&req); err != nil {
			return
		}
		data, err := json.Marshal(protocol.Response{
			OK:    true,
			Tasks: []protocol.TaskInfo{{Name: "tea", State: "running", Remaining: "59s"}},
		})
		if err != nil {
			return
		}
		_, _ = c.Write(append(data, '\n'))
	})
	overrideSocketPath(t, path)

	resp, err := Call(protocol.Request{Action: protocol.ActionList})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.OK || len(resp.Tasks) != 1 || resp.Tasks[0].Name != "tea" || resp.Tasks[0].State != "running" {
		t.Fatalf("意外响应: %+v", resp)
	}
}

func TestCallDaemonNotRunning(t *testing.T) {
	overrideSocketPath(t, filepath.Join(t.TempDir(), "missing.sock"))
	_, err := Call(protocol.Request{Action: protocol.ActionList})
	if !errors.Is(err, ErrDaemonNotRunning) {
		t.Fatalf("应返回 ErrDaemonNotRunning，得到: %v", err)
	}
}

func TestCallTimesOutWhenDaemonSilent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "silent.sock")
	startFakeDaemon(t, path, func(c net.Conn) {
		// 故意不读取也不响应，模拟 daemon 挂起。
		time.Sleep(5 * time.Second)
		_ = c.Close()
	})
	overrideSocketPath(t, path)

	start := time.Now()
	if _, err := Call(protocol.Request{Action: protocol.ActionList}); err == nil {
		t.Fatal("daemon 无响应时应返回错误")
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("应在 2 秒超时后返回，实际耗时 %v", elapsed)
	}
}
