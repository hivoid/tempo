//go:build linux

package daemon

// askConfirmPlatform 是 askConfirm 的 Linux 实现：按 kdialog → zenity →
// notify-send 的顺序探测并回退。

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// promptTimeout 限制单次确认命令的等待时长，超时按执行失败处理、继续回退。
const promptTimeout = 5 * time.Minute

// askConfirmPlatform 弹出三选确认框（继续/停止/暂停），返回用户选择：
//   - kdialog --yesnocancel 可用时以退出码映射：0 继续、1 停止、2 暂停；
//   - zenity --question + --extra-button 可用时：退出码 0 为继续；退出码 1 且
//     stdout 含额外按钮文本「暂停」为暂停（zenity 点击额外按钮时把按钮文本
//     打到 stdout 并以退出码 1 退出），否则为停止；
//   - notify-send 可用时仅发送通知、无法阻塞等待选择，发出后按继续处理
//     （无交互式弹窗时自动继续优于停滞）；
//   - 三者均不可用时记录日志并按继续处理。
//
// message 用于交互式弹窗正文，fallbackMessage 用于纯通知回退时的通知正文，
// 两者由调用方按场景区分。某个工具执行失败（如无法连接显示服务）时记录
// 日志并继续尝试下一个工具。
func askConfirmPlatform(logger *slog.Logger, title, message, fallbackMessage string) PromptResult {
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
		// 仅通知、无法阻塞确认：正文用回退文案，通知送达后自动继续。
		logger.Info("no interactive prompt available, auto-continue")
		ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
		defer cancel()
		if err := exec.CommandContext(ctx, "notify-send",
			"-i", "clock", "-a", "tempo", "-t", "60000",
			title, fallbackMessage).Run(); err != nil {
			logger.Error("notify-send 提示失败", "error", err)
		}
		return PromptContinue
	}
	logger.Warn("no prompt tool available, auto-continue")
	return PromptContinue
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
