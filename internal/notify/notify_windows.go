//go:build windows

package notify

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"tempo/internal/platform"

	"github.com/go-toast/toast"
)

// appName 是 Toast 通知使用的 AppUserModelID。
const appName = "tempo"

// toastLongThreshold 是 Toast 显示时长的映射阈值：建议显示时长超过它用 long，否则用 short。
const toastLongThreshold = 5 * time.Second

// windowsNotifier 优先发送 Toast 通知，失败时回退 MessageBoxW。
type windowsNotifier struct{}

// NewNotifier 返回 Windows 平台的 Notifier 实现。
func NewNotifier() Notifier {
	return &windowsNotifier{}
}

// hiddenNotifyCmd 是通知辅助子命令名：服务进程（Session 0）无法显示任何窗口
// UI，改为在用户会话中执行 `tempo __notify ...` 显示通知。须与 main.go switch
// 中的同名分支保持一致。
const hiddenNotifyCmd = "__notify"

// Notify 发送桌面提醒（PRD 第 3 节）：优先 Toast；Toast 创建或显示失败时回退
// MessageBoxW（阻塞至用户确认）。进程由 SCM 以服务身份启动时运行在 Session 0，
// 任何窗口 UI 都不可见，改为在活动用户会话中启动通知辅助进程。错误沿接口
// 返回、由调用方（Manager）记录，本层不 panic、不打日志。
func (n *windowsNotifier) Notify(title, message string, duration time.Duration) error {
	// 会话检测失败时按普通进程处理，保持直接显示路径。
	if inService, err := platform.IsWindowsService(); err == nil && inService {
		return relayToUserSession(title, message, duration)
	}
	if err := sendToast(title, message, duration); err == nil {
		return nil
	}
	return sendMessageBox(title, message)
}

// relayToUserSession 在活动控制台会话中以登录用户身份启动本二进制的通知辅助
// 进程（tempo __notify）：标题、内容与建议显示时长（毫秒）经命令行参数传递，
// 由 platform.LaunchInUserSession 异步拉起，不等待用户交互。
func relayToUserSession(title, message string, duration time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位通知辅助进程: %w", err)
	}
	return platform.LaunchInUserSession(exe,
		hiddenNotifyCmd,
		title,
		message,
		strconv.FormatInt(duration.Milliseconds(), 10),
	)
}

// sendToast 通过 jimbertools/toast 发送 Toast 通知：建议显示时长超过
// toastLongThreshold 时映射为 long，否则为 short。
func sendToast(title, message string, duration time.Duration) error {
	d := toast.Short
	if duration > toastLongThreshold {
		d = toast.Long
	}
	notification := toast.Notification{
		AppID:    appName,
		Title:    title,
		Message:  message,
		Duration: d,
	}
	if err := notification.Push(); err != nil {
		return fmt.Errorf("toast: %w", err)
	}
	return nil
}

// sendMessageBox 调用 Win32 MessageBoxW 作为 Toast 不可用时的回退以保证可见性
// （PRD 第 3 节）。参数依次为：hWnd（0 表示无父窗口）、lpText（正文）、
// lpCaption（标题）、uType（0 = MB_OK）。
func sendMessageBox(title, message string) error {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBoxW := user32.NewProc("MessageBoxW")
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return fmt.Errorf("MessageBoxW: %w", err)
	}
	textPtr, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return fmt.Errorf("MessageBoxW: %w", err)
	}
	ret, _, callErr := messageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		0,
	)
	if ret == 0 {
		return fmt.Errorf("MessageBoxW: %v", callErr)
	}
	return nil
}
