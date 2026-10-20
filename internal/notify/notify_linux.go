//go:build linux

package notify

import (
	"fmt"
	"os/exec"
	"time"
)

// linuxNotifier 通过 notify-send 命令发送桌面通知。
type linuxNotifier struct{}

// NewNotifier 返回 Linux 平台的 Notifier 实现（notify-send）。
func NewNotifier() Notifier {
	return &linuxNotifier{}
}

// Notify 执行 notify-send -u normal -t <ms> title message。
// duration 为建议显示时长，<=0 时省略 -t 参数。
func (n *linuxNotifier) Notify(title, message string, duration time.Duration) error {
	args := []string{"-u", "normal"}
	if duration > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", duration.Milliseconds()))
	}
	args = append(args, title, message)

	cmd := exec.Command("notify-send", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify-send: %w: %s", err, string(out))
	}
	return nil
}
