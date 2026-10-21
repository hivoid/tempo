//go:build linux

package notify

import (
	"fmt"
	"os/exec"
	"time"
)

// defaultNotifySeconds 是 duration 非正（或不足 1 秒）时 -t 采用的默认秒数。
const defaultNotifySeconds = 30

// linuxNotifier 通过 notify-send 命令发送桌面通知。
type linuxNotifier struct{}

// NewNotifier 返回 Linux 平台的 Notifier 实现（notify-send）。
func NewNotifier() Notifier {
	return &linuxNotifier{}
}

// Notify 执行 notify-send -i clock -a tempo -u normal -t <ms> title message，
// 参数顺序固定：-i clock、-a tempo、-u normal、-t <ms>、title、message。
// notify-send 的 -t 单位是毫秒，这里用秒表达再转换：把 duration 按秒取整后
// 乘以 1000；duration <= 0 用默认值 30 秒，乘以 1000 后即 -t 30000。
func (n *linuxNotifier) Notify(title, message string, duration time.Duration) error {
	seconds := int(duration.Seconds())
	if seconds <= 0 {
		// 不足 1 秒同样取默认值，避免算出 -t 0 使通知常驻不消失。
		seconds = defaultNotifySeconds
	}
	ms := seconds * 1000

	args := []string{
		"-i", "clock",
		"-a", "tempo",
		"-u", "normal",
		"-t", fmt.Sprintf("%d", ms),
		title,
		message,
	}

	cmd := exec.Command("notify-send", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("notify-send: %w: %s", err, string(out))
	}
	return nil
}
