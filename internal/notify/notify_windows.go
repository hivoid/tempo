//go:build windows

package notify

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
	"unicode/utf16"

	"tempo/internal/platform"
)

// toastLongThreshold 是 Toast 显示时长的映射阈值：建议显示时长超过它用 long，否则用 short。
const toastLongThreshold = 5 * time.Second

// powershellAUMID 是 Windows PowerShell 自带的已注册 AppUserModelID（开始菜单中
// 系统自带 powershell.exe 的快捷方式）。未打包应用发送 Toast 要求其 AUMID 已在
// 开始菜单注册，否则 ToastNotificationManager 的 Show() 会静默丢弃通知且不返回
// 任何错误——此前以自定义 AppID "tempo" 经 go-toast 发送时正是该症状：Push 返回
// nil 但通知从不渲染，上层 MessageBox 回退因此永不触发。改用系统注册的 AUMID
// 可保证 Toast 真正显示，代价是通知来源显示为 "Windows PowerShell"。
const powershellAUMID = `{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe`

// toastTimeout 限制单次 Toast 的发送时长（PowerShell 冷启动通常 1~3 秒），
// 防止 PowerShell 异常时无限阻塞计时结束回调；超时按失败处理并触发 MessageBox 回退。
const toastTimeout = 15 * time.Second

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
// 任何窗口 UI 都对用户不可见：改为 relay 到活动用户会话启动通知辅助进程；
// relay 失败时回退 WTSSendMessage 直接在用户会话桌面弹消息框（不等待关闭）。
// Session 0 内的 Toast/MessageBox 用户不可见，不再作为回退。错误沿接口
// 返回、由调用方（Manager）记录，本层不 panic、不打日志。
func (n *windowsNotifier) Notify(title, message string, duration time.Duration) error {
	// 会话检测失败时按普通进程处理，保持直接显示路径。
	if inService, err := platform.IsWindowsService(); err == nil && inService {
		return notifyFromService(title, message, duration)
	}
	if err := sendToast(title, message, duration); err == nil {
		return nil
	}
	return sendMessageBox(title, message)
}

// notifyFromService 是服务进程（Session 0）的通知路径：先 relay 到用户会话
// 拉起辅助进程显示 Toast；relay 失败（无活动会话、缺特权等）时回退
// WTSSendMessage 在用户会话桌面弹消息框（异步、不阻塞计时流程）。两者都
// 失败时返回合并错误，交由调用方（Manager）记录。
func notifyFromService(title, message string, duration time.Duration) error {
	err := relayToUserSession(title, message, duration)
	if err == nil {
		return nil
	}
	if _, werr := platform.SendMessageToUserSession(title, message, platform.MBOk, duration, false); werr != nil {
		return fmt.Errorf("relay 通知辅助进程: %w；WTS 回退亦失败: %v", err, werr)
	}
	return nil
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

// toastScriptTemplate 是发送 Toast 的 PowerShell 脚本模板，经 -EncodedCommand
// 传入（UTF-16LE 的 base64，规避命令行引号转义且不受 ExecutionPolicy 限制）。
// 标题与正文在脚本内先 base64 解码、再经 SecurityElement.Escape 做 XML 转义，
// 避免内容破坏通知 XML；模板中的 "" 为 PowerShell 双引号字符串内的转义双引号。
const toastScriptTemplate = `
$ErrorActionPreference = 'Stop'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null
$appId = '%s'
$title = [System.Security.SecurityElement]::Escape([System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String('%s')))
$message = [System.Security.SecurityElement]::Escape([System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String('%s')))
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml("<toast duration=""%s""><visual><binding template=""ToastGeneric""><text>$title</text><text>$message</text></binding></visual></toast>")
$toast = New-Object Windows.UI.Notifications.ToastNotification $xml
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appId).Show($toast)
`

// sendToast 直接经 Windows PowerShell 5.1 调用 WinRT 发送 Toast 通知：建议显示
// 时长超过 toastLongThreshold 时映射为 long，否则为 short。不经 go-toast：其对
// 未打包应用的 AUMID 注册不可靠，会产生「成功却不显示」的静默失败（见
// powershellAUMID 注释）。错误沿接口返回，由调用方触发 MessageBox 回退。
func sendToast(title, message string, duration time.Duration) error {
	d := "short"
	if duration > toastLongThreshold {
		d = "long"
	}
	script := fmt.Sprintf(toastScriptTemplate,
		powershellAUMID,
		base64.StdEncoding.EncodeToString([]byte(title)),
		base64.StdEncoding.EncodeToString([]byte(message)),
		d,
	)

	// -EncodedCommand 要求整个脚本先编码为 UTF-16LE 再做 base64。
	u16 := utf16.Encode([]rune(script))
	raw := make([]byte, 2*len(u16))
	for i, r := range u16 {
		binary.LittleEndian.PutUint16(raw[2*i:], r)
	}

	ctx, cancel := context.WithTimeout(context.Background(), toastTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe",
		"-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("toast: %w: %s", err, strings.TrimSpace(string(out)))
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
