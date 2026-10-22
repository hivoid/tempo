//go:build linux

// Package platform 提供 daemon 托管方式的平台适配：Linux 上 daemon 由 systemd
// 用户服务托管、通过 `tempo daemon` 前台运行，进程内无需服务模式（CONTEXT.md §5.7）。
package platform

import (
	"errors"
	"fmt"
)

// IsWindowsService 在 Linux 上恒为 false：进程启动来源无需判断。
func IsWindowsService() (bool, error) {
	return false, nil
}

// RunAsService 在 Linux 上不受支持：daemon 由 systemd 用户服务托管，
// 不通过进程内服务模式启动。run 的 stop 签名与 Windows 侧保持一致。
func RunAsService(name string, run func(stop <-chan struct{}) error) error {
	return errors.New("RunAsService 仅支持 Windows")
}

// InstallService 在 Linux 上打印 systemd 用户服务的手动安装步骤并返回 nil：
// unit 文件需用户自行复制并启用，本工具不做实际改动。
func InstallService(name, displayName, exePath string) error {
	fmt.Println("Linux 平台请手动安装 systemd 用户服务：")
	fmt.Println("  1. mkdir -p ~/.config/systemd/user")
	fmt.Println("  2. cp contrib/tempo.service ~/.config/systemd/user/")
	fmt.Println("  3. systemctl --user daemon-reload")
	fmt.Println("  4. systemctl --user enable --now tempo.service")
	return nil
}
