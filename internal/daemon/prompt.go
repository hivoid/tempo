package daemon

// 三选确认弹窗（继续/停止/暂停）：本文件提供平台无关入口与三态结果类型，
// 平台实现按 build tag 隔离并各自提供同名 askConfirmPlatform：
//   - Linux（prompt_linux.go）：kdialog → zenity → notify-send 依次探测回退；
//   - Windows（prompt_windows.go）：TaskDialogIndirect 三按钮对话框，
//     加载失败时回退 MessageBoxW，仍不可用则自动继续。

import "log/slog"

// PromptResult 是阶段结束确认弹窗的三态结果。
type PromptResult int

const (
	// PromptContinue 表示用户选择继续，由调用方推进到下一阶段。
	PromptContinue PromptResult = iota
	// PromptStop 表示用户选择停止，由调用方终止当前流程。
	PromptStop
	// PromptPause 表示用户选择暂停，由调用方进入等待手动恢复的状态。
	PromptPause
)

// String 返回三态结果的名称，用于日志记录。
func (r PromptResult) String() string {
	switch r {
	case PromptContinue:
		return "continue"
	case PromptStop:
		return "stop"
	case PromptPause:
		return "pause"
	default:
		return "unknown"
	}
}

// askConfirmFn 是 askConfirm 的注入点：生产代码固定为 askConfirm，
// 测试替换为桩函数以避免真实弹窗阻塞。
var askConfirmFn = askConfirm

// askConfirm 弹出三选确认框（继续/停止/暂停），返回用户选择：继续 → 推进、
// 停止 → 终止、暂停（或无法弹窗）→ 等待 tempo pomo next 手动推进。
// message 用于交互式弹窗正文，fallbackMessage 用于纯通知回退时的通知正文，
// 两者由调用方按场景区分；具体弹窗方式委托给当前平台的 askConfirmPlatform
// （prompt_linux.go / prompt_windows.go 按 build tag 二选一参与编译）。
func askConfirm(logger *slog.Logger, title, message, fallbackMessage string) PromptResult {
	return askConfirmPlatform(logger, title, message, fallbackMessage)
}
