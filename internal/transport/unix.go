//go:build linux

package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"

	"tempo/internal/protocol"
)

// socketName 是 Unix domain socket 的文件名。
const socketName = "tempo-daemon.sock"

// unixServer 基于 Unix domain socket 的 Server 实现。
type unixServer struct {
	listener net.Listener
	path     string
}

// NewServer 创建 Linux 平台的 IPC Server。
func NewServer() Server {
	return &unixServer{path: socketPath()}
}

// socketPath 返回 socket 文件路径：优先 $XDG_RUNTIME_DIR，为空时回退系统临时目录。
func socketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, socketName)
}

// Listen 监听 Unix domain socket，先移除上次异常退出残留的 socket 文件。
func (s *unixServer) Listen() error {
	if s.listener != nil {
		return fmt.Errorf("transport: 已在监听 %s", s.path)
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("transport: 移除残留 socket %s: %w", s.path, err)
	}
	l, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("transport: 监听 %s: %w", s.path, err)
	}
	s.listener = l
	return nil
}

// Accept 等待并返回下一条连接。
func (s *unixServer) Accept() (Conn, error) {
	if s.listener == nil {
		return nil, errors.New("transport: 尚未调用 Listen")
	}
	c, err := s.listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("transport: accept: %w", err)
	}
	return &unixConn{conn: c, reader: bufio.NewReader(c)}, nil
}

// Close 关闭监听器并删除 socket 文件。
func (s *unixServer) Close() error {
	if s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	s.listener = nil
	if rmErr := os.Remove(s.path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) && err == nil {
		err = fmt.Errorf("transport: 移除 socket %s: %w", s.path, rmErr)
	}
	return err
}

// Address 返回 socket 文件路径。
func (s *unixServer) Address() string {
	return s.path
}

// unixConn 基于 net.Conn 的行分隔 JSON 连接实现。
type unixConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

// ReadRequest 读取一行 JSON 并反序列化为 Request。
func (c *unixConn) ReadRequest() (protocol.Request, error) {
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
func (c *unixConn) WriteResponse(resp protocol.Response) error {
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
func (c *unixConn) Close() error {
	return c.conn.Close()
}

// readLine 读取以 \n 分隔的一行原始字节，并去掉行尾换行符。
func (c *unixConn) readLine() ([]byte, error) {
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(line, "\r\n"), nil
}
