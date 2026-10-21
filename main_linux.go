//go:build linux

package main

import "fmt"

// init 将 Linux 实现注册到 main.go 的平台钩子变量。
func init() {
	maybeRunAsServiceFn = maybeRunAsService
	installServiceFn = installService
}

// maybeRunAsService 在 Linux 上恒为非服务模式：daemon 由 systemd 用户服务托管、
// 通过 `tempo daemon` 前台运行，无需在进程内判断启动来源（CONTEXT.md §5.7）。
func maybeRunAsService() (bool, error) {
	return false, nil
}

// installService 在 Linux 上保持原有提示行为：systemd 用户服务需手动复制 unit
// 文件安装，不做实际动作（Windows 侧由 main_windows.go 实际安装服务）。
func installService() error {
	fmt.Println("Linux 平台请手动安装 systemd 用户服务：")
	fmt.Println("  1. mkdir -p ~/.config/systemd/user")
	fmt.Println("  2. cp contrib/tempo-daemon.service ~/.config/systemd/user/")
	fmt.Println("  3. systemctl --user daemon-reload")
	fmt.Println("  4. systemctl --user enable --now tempo-daemon.service")
	return nil
}
