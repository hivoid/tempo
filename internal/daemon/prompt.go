package daemon

// 确认/提醒弹窗：本文件提供平台无关入口（三选确认 askConfirm、单按钮结束
// 提醒 notifyDone）、三态结果类型与两平台共享的等待超时常量，平台实现按
// build tag 隔离并各自提供同名 askConfirmPlatform / notifyDonePlatform：
//   - Linux（prompt_linux.go）：kdialog → zenity → notify-send 依次探测回退；
//   - Windows（prompt_windows.go）：TaskDialogIndirect 三按钮对话框，
//     加载失败时回退 MessageBoxW，仍不可用则自动继续。

import (
	"log/slog"
	"time"
)

// promptTimeout 限制单次确认弹窗的等待时长：Linux 侧约束确认命令的执行时长，
// Windows 侧作为 WTSSendMessage 弹窗的自动关闭超时（serviceConfirm 同步等待
// 至多此时长）。超时后由各平台实现按各自的回退语义处理。
const promptTimeout = 5 * time.Minute

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

// notifyDoneFn 是 notifyDone 的注入点：生产代码固定为 notifyDone，
// 测试替换为桩函数以避免真实弹窗阻塞。
var notifyDoneFn = notifyDone

// notifyDone 弹出单按钮结束提醒框（仅一个「知道了」按钮）：与番茄钟阶段结束
// 确认使用相同的平台组件与回退链，但按钮只有一个，阻塞至用户确认或超时
// （promptTimeout）。返回是否已成功向用户展示提醒；无法展示时返回 false，
// 由调用方决定纯通知兜底。具体弹窗方式委托给当前平台的 notifyDonePlatform
// （prompt_linux.go / prompt_windows.go 按 build tag 二选一参与编译）。
func notifyDone(logger *slog.Logger, title, message string) bool {
	return notifyDonePlatform(logger, title, message)
}
