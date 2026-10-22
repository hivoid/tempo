// Package client 提供 CLI 侧与 daemon 通信的 IPC 客户端。
package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tempo/internal/protocol"
	"tempo/internal/transport"
)

// ErrDaemonNotRunning 表示无法连接 daemon（socket 不存在或连接被拒）。
var ErrDaemonNotRunning = errors.New("daemon not running")

// socketPathFn 返回 daemon socket 路径；独立为包级变量以便测试替换为临时路径，
// 避免测试与真实 daemon 的 socket 互相干扰。
var socketPathFn = transport.SocketPath

// ipcTimeout 是 IPC 连接与请求-响应往返的超时时间。
const ipcTimeout = 2 * time.Second

// Call 连接 daemon，发送一个请求并返回其响应。
// 拨号失败时返回包装后的 ErrDaemonNotRunning，供 CLI 识别后输出提示并退出。
func Call(req protocol.Request) (protocol.Response, error) {
	conn, err := dialSocket(socketPathFn(), ipcTimeout)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("%w: %w", ErrDaemonNotRunning, err)
	}
	defer conn.Close()
	// 限定整个会话的超时，避免 daemon 无响应时 CLI 悬挂。
	_ = conn.SetDeadline(time.Now().Add(ipcTimeout))

	data, err := json.Marshal(req)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("client: 序列化请求: %w", err)
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return protocol.Response{}, fmt.Errorf("client: 发送请求: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return protocol.Response{}, fmt.Errorf("client: 读取响应: %w", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(bytes.TrimRight(line, "\r\n"), &resp); err != nil {
		return protocol.Response{}, fmt.Errorf("client: 解析响应: %w", err)
	}
	return resp, nil
}
