package daemon

// 阶段结束三选确认弹窗（继续/停止/暂停）：按 kdialog → zenity → notify-send
// 的顺序探测并回退。本文件平台无关、不带 build tag；所探测命令均为 Linux
// 桌面环境工具，Windows 提醒由 notify 包的 Toast/MessageBox 承担（CONTEXT.md §3）。

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// confirmTitle 与 confirmMessage 是阶段结束确认弹窗的标题与正文。
const (
	confirmTitle   = "tempo"
	confirmMessage = "阶段结束，是否开始下一阶段？"
)

// promptTimeout 限制单次确认命令的等待时长，超时按执行失败处理、继续回退。
const promptTimeout = 5 * time.Minute

// PromptResult 是阶段结束确认弹窗的三态结果。
type PromptResult int

const (
	// PromptContinue 表示用户选择继续，推进到下一阶段。
	PromptContinue PromptResult = iota
	// PromptStop 表示用户选择停止，终止整个番茄钟序列。
	PromptStop
	// PromptPause 表示用户选择暂停（或无法弹窗），进入 waiting 等待 pomo next。
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

// askConfirm 弹出三选确认框（继续/停止/暂停），返回用户选择：
//   - kdialog --yesnocancel 可用时以退出码映射：0 继续、1 停止、2 暂停；
//   - zenity --question + --extra-button 可用时：退出码 0 为继续；退出码 1 且
//     stdout 含额外按钮文本「暂停」为暂停（zenity 点击额外按钮时把按钮文本
//     打到 stdout 并以退出码 1 退出），否则为停止；
//   - notify-send 可用时仅发送通知、无法阻塞等待选择，发出后按暂停处理
//     （关闭通知不等于用户想终止序列）；
//   - 三者均不可用时记录日志并按暂停处理。
//
// 暂停（或无法弹窗）时调用方进入 waiting 状态，等待 tempo pomo next 推进。
// 某个工具执行失败（如无法连接显示服务）时记录日志并继续尝试下一个工具。
func askConfirm(logger *slog.Logger, title, message string) PromptResult {
	if _, err := exec.LookPath("kdialog"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
		defer cancel()
		err := exec.CommandContext(ctx, "kdialog", "--title", title,
			"--yes-label", "继续", "--no-label", "停止", "--cancel-label", "暂停",
			"--yesnocancel", message).Run()
		switch exitCode(err) {
		case 0:
			logger.Info("确认弹窗已选择", "tool", "kdialog", "result", PromptContinue)
			return PromptContinue
		case 1:
			logger.Info("确认弹窗已选择", "tool", "kdialog", "result", PromptStop)
			return PromptStop
		case 2:
			logger.Info("确认弹窗已选择", "tool", "kdialog", "result", PromptPause)
			return PromptPause
		default:
			logger.Error("kdialog 确认失败，尝试 zenity", "error", err)
		}
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "zenity", "--question",
			"--title", title, "--text", message,
			"--ok-label", "继续", "--cancel-label", "停止",
			"--extra-button", "暂停").Output()
		switch {
		case err == nil:
			logger.Info("确认弹窗已选择", "tool", "zenity", "result", PromptContinue)
			return PromptContinue
		case exitCode(err) == 1 && strings.Contains(string(out), "暂停"):
			logger.Info("确认弹窗已选择", "tool", "zenity", "result", PromptPause)
			return PromptPause
		case exitCode(err) == 1:
			logger.Info("确认弹窗已选择", "tool", "zenity", "result", PromptStop)
			return PromptStop
		default:
			logger.Error("zenity 确认失败，尝试 notify-send", "error", err)
		}
	}
	if _, err := exec.LookPath("notify-send"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
		defer cancel()
		if err := exec.CommandContext(ctx, "notify-send",
			"-i", "clock", "-n", "alarm", "-a", "tempo", "-t", "60000",
			title, message).Run(); err != nil {
			logger.Error("notify-send 提示失败", "error", err)
		}
		// 仅通知、无法阻塞确认，按暂停处理进入 waiting。
		logger.Info("确认弹窗已选择", "tool", "notify-send", "result", PromptPause)
		return PromptPause
	}
	logger.Error("无可用确认工具（kdialog/zenity/notify-send），番茄钟进入 waiting")
	return PromptPause
}

// exitCode 返回命令的退出码：err 为 nil 时为 0；非 ExitError（被超时杀死、
// 启动失败等）返回 -1，供调用方按「其他错误」继续回退。
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
