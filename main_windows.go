//go:build windows

package main

import (
	"fmt"
	"os"

	"tempo/internal/platform"
)

// serviceName 是 Windows 服务的注册名，安装与运行必须使用同一名称。
const serviceName = "tempo"

// init 将 Windows 实现注册到 main.go 的平台钩子变量。
func init() {
	maybeRunAsServiceFn = maybeRunAsService
	installServiceFn = installService
}

// maybeRunAsService 处理 Windows 服务模式（单二进制双模式，CONTEXT.md §5.7）：
// 进程由 SCM 启动时以服务身份运行 daemon 并阻塞至服务停止，返回 (true, err)；
// 否则返回 (false, nil)，main 继续 CLI 流程。
func maybeRunAsService() (bool, error) {
	inService, err := platform.IsWindowsService()
	if err != nil {
		return false, fmt.Errorf("检测服务运行模式: %w", err)
	}
	if !inService {
		return false, nil
	}
	return true, platform.RunAsService(serviceName, func() error {
		return runDaemon(defaultConfigPath())
	})
}

// installService 将当前二进制安装为 Windows 服务 "tempo"。需要管理员权限。
func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件: %w", err)
	}
	if err := platform.InstallService(serviceName, "tempo daemon", exePath); err != nil {
		return err
	}
	fmt.Printf("服务 %s 已安装\n", serviceName)
	return nil
}
