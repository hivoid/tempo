package daemon

// 阶段结束确认弹窗：按 kdialog → zenity → notify-send 的顺序探测并回退。
// 本文件平台无关、不带 build tag；所探测命令均为 Linux 桌面环境工具，
// Windows 提醒由 notify 包的 Toast/MessageBox 承担（CONTEXT.md §3）。

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"time"
)

// confirmTitle 与 confirmMessage 是阶段结束确认弹窗的标题与正文。
const (
	confirmTitle   = "tempo"
	confirmMessage = "阶段结束，是否开始下一阶段？"
)

// promptTimeout 限制单次确认命令的等待时长，超时视为用户未响应、按拒绝处理。
const promptTimeout = 5 * time.Minute

// askConfirmFn 是 askConfirm 的注入点：生产代码固定为 askConfirm，
// 测试替换为桩函数以避免真实弹窗阻塞。
var askConfirmFn = askConfirm

// askConfirm 弹出确认框询问用户是否开始下一阶段，返回用户选择：
//   - kdialog --yesno 可用时以其退出码为准：0 为确认，1 为拒绝；
//   - zenity --question 可用时同上；
//   - notify-send 可用时仅发送通知、无法阻塞等待选择，发出后按拒绝处理；
//   - 三者均不可用时记录日志并按拒绝处理。
//
// 拒绝（或无法弹窗）时调用方进入 waiting 状态，等待 tempo pomodoro next 推进。
// 其他执行错误（如无法连接显示服务）记录日志后继续尝试下一个工具。
func askConfirm(logger *slog.Logger, title, message string) bool {
	if _, err := exec.LookPath("kdialog"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
		defer cancel()
		err := exec.CommandContext(ctx, "kdialog", "--title", title, "--yesno", message).Run()
		switch {
		case err == nil:
			return true
		case isRejectExit(err):
			return false
		default:
			logger.Error("kdialog 确认失败，尝试下一个工具", "error", err)
		}
	}
	if _, err := exec.LookPath("zenity"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
		defer cancel()
		err := exec.CommandContext(ctx, "zenity", "--question", "--title", title, "--text", message).Run()
		switch {
		case err == nil:
			return true
		case isRejectExit(err):
			return false
		default:
			logger.Error("zenity 确认失败", "error", err)
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
		// 仅通知、无法阻塞确认，按拒绝处理进入 waiting。
		return false
	}
	logger.Error("无可用确认工具（kdialog/zenity/notify-send），番茄钟进入 waiting")
	return false
}

// isRejectExit 判断 err 是否为命令以退出码 1 结束——kdialog --yesno 与
// zenity --question 均以退出码 1 表示用户选择否。
func isRejectExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}
