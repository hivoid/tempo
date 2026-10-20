//go:build linux

package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tempo/internal/protocol"
)

func TestSocketPathPrefersXDGRuntimeDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	if got, want := SocketPath(), filepath.Join(dir, socketName); got != want {
		t.Errorf("SocketPath() = %q, want %q", got, want)
	}
}

func TestSocketPathFallsBackToTempDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if got, want := SocketPath(), filepath.Join(os.TempDir(), socketName); got != want {
		t.Errorf("SocketPath() = %q, want %q", got, want)
	}
}

func TestNewServerAddress(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	if got, want := NewServer().Address(), filepath.Join(dir, socketName); got != want {
		t.Errorf("Address() = %q, want %q", got, want)
	}
}

func TestServerRoundtrip(t *testing.T) {
	srv := &unixServer{path: filepath.Join(t.TempDir(), "test.sock")}
	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer srv.Close()

	served := make(chan error, 1)
	go func() {
		c, err := srv.Accept()
		if err != nil {
			served <- err
			return
		}
		defer c.Close()
		req, err := c.ReadRequest()
		if err != nil {
			served <- err
			return
		}
		if req.Action != protocol.ActionList {
			served <- errors.New("action = " + req.Action)
			return
		}
		served <- c.WriteResponse(protocol.Response{
			OK:    true,
			Tasks: []protocol.TaskInfo{{Name: "tea", State: "running", Remaining: "59s"}},
		})
	}()

	conn, err := net.Dial("unix", srv.path)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"action":"list"}` + "\n")); err != nil {
		t.Fatalf("发送请求: %v", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(bytes.TrimRight(line, "\n"), &resp); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if !resp.OK || len(resp.Tasks) != 1 || resp.Tasks[0].Name != "tea" || resp.Tasks[0].State != "running" {
		t.Fatalf("意外响应: %+v", resp)
	}

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("服务端处理失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("服务端未在超时内完成处理")
	}
}

func TestListenRemovesStaleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.sock")
	if err := os.WriteFile(path, []byte("残留文件"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &unixServer{path: path}
	if err := srv.Listen(); err != nil {
		t.Fatalf("Listen 应清理残留文件: %v", err)
	}
	defer srv.Close()
}

func TestCloseRemovesSocketFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	srv := &unixServer{path: path}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Listen 后 socket 文件应存在: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Close 后 socket 文件应被删除，stat 错误 = %v", err)
	}
}

func TestAcceptWithoutListen(t *testing.T) {
	if _, err := (&unixServer{}).Accept(); err == nil {
		t.Error("未 Listen 时 Accept 应返回错误")
	}
}

func TestDoubleListenRejected(t *testing.T) {
	srv := &unixServer{path: filepath.Join(t.TempDir(), "s.sock")}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Listen(); err == nil {
		t.Error("重复 Listen 应返回错误")
	}
}

func TestReadRequestRejectsInvalidJSON(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	conn := &unixConn{conn: c1, reader: bufio.NewReader(c1)}
	go func() {
		_, _ = c2.Write([]byte("不是 JSON\n"))
	}()
	if _, err := conn.ReadRequest(); err == nil {
		t.Error("非法 JSON 应返回错误")
	}
}
