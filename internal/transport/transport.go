// Package transport 提供 daemon 与 CLI 之间 IPC 传输的平台无关抽象。
package transport

import "tempo/internal/protocol"

// Server 是 daemon 侧的 IPC 监听器抽象。
type Server interface {
	// Listen 开始监听 IPC 地址。
	Listen() error
	// Accept 阻塞等待并返回下一条连接。
	Accept() (Conn, error)
	// Close 关闭监听器并清理资源。
	Close() error
	// Address 返回 IPC 地址（如 Unix socket 路径）。
	Address() string
}

// Conn 是单条 IPC 连接的抽象，使用按行（\n）分隔的 JSON 编解码。
type Conn interface {
	// ReadRequest 读取一行 JSON 并反序列化为 Request。
	ReadRequest() (protocol.Request, error)
	// WriteResponse 将 Response 序列化为一行 JSON 写出。
	WriteResponse(resp protocol.Response) error
	// Close 关闭连接。
	Close() error
}
