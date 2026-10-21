//go:build windows

package main

// Windows 的服务模式检测与 install-service 已下沉到 internal/platform
// （service_windows.go），main.go 通过 platform.IsWindowsService / RunAsService /
// InstallService 直接调用，不再需要 init 注入平台钩子。
// 本文件仅作占位保留，以标示平台边界。
