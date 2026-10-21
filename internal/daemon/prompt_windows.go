//go:build windows

package daemon

// Windows 的三选确认实现：经 comctl32 的 TaskDialogIndirect 弹出
// 「继续/停止/暂停」三按钮模态对话框，加载失败时回退 user32 的 MessageBoxW。
// 全部经 syscall 调用，无 CGO、无第三方依赖。

import (
	"log/slog"
	"syscall"
	"unsafe"
)

// 自定义按钮 ID 与 Win32 标准对话框返回值（commctrl.h / winuser.h）。
const (
	taskDialogContinue = 1001 // 自定义按钮「继续」的 ID
	taskDialogStop     = 1002 // 自定义按钮「停止」的 ID
	taskDialogPause    = 1003 // 自定义按钮「暂停」的 ID

	idCancel      = 2   // IDCANCEL：用户按 Esc 或关闭窗口
	idYes         = 6   // IDYES：MessageBoxW 的「是」
	idNo          = 7   // IDNO：MessageBoxW 的「否」
	mbYesNoCancel = 0x3 // MB_YESNOCANCEL：是/否/取消三按钮
)

// TaskDialogIndirect 需要 comctl32 v6，与 user32 的 MessageBoxW 均按需加载；
// 加载失败时由 askConfirmPlatform 回退处理。
var (
	procTaskDialogIndirect = syscall.NewLazyDLL("comctl32.dll").NewProc("TaskDialogIndirect")
	procMessageBoxW        = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

// taskDialogButton 对应 Win32 的 TASKDIALOG_BUTTON：按钮 ID 与按钮文本
// （UTF-16 指针）。Go 的自然对齐与 C 一致，无需显式填充。
type taskDialogButton struct {
	nButtonID     int32
	pszButtonText *uint16
}

// taskDialogConfig 对应 Win32 的 TASKDIALOGCONFIG。字段顺序与 Win32 定义一致，
// Go 的自然对齐规则与 MSVC 相同、布局逐字段匹配（x64 上结构体大小为 176）；
// 两个联合各取其一，以单一字段表示。cbSize 必须置为 unsafe.Sizeof 的实际大小。
type taskDialogConfig struct {
	cbSize                  uint32
	hwndParent              uintptr
	hInstance               uintptr
	dwFlags                 uint32
	dwCommonButtons         uint32
	pszWindowTitle          *uint16
	hMainIcon               uintptr // 联合 {HICON hMainIcon; PCWSTR pszMainIcon}
	pszMainInstruction      *uint16
	pszContent              *uint16
	cButtons                uint32
	pButtons                *taskDialogButton
	cRadioButtons           int32
	pRadioButtons           uintptr
	nDefaultButton          int32
	nDefaultRadioButton     int32
	pszVerificationText     uintptr
	pszExpandedInformation  uintptr
	pszExpandedControlText  uintptr
	pszCollapsedControlText uintptr
	hFooterIcon             uintptr // 联合 {HICON hFooterIcon; PCWSTR pszFooterIcon}
	pszFooter               uintptr
	pfCallback              uintptr
	lpCallbackData          uintptr
	cxWidth                 uint32
}

// askConfirmPlatform 是 askConfirm 的 Windows 实现：TaskDialogIndirect 弹出
// 三按钮对话框，阻塞至用户选择。
//   - 1001/1002/1003（自定义按钮）→ 继续/停止/暂停；
//   - IDCANCEL（用户按 Esc 或关闭窗口）→ 暂停，与 Linux 关闭弹窗的语义一致；
//   - 调用失败（如未启用 comctl32 v6 视觉样式清单、会话 0 无交互桌面）→
//     记录 WARN 后按继续处理，与 Linux 的 notify-send 回退语义一致；
//   - TaskDialogIndirect 加载失败（极罕见）→ 回退 MessageBoxW 的
//     MB_YESNOCANCEL：是 → 继续、否 → 停止、取消 → 暂停。
//
// fallbackMessage 在 Windows 上未使用（MessageBoxW 兜底仍以 message 为正文），
// 保留形参以匹配平台无关签名。
func askConfirmPlatform(logger *slog.Logger, title, message, fallbackMessage string) PromptResult {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		logger.Warn("TaskDialogIndirect failed, auto-continue", "error", err)
		return PromptContinue
	}
	contentPtr, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		logger.Warn("TaskDialogIndirect failed, auto-continue", "error", err)
		return PromptContinue
	}

	buttons := make([]taskDialogButton, 0, 3)
	for _, b := range []struct {
		id   int32
		text string
	}{
		{taskDialogContinue, "继续"},
		{taskDialogStop, "停止"},
		{taskDialogPause, "暂停"},
	} {
		textPtr, err := syscall.UTF16PtrFromString(b.text)
		if err != nil {
			logger.Warn("TaskDialogIndirect failed, auto-continue", "error", err)
			return PromptContinue
		}
		buttons = append(buttons, taskDialogButton{nButtonID: b.id, pszButtonText: textPtr})
	}

	// comctl32 中无该导出（加载失败）时回退 MessageBoxW。
	if procTaskDialogIndirect.Find() != nil {
		logger.Warn("TaskDialogIndirect unavailable, falling back to MessageBoxW")
		return messageBoxConfirm(logger, titlePtr, contentPtr)
	}

	cfg := taskDialogConfig{
		cbSize:                  uint32(unsafe.Sizeof(taskDialogConfig{})),
		hwndParent:              0, // 无父窗口
		hInstance:               0,
		dwFlags:                 0,
		dwCommonButtons:         0, // 不使用系统标准按钮
		pszWindowTitle:          titlePtr,
		hMainIcon:               0,
		pszMainInstruction:      nil,
		pszContent:              contentPtr,
		cButtons:                uint32(len(buttons)),
		pButtons:                &buttons[0],
		cRadioButtons:           0,
		pRadioButtons:           0,
		nDefaultButton:          taskDialogContinue, // 继续为默认按钮
		nDefaultRadioButton:     0,
		pszVerificationText:     0,
		pszExpandedInformation:  0,
		pszExpandedControlText:  0,
		pszCollapsedControlText: 0,
		hFooterIcon:             0,
		pszFooter:               0,
		pfCallback:              0,
		lpCallbackData:          0,
		cxWidth:                 0,
	}

	var chosen int32
	hr, _, _ := procTaskDialogIndirect.Call(
		uintptr(unsafe.Pointer(&cfg)),
		uintptr(unsafe.Pointer(&chosen)),
		0, // pnRadioButton：未使用
		0, // pfVerificationFlagChecked：未使用
	)
	if hr != 0 {
		// 调用失败：与 Linux 的 notify-send 回退一致，自动继续。
		logger.Warn("TaskDialogIndirect failed, auto-continue", "hresult", hr)
		return PromptContinue
	}
	switch chosen {
	case taskDialogContinue:
		return PromptContinue
	case taskDialogStop:
		return PromptStop
	case taskDialogPause, idCancel:
		// Esc 或关闭窗口返回 IDCANCEL，视为暂停。
		return PromptPause
	default:
		// 异常返回值按调用失败处理，自动继续。
		logger.Warn("TaskDialogIndirect failed, auto-continue", "button", chosen)
		return PromptContinue
	}
}

// messageBoxConfirm 以 user32 的 MessageBoxW（MB_YESNOCANCEL）作为
// TaskDialogIndirect 不可用时的兜底：是 → 继续、否 → 停止、取消 → 暂停。
// 调用阻塞至用户选择；MessageBoxW 也失败时按继续处理并记录 WARN。
func messageBoxConfirm(logger *slog.Logger, titlePtr, textPtr *uint16) PromptResult {
	ret, _, _ := procMessageBoxW.Call(
		0,                                 // hWnd：无父窗口
		uintptr(unsafe.Pointer(textPtr)),  // lpText：正文（message）
		uintptr(unsafe.Pointer(titlePtr)), // lpCaption：标题（title）
		mbYesNoCancel,
	)
	switch ret {
	case idYes:
		return PromptContinue
	case idNo:
		return PromptStop
	case idCancel:
		return PromptPause
	default:
		// 0 = 调用失败：与 notify-send 回退一致，自动继续。
		logger.Warn("MessageBoxW failed, auto-continue", "ret", ret)
		return PromptContinue
	}
}
