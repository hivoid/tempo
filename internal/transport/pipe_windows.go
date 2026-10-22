//go:build windows

package transport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"

	"tempo/internal/protocol"
)

// pipeName 是 daemon IPC 命名管道的完整地址。
const pipeName = `\\.\pipe\tempo-daemon`

// pipeSDDL 是命名管道的安全描述符（SDDL）：DACL 带保护标志，授予 SYSTEM（SY）、
// Administrators（BA）与 Authenticated Users（AU）完全访问权限。命名管道默认
// 取创建进程令牌的默认 DACL——服务以 LocalSystem 运行时普通用户不在其中，
// 其 CLI 将无法连接写入，因此显式放宽到本机已登录用户。
const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;AU)"

// pipeServer 基于 Windows 命名管道的 Server 实现。
type pipeServer struct {
	listener net.Listener
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

// Listen 监听命名管道。pipeSDDL 由 go-winio 在创建每个管道实例时应用，
// 无需自行解析 SDDL 或保持描述符存活。
func (s *pipeServer) Listen() error {
	if s.listener != nil {
		return fmt.Errorf("transport: 已在监听 %s", s.path)
	}
	l, err := winio.ListenPipe(s.path, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL,
	})
	if err != nil {
		return fmt.Errorf("transport: 监听 %s: %w", s.path, err)
	}
	s.listener = l
	return nil
}

// Accept 等待并返回下一条连接。go-winio 的 Accept 直接返回 net.Conn，
// 据此包装为 transport.Conn。
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
