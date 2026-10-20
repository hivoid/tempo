// Package notify 提供跨平台桌面通知的抽象接口与平台实现。
package notify

import "time"

// Notifier 是桌面通知的抽象接口，由各平台文件提供实现。
type Notifier interface {
	// Notify 发送桌面提醒。duration 为建议显示时长，平台可忽略。
	Notify(title, message string, duration time.Duration) error
}
