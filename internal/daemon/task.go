// Package daemon 实现计时任务、番茄钟状态机与 IPC 服务端。
package daemon

import (
	"sync"
	"time"
)

// TaskState 表示任务状态。
type TaskState int

const (
	// StateRunning 表示任务正在计时。
	StateRunning TaskState = iota
	// StatePaused 表示任务已暂停，保留剩余时长。
	StatePaused
)

// String 返回 TaskState 的人类可读表示，取值与 protocol.TaskInfo.State 一致。
func (s TaskState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StatePaused:
		return "paused"
	default:
		return "unknown"
	}
}

// Task 是一个计时任务，持有计时器与状态，所有方法并发安全。
type Task struct {
	mu sync.Mutex

	name         string
	rule         string
	finishNotify string // 计时结束提醒命令，空时由 Manager 走平台默认通知
	finishEvent  string // 计时结束事件命令，空 = 不执行

	deadline  time.Time     // 运行中的截止时刻
	remaining time.Duration // 暂停时保留的剩余时长
	timer     *time.Timer
	state     TaskState
	finished  bool // 计时已到点，结束动作处理中
	stopped   bool // 已被 Stop() 显式停止

	onFinish func(*Task)
}

// newTask 创建并立即启动一个计时任务。d 到点时调用 onFinish，结束行为（通知、
// 结束事件、番茄钟下一阶段）由注入方决定，Task 自身不执行任何动作。
// onFinish 为必需回调。
func newTask(name, rule, finishNotify, finishEvent string, d time.Duration, onFinish func(*Task)) *Task {
	t := &Task{
		name:         name,
		rule:         rule,
		finishNotify: finishNotify,
		finishEvent:  finishEvent,
		deadline:  time.Now().Add(d),
		remaining: d,
		state:     StateRunning,
		onFinish:  onFinish,
	}
	t.timer = time.AfterFunc(d, t.fire)
	return t
}

// fire 是计时器到点回调：仅在任务运行中且未被暂停/停止时触发结束动作。
// 回调在锁外执行，允许 onFinish 反向调用 Task 的方法（如 Stop）。
func (t *Task) fire() {
	t.mu.Lock()
	if t.state != StateRunning || t.finished || t.stopped {
		t.mu.Unlock()
		return
	}
	t.finished = true
	t.remaining = 0
	onFinish := t.onFinish
	t.mu.Unlock()

	if onFinish != nil {
		onFinish(t)
	}
}

// Pause 暂停任务并用 time.Until(deadline) 计算剩余时长（CONTEXT.md §5.5）。
// 对已暂停、已结束或已停止的任务调用无副作用。
func (t *Task) Pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != StateRunning || t.finished || t.stopped {
		return
	}
	t.remaining = time.Until(t.deadline)
	if t.timer != nil {
		// 返回值被忽略：回调可能已进入 fire()，由其 finished/stopped 守卫兜底。
		t.timer.Stop()
	}
	t.state = StatePaused
}

// Resume 从剩余时长恢复计时。对运行中的任务调用无副作用。
func (t *Task) Resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != StatePaused || t.finished || t.stopped {
		return
	}
	t.deadline = time.Now().Add(t.remaining)
	t.timer = time.AfterFunc(t.remaining, t.fire)
	t.state = StateRunning
}

// Stop 停止计时器；任务由调用方（Manager）负责从注册表中移除。
// 重复调用无副作用。
func (t *Task) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.stopped = true
	if t.timer != nil {
		t.timer.Stop()
	}
}

// Remaining 返回剩余时长；已结束或已停止的任务返回 0。
func (t *Task) Remaining() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case t.finished || t.stopped:
		return 0
	case t.state == StateRunning:
		return time.Until(t.deadline)
	default:
		return t.remaining
	}
}

// State 返回当前任务状态。
func (t *Task) State() TaskState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}
