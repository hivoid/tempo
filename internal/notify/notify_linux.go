//go:build linux

package notify

import (
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// linuxNotifier 通过 notify-send 命令发送桌面通知。
type linuxNotifier struct{}

// NewNotifier 返回 Linux 平台的 Notifier 实现（notify-send）。
func NewNotifier() Notifier {
	return &linuxNotifier{}
}

// Notify 执行 notify-send -u normal -t <ms> title message。
// duration 为建议显示时长，<=0 时使用 10 秒默认值。
func (n *linuxNotifier) Notify(title, message string, duration time.Duration) error {
	ms := duration.Milliseconds()
	if ms <= 0 {
		ms = 10000
	}
	cmd := exec.Command("notify-send",
		"-u", "normal",
		"-t", strconv.FormatInt(ms, 10),
		title, message,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify-send: %w: %s", err, string(out))
	}
	return nil
}
