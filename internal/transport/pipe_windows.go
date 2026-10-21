//go:build windows

package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"gopkg.in/natefinch/npipe.v2"

	"tempo/internal/protocol"
)

// pipeName 是 daemon IPC 命名管道的完整地址。
const pipeName = `\\.\pipe\tempo-daemon`

// pipeServer 基于 Windows 命名管道的 Server 实现。
type pipeServer struct {
	listener *npipe.PipeListener
	path     string
}

// NewServer 创建 Windows 平台的 IPC Server。
func NewServer() Server {
	return &pipeServer{path: pipeName}
}

// SocketPath 返回命名管道地址。导出供 CLI 客户端复用，
// 保证 daemon 与客户端使用同一地址（与 unix.go 的 SocketPath 对称）。
func SocketPath() string {
	return pipeName
}

// Listen 监听命名管道。
func (s *pipeServer) Listen() error {
	if s.listener != nil {
		return fmt.Errorf("transport: 已在监听 %s", s.path)
	}
	l, err := npipe.Listen(s.path)
	if err != nil {
		return fmt.Errorf("transport: 监听 %s: %w", s.path, err)
	}
	s.listener = l
	return nil
}

// Accept 等待并返回下一条连接。npipe 的 Accept 返回 *npipe.PipeConn，
// 其实现了 net.Conn，据此包装为 transport.Conn。
func (s *pipeServer) Accept() (Conn, error) {
	if s.listener == nil {
		return nil, errors.New("transport: 尚未调用 Listen")
	}
	c, err := s.listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("transport: accept: %w", err)
	}
	return &pipeConn{conn: c, reader: bufio.NewReader(c)}, nil
}

// Close 关闭监听器。命名管道不落盘，无需清理残留文件。
func (s *pipeServer) Close() error {
	if s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	s.listener = nil
	return err
}

// Address 返回命名管道地址。
func (s *pipeServer) Address() string {
	return s.path
}

// pipeConn 基于 net.Conn 的行分隔 JSON 连接实现。
type pipeConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

// ReadRequest 读取一行 JSON 并反序列化为 Request。
func (c *pipeConn) ReadRequest() (protocol.Request, error) {
	line, err := c.readLine()
	if err != nil {
		return protocol.Request{}, err
	}
	var req protocol.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return protocol.Request{}, fmt.Errorf("transport: 解析请求: %w", err)
	}
	return req, nil
}

// WriteResponse 将 Response 序列化为一行 JSON 写出。
func (c *pipeConn) WriteResponse(resp protocol.Response) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("transport: 序列化响应: %w", err)
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		return fmt.Errorf("transport: 写响应: %w", err)
	}
	return nil
}

// Close 关闭底层连接。
func (c *pipeConn) Close() error {
	return c.conn.Close()
}

// readLine 读取以 \n 分隔的一行原始字节，并去掉行尾换行符。
func (c *pipeConn) readLine() ([]byte, error) {
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}
