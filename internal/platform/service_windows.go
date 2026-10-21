//go:build windows

// Package platform 提供 daemon 托管方式的平台适配：Windows 上将 daemon
// 注册为系统服务，由 SCM（服务控制管理器）驱动其生命周期。
package platform

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// serviceRunner 把 daemon 的 run 生命周期适配为 svc.Handler，由 SCM 启动与停止。
type serviceRunner struct {
	run func() error
}

// IsWindowsService 报告当前进程是否由 SCM 作为服务启动。
// main 在 Windows 上首先调用它决定进入服务运行模式还是 CLI 模式（CONTEXT.md §5.7）。
func IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

// RunAsService 以服务模式运行 run（进程须已由 SCM 启动，name 为服务名）：
// run 在 goroutine 中执行，收到停止/关机请求后上报 StopPending，
// 等待 run 退出，最后上报 Stopped。
//
// 注意：run 的签名不携带 context，无法从外部强制中断；停止时只能等待其
// 自行返回（daemon.Serve 的退出机制待 main 集成服务模式时接线）。
func RunAsService(name string, run func() error) error {
	return svc.Run(name, &serviceRunner{run: run})
}

// Execute 实现 svc.Handler：上报启动/运行状态并处理 SCM 控制请求。
// 返回 (false, 0) 表示正常停止，(false, 1) 表示 run 以错误退出。
func (s *serviceRunner) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	done := make(chan error, 1)
	go func() { done <- s.run() }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			// run 自行退出（如 IPC 监听失败）：按是否出错上报退出码。
			status <- svc.Status{State: svc.Stopped}
			if err != nil {
				return false, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				// SCM 询问当前状态时原样回报。
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				err := <-done
				status <- svc.Status{State: svc.Stopped}
				if err != nil {
					return false, 1
				}
				return false, 0
			}
			// Pause/Continue 未在 Accepts 中声明，不应到达；到达也忽略。
		}
	}
}

// InstallService 安装 Windows 服务：服务启动时执行 exePath（不带参数，
// main 在服务模式下自行进入 daemon 逻辑）。需要管理员权限。
func InstallService(name, displayName, exePath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务控制管理器: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(name); err == nil {
		s.Close()
		return fmt.Errorf("服务 %s 已存在", name)
	}

	s, err := m.CreateService(name, exePath, mgr.Config{
		DisplayName: displayName,
		Description: "tempo CLI 计时器 daemon",
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return fmt.Errorf("创建服务 %s: %w", name, err)
	}
	defer s.Close()

	// 失败自动重启：间隔 10s，失败计数每 60s 重置，保证 daemon 意外退出后可恢复。
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
	}, 60); err != nil {
		return fmt.Errorf("设置失败恢复动作: %w", err)
	}
	return nil
}

// RemoveService 删除已安装的 Windows 服务。服务仍在运行时先请求停止并等待
// 至多 10 秒，避免残留「标记待删除」的悬挂状态。需要管理员权限。
func RemoveService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务控制管理器: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err != nil {
		return fmt.Errorf("打开服务 %s: %w", name, err)
	}
	defer s.Close()

	if status, err := s.Query(); err == nil && status.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("停止服务 %s: %w", name, err)
		}
		waitServiceStopped(s, 10*time.Second)
	}

	return s.Delete()
}

// waitServiceStopped 轮询等待服务进入 Stopped 状态；超时后放弃（交由
// Delete 标记待删除）。
func waitServiceStopped(s *mgr.Service, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := s.Query()
		if err != nil || status.State == svc.Stopped {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
