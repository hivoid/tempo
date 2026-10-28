package daemon

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskStateString(t *testing.T) {
	cases := map[TaskState]string{
		StateRunning:  "running",
		StatePaused:   "paused",
		TaskState(42): "unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("TaskState(%d).String() = %q, want %q", int(s), got, want)
		}
	}
}

func TestNewTaskInitial(t *testing.T) {
	task := newTask("tea", "rule-a", "", "", time.Minute, func(*Task) {})
	if got := task.State(); got != StateRunning {
		t.Errorf("初始状态 = %v, want running", got)
	}
	if got := task.Remaining(); got <= 0 || got > time.Minute {
		t.Errorf("初始剩余 = %v, want (0, %v]", got, time.Minute)
	}
}

func TestTaskFinishTriggersCallback(t *testing.T) {
	done := make(chan *Task, 1)
	task := newTask("tea", "", "", "", 30*time.Millisecond, func(tk *Task) { done <- tk })
	select {
	case got := <-done:
		if got != task {
			t.Error("onFinish 应收到当前任务指针")
		}
		if got := task.Remaining(); got != 0 {
			t.Errorf("结束后剩余 = %v, want 0", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onFinish 未被调用")
	}
}

func TestTaskPauseHoldsRemainingAndBlocksFinish(t *testing.T) {
	var fired atomic.Int32
	task := newTask("tea", "", "", "", 200*time.Millisecond, func(*Task) { fired.Add(1) })

	time.Sleep(50 * time.Millisecond)
	task.Pause()
	if got := task.State(); got != StatePaused {
		t.Fatalf("暂停后状态 = %v, want paused", got)
	}
	rem := task.Remaining()
	if rem <= 0 || rem >= 200*time.Millisecond {
		t.Errorf("暂停后剩余 = %v, want (0, 200ms)", rem)
	}

	// 原始 deadline 早已过期，但暂停期间不应触发回调（time.Until + timer.Stop 语义）。
	time.Sleep(300 * time.Millisecond)
	if n := fired.Load(); n != 0 {
		t.Fatalf("暂停期间 onFinish 不应触发，实际 %d 次", n)
	}
	if got := task.State(); got != StatePaused {
		t.Fatalf("暂停期间状态应为 paused，得到 %v", got)
	}

	task.Resume()
	if got := task.State(); got != StateRunning {
		t.Fatalf("继续后状态 = %v, want running", got)
	}

	// 从剩余时长恢复，到点应恰好触发一次回调。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && fired.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if n := fired.Load(); n != 1 {
		t.Errorf("onFinish 触发次数 = %d, want 1", n)
	}
	if got := task.Remaining(); got != 0 {
		t.Errorf("结束后剩余 = %v, want 0", got)
	}
}

func TestTaskStopPreventsCallback(t *testing.T) {
	var fired atomic.Int32
	task := newTask("tea", "", "", "", 50*time.Millisecond, func(*Task) { fired.Add(1) })
	task.Stop()
	time.Sleep(200 * time.Millisecond)
	if n := fired.Load(); n != 0 {
		t.Errorf("停止后 onFinish 不应触发，实际 %d 次", n)
	}
	if got := task.Remaining(); got != 0 {
		t.Errorf("停止后剩余 = %v, want 0", got)
	}
}

func TestTaskResumeWithoutPauseIsNoop(t *testing.T) {
	var fired atomic.Int32
	task := newTask("tea", "", "", "", time.Hour, func(*Task) { fired.Add(1) })
	task.Resume()
	if got := task.State(); got != StateRunning {
		t.Errorf("对运行中任务 Resume 后状态 = %v, want running", got)
	}
	task.Stop()
}
