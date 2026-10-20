//go:build windows

package notify

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

// toastScript 通过 PowerShell 调用 WinRT Toast API 发送通知（无第三方依赖）。
// 标题与正文经环境变量传入并用 SecurityElement::Escape 做 XML 转义，防止注入；
// 使用 PowerShell 自身 AUMID，避免应用注册要求。
const toastScript = `$ErrorActionPreference = 'Stop'
$null = [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
$null = [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]
$title = [System.Security.SecurityElement]::Escape($env:TEMPO_NOTIFY_TITLE)
$message = [System.Security.SecurityElement]::Escape($env:TEMPO_NOTIFY_MESSAGE)
$xmlText = '<toast><visual><binding template="ToastGeneric"><text>' + $title + '</text><text>' + $message + '</text></binding></visual></toast>'
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($xmlText)
$toast = New-Object Windows.UI.Notifications.ToastNotification($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($toast)`

// windowsNotifier 优先发送 Toast 通知，失败时回退 MessageBoxW。
type windowsNotifier struct{}

// NewNotifier 返回 Windows 平台的 Notifier 实现。duration 为建议显示时长，本平台忽略。
func NewNotifier() Notifier {
	return &windowsNotifier{}
}

// Notify 发送桌面提醒：Toast 成功即返回；Toast 不可用或失败时回退 MessageBoxW（阻塞至用户确认）。
func (n *windowsNotifier) Notify(title, message string, duration time.Duration) error {
	if err := sendToast(title, message); err == nil {
		return nil
	}
	return sendMessageBox(title, message)
}

// sendToast 通过 PowerShell 调用 WinRT Toast API。
func sendToast(title, message string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", toastScript)
	cmd.Env = append(os.Environ(),
		"TEMPO_NOTIFY_TITLE="+title,
		"TEMPO_NOTIFY_MESSAGE="+message,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("toast: %w: %s", err, string(out))
	}
	return nil
}

// sendMessageBox 调用 Win32 MessageBoxW 作为回退方案。
func sendMessageBox(title, message string) error {
	const (
		mbIconInformation = 0x00000040
		mbSystemModal     = 0x00001000
	)
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
	messageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(textPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		mbIconInformation|mbSystemModal,
	)
	return nil
}
