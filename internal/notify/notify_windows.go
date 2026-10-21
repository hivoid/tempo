//go:build windows

package notify

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

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

// Notify 发送桌面提醒（PRD 第 3 节）：优先 Toast；Toast 创建或显示失败时回退
// MessageBoxW（阻塞至用户确认）。错误沿接口返回、由调用方（Manager）记录，
// 本层不 panic、不打日志。
func (n *windowsNotifier) Notify(title, message string, duration time.Duration) error {
	if err := sendToast(title, message, duration); err == nil {
		return nil
	}
	return sendMessageBox(title, message)
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
