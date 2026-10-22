//go:build windows

package client

import (
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// dialSocket 按平台建立 IPC 连接：Windows 连接命名管道。DialPipe 以传入超时
// 等待管道实例可用，超时或管道不存在时返回错误，与 Unix 侧拨号语义对齐。
func dialSocket(path string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(path, &timeout)
}
