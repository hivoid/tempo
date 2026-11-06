package daemon

import (
	"testing"
	"time"

	"tempo/internal/protocol"
)

// newStatusTestManager 构造无外部依赖的 Manager（日志丢弃、无 notifier、空配置），
// 仅供本文件的 pomodoro status 相关用例使用；命名独特以避免与其他测试文件冲突。
func newStatusTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(nil, nil, nil)
}

// startStatusTestPomodoro 以显式参数启动一个番茄钟序列（不依赖默认值补全），
// 并用 t.Cleanup 保证用例结束时终止序列、清理计时器与注册表。
func startStatusTestPomodoro(t *testing.T, m *Manager, name string) {
	t.Helper()
	err := m.PomodoroStart(name, PomodoroOptions{
		Work:       25 * time.Minute,
		ShortBreak: 5 * time.Minute,
		LongBreak:  15 * time.Minute,
		Cycle:      4,
	})
	if err != nil {
		t.Fatalf("启动番茄钟 %s: %v", name, err)
	}
	t.Cleanup(func() { _ = m.PomodoroStop(name) })
}

func TestManagerPomodoroStatusesListsAllSequences(t *testing.T) {
	m := newStatusTestManager(t)
	startStatusTestPomodoro(t, m, "beta")
	startStatusTestPomodoro(t, m, "alpha")

	states := m.PomodoroStatuses()
	if len(states) != 2 {
		t.Fatalf("期望返回 2 条状态，得到 %d 条", len(states))
	}
	if states[0].Name != "alpha" || states[1].Name != "beta" {
		t.Fatalf("期望按名称排序 [alpha beta]，得到 [%s %s]", states[0].Name, states[1].Name)
	}
	if states[0].Phase != phaseWork {
		t.Fatalf("期望初始阶段为 %s，得到 %s", phaseWork, states[0].Phase)
	}
	if states[0].Completed != 0 || states[0].Cycle != 4 {
		t.Fatalf("期望 completed=0 cycle=4，得到 completed=%d cycle=%d",
			states[0].Completed, states[0].Cycle)
	}
	if states[0].Remaining <= 0 {
		t.Fatalf("期望活动阶段剩余时长为正，得到 %s", states[0].Remaining)
	}
}

func TestDispatchPomodoroStatusWithoutNameReturnsAll(t *testing.T) {
	m := newStatusTestManager(t)
	startStatusTestPomodoro(t, m, "p1")
	startStatusTestPomodoro(t, m, "p2")

	resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus})
	if !resp.OK {
		t.Fatalf("期望 OK 响应，得到错误: %s", resp.Error)
	}
	if resp.Pomodoro != nil {
		t.Fatal("未指定名称时不应填充单条 Pomodoro 字段")
	}
	if len(resp.Pomodoros) != 2 {
		t.Fatalf("期望返回 2 条状态，得到 %d 条", len(resp.Pomodoros))
	}
	if resp.Pomodoros[0].Name != "p1" || resp.Pomodoros[1].Name != "p2" {
		t.Fatalf("期望按名称排序 [p1 p2]，得到 [%s %s]",
			resp.Pomodoros[0].Name, resp.Pomodoros[1].Name)
	}
}

func TestDispatchPomodoroStatusWithNameReturnsSingle(t *testing.T) {
	m := newStatusTestManager(t)
	startStatusTestPomodoro(t, m, "p1")
	startStatusTestPomodoro(t, m, "p2")

	resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus, Name: "p2"})
	if !resp.OK {
		t.Fatalf("期望 OK 响应，得到错误: %s", resp.Error)
	}
	if resp.Pomodoro == nil {
		t.Fatal("指定名称时期望填充单条 Pomodoro 字段")
	}
	if resp.Pomodoro.Name != "p2" {
		t.Fatalf("期望返回 p2 的状态，得到 %s", resp.Pomodoro.Name)
	}
	if len(resp.Pomodoros) != 0 {
		t.Fatalf("指定名称时不应填充 Pomodoros 列表，得到 %d 条", len(resp.Pomodoros))
	}
}

func TestDispatchPomodoroStatusWithoutAnyReturnsEmptyList(t *testing.T) {
	m := newStatusTestManager(t)

	resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus})
	if !resp.OK {
		t.Fatalf("期望 OK 响应，得到错误: %s", resp.Error)
	}
	if len(resp.Pomodoros) != 0 {
		t.Fatalf("无活动番茄钟时期望空列表，得到 %d 条", len(resp.Pomodoros))
	}
}

func TestDispatchPomodoroStatusUnknownNameErrors(t *testing.T) {
	m := newStatusTestManager(t)

	resp := dispatch(m, protocol.Request{Action: protocol.ActionPomodoroStatus, Name: "ghost"})
	if resp.OK {
		t.Fatal("查询不存在的序列时期望失败响应")
	}
}
