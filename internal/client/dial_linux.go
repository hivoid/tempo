//go:build linux

package client

import (
	"net"
	"time"
)

// dialSocket 按平台建立 IPC 连接：Linux 连接 Unix domain socket。
func dialSocket(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}
