// Package protocol 定义 CLI 与 daemon 之间 IPC 的请求/响应结构。
package protocol

// Request 是 CLI 发送给 daemon 的一条请求。
type Request struct {
	Action string            `json:"action"`
	Name   string            `json:"name,omitempty"`
	Args   map[string]string `json:"args,omitempty"`
}

// Response 是 daemon 返回给 CLI 的一条响应。
type Response struct {
	OK       bool          `json:"ok"`
	Error    string        `json:"error,omitempty"`
	Tasks    []TaskInfo    `json:"tasks,omitempty"`
	Pomodoro *PomodoroInfo `json:"pomodoro,omitempty"`
}

// TaskInfo 描述单个活动任务的对外状态。
type TaskInfo struct {
	Name      string `json:"name"`
	State     string `json:"state"`     // "running" | "paused"
	Remaining string `json:"remaining"` // 人类可读，如 "24m30s"
	Rule      string `json:"rule,omitempty"`
}

// PomodoroInfo 描述番茄钟的当前阶段与周期进度。
type PomodoroInfo struct {
	Phase     string `json:"phase"` // "work" | "short_break" | "long_break"
	Completed int    `json:"completed"`
	Cycle     int    `json:"cycle"`
	Remaining string `json:"remaining"`
	Waiting   bool   `json:"waiting,omitempty"` // 阶段结束待确认，等待 pomodoro next
}

// IPC 动作常量，对应 Request.Action 的合法取值。
const (
	ActionStart          = "start"
	ActionList           = "list"
	ActionPause          = "pause"
	ActionResume         = "resume"
	ActionStop           = "stop"
	ActionPomodoroStart  = "pomodoro_start"
	ActionPomodoroStatus = "pomodoro_status"
	ActionPomodoroStop   = "pomodoro_stop"
	ActionPomodoroNext   = "pomodoro_next"
)
