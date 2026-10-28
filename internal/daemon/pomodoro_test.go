package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tempo/internal/config"
)

// waitForPomodoroPhase 轮询等待番茄钟进入指定阶段，消除计时器异步触发的竞态。
func waitForPomodoroPhase(t *testing.T, p *PomodoroTracker, phase string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.Status().Phase == phase {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待阶段 %s 超时，当前状态: %+v", phase, p.Status())
}

// waitForTasks 轮询等待任务列表达到期望数量：阶段切换中旧任务移除与新任务
// 注册之间存在短暂间隙，断言前需等列表稳定。
func waitForTasks(t *testing.T, m *Manager, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(m.List()) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待任务数量 %d 超时，当前: %+v", want, m.List())
}

// waitForFile 轮询等待文件出现，用于观察异步执行的结束事件命令副作用。
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待文件 %s 超时", path)
}

// overrideAskConfirm 将确认弹窗替换为固定返回 result 的桩函数，测试结束后恢复。
// 真实 askConfirm 会阻塞等待用户交互（最长 promptTimeout），自动化测试必须打桩。
func overrideAskConfirm(t *testing.T, result PromptResult) {
	t.Helper()
	old := askConfirmFn
	askConfirmFn = func(*slog.Logger, string, string, string) PromptResult { return result }
	t.Cleanup(func() { askConfirmFn = old })
}

// waitForWaiting 轮询等待番茄钟进入 waiting 状态：确认弹窗结果由独立 goroutine
// 异步结算，断言前需等待其完成。
func waitForWaiting(t *testing.T, p *PomodoroTracker, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.Status().Waiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 waiting 状态超时，当前: %+v", p.Status())
}

// TestPomodoroStartsInWorkPhase 验证构造后立即启动 work 阶段任务并以序列名注册。
func TestPomodoroStartsInWorkPhase(t *testing.T) {
	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       30 * time.Second,
		ShortBreak: 5 * time.Second,
		LongBreak:  15 * time.Second,
		Cycle:      4,
	}, m)

	st := p.Status()
	if st.Phase != phaseWork || st.Completed != 0 || st.Cycle != 4 {
		t.Errorf("初始状态 = %+v, want phase=work completed=0 cycle=4", st)
	}
	if st.Remaining <= 0 {
		t.Errorf("初始剩余时长异常: %v", st.Remaining)
	}
	waitForTasks(t, m, 1, time.Second)
	if tasks := m.List(); tasks[0].Name != "pomo" || tasks[0].State != "running" {
		t.Errorf("应以序列名注册运行中的阶段任务: %+v", tasks)
	}
	p.Stop()
}

// TestPomodoroFillDefaults 验证参数补全优先级：显式传入 > 全局配置 > PRD 默认值。
func TestPomodoroFillDefaults(t *testing.T) {
	defWork, defShort, defLong, defCycle := config.DefaultPomodoro()

	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("p1", PomodoroOptions{}, m)
	if p.opts.Work != defWork || p.opts.ShortBreak != defShort ||
		p.opts.LongBreak != defLong || p.opts.Cycle != defCycle {
		t.Errorf("空参数应回退 PRD 默认值: %+v", p.opts)
	}
	if p.opts.FinishEach != "" || p.opts.FinishSequence != "" {
		t.Errorf("空参数的结束事件应为空（不执行）: each=%q sequence=%q", p.opts.FinishEach, p.opts.FinishSequence)
	}
	p.Stop()

	cfg := &config.Config{
		Pomodoro: config.PomodoroConfig{
			Work: "1m", ShortBreak: "2m", LongBreak: "3m", Cycle: 3,
		},
	}
	m2 := NewManager(cfg, nil, nil)
	p2 := newPomodoroTracker("p2", PomodoroOptions{}, m2)
	if p2.opts.Work != time.Minute || p2.opts.ShortBreak != 2*time.Minute ||
		p2.opts.LongBreak != 3*time.Minute || p2.opts.Cycle != 3 {
		t.Errorf("应回退全局 [pomodoro] 配置: %+v", p2.opts)
	}
	p2.Stop()

	// 时长非法或 cycle 非正时，[pomodoro] 配置回退 PRD 默认值而非报错。
	cfgBad := &config.Config{
		Pomodoro: config.PomodoroConfig{Work: "abc", ShortBreak: "0s", Cycle: -1},
	}
	m3 := NewManager(cfgBad, nil, nil)
	p3 := newPomodoroTracker("p3", PomodoroOptions{}, m3)
	if p3.opts.Work != defWork || p3.opts.ShortBreak != defShort ||
		p3.opts.LongBreak != defLong || p3.opts.Cycle != defCycle {
		t.Errorf("非法配置应回退 PRD 默认值: %+v", p3.opts)
	}
	p3.Stop()
}

// TestPomodoroWorkToShortBreak 验证专注正常完成后进入短休、completed 递增，
// 且旧阶段任务被同名新任务替换。
func TestPomodoroWorkToShortBreak(t *testing.T) {
	overrideAskConfirm(t, PromptContinue)
	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       30 * time.Millisecond,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)

	waitForPomodoroPhase(t, p, phaseShortBreak, 2*time.Second)
	waitForTasks(t, m, 1, time.Second)

	st := p.Status()
	if st.Completed != 1 {
		t.Errorf("completed = %d, want 1", st.Completed)
	}
	if st.Remaining <= 0 {
		t.Errorf("短休应已开始计时: %v", st.Remaining)
	}
	if tasks := m.List(); tasks[0].Name != "pomo" {
		t.Errorf("阶段任务应以序列名注册: %+v", tasks)
	}
	p.Stop()
}

// TestPomodoroLongBreakAfterCycle 验证每完成 Cycle 个专注后进入长休（验收 7）。
func TestPomodoroLongBreakAfterCycle(t *testing.T) {
	overrideAskConfirm(t, PromptContinue)
	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       20 * time.Millisecond,
		ShortBreak: 20 * time.Millisecond,
		LongBreak:  time.Second,
		Cycle:      2,
	}, m)

	waitForPomodoroPhase(t, p, phaseLongBreak, 3*time.Second)
	if st := p.Status(); st.Completed != 2 {
		t.Errorf("completed = %d, want 2", st.Completed)
	}
	p.Stop()
}

// TestPomodoroPauseKeepsProgress 验证暂停作用于当前阶段任务且 completed 不变
// （F20、验收 9）。
func TestPomodoroPauseKeepsProgress(t *testing.T) {
	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       10 * time.Second,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)

	if err := m.Pause("pomo"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitForTasks(t, m, 1, time.Second)
	if tasks := m.List(); tasks[0].State != "paused" {
		t.Errorf("暂停应作用于当前阶段任务: %+v", tasks)
	}
	before := p.Status()
	time.Sleep(50 * time.Millisecond)
	after := p.Status()
	if after.Completed != before.Completed {
		t.Errorf("暂停不应改变 completed: %d -> %d", before.Completed, after.Completed)
	}
	if after.Remaining != before.Remaining {
		t.Errorf("暂停后剩余时长应冻结: %s -> %s", before.Remaining, after.Remaining)
	}
	if err := m.Resume("pomo"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if tasks := m.List(); tasks[0].State != "running" {
		t.Errorf("继续后阶段任务应恢复运行: %+v", tasks)
	}
	p.Stop()
}

// TestPomodoroEachFinishEventRunsPerStage 验证每阶段结束事件（finish_each）在
// 每个阶段结束时执行：命令取自规则档位，CLI 未传时经 fillDefaults 补全。
func TestPomodoroEachFinishEventRunsPerStage(t *testing.T) {
	overrideAskConfirm(t, PromptPause)
	flagFile := filepath.Join(t.TempDir(), "stage-done")
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"pomo": {Kind: config.RulePomodoro, FinishEach: "touch " + flagFile},
		},
	}
	m := NewManager(cfg, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Rule:       "pomo",
		Work:       30 * time.Millisecond,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)

	waitForFile(t, flagFile, 2*time.Second)
	p.Stop()
}

// TestPomodoroSequenceFinishEventOnStop 验证整个序列结束事件（finish_sequence）
// 仅在序列终止时执行一次，且停止后不残留阶段任务。
func TestPomodoroSequenceFinishEventOnStop(t *testing.T) {
	flagFile := filepath.Join(t.TempDir(), "sequence-done")
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"pomo": {Kind: config.RulePomodoro, FinishSequence: "touch " + flagFile},
		},
	}
	m := NewManager(cfg, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Rule:       "pomo",
		Work:       time.Hour, // 阶段远未结束，事件只能来自 Stop
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)

	p.Stop()
	waitForFile(t, flagFile, 2*time.Second)
	if tasks := m.List(); len(tasks) != 0 {
		t.Errorf("停止后应无阶段任务: %+v", tasks)
	}
}

// TestPomodoroFinishEventsFromOptions 验证 CLI 传入的结束事件优先于规则配置：
// opts 的 finish_sequence 生效，规则的对应配置被覆盖。
func TestPomodoroFinishEventsFromOptions(t *testing.T) {
	flagFile := filepath.Join(t.TempDir(), "cli-sequence-done")
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"pomo": {Kind: config.RulePomodoro, FinishSequence: "exit 1"},
		},
	}
	m := NewManager(cfg, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Rule:           "pomo",
		Work:           time.Hour,
		ShortBreak:     time.Second,
		LongBreak:      time.Second,
		Cycle:          4,
		FinishSequence: "touch " + flagFile,
	}, m)

	p.Stop()
	waitForFile(t, flagFile, 2*time.Second)
}

// TestPomodoroFinishEventFailureNotBlocking 验证结束事件命令失败仅记日志，
// 不阻塞阶段推进（F12）。
func TestPomodoroFinishEventFailureNotBlocking(t *testing.T) {
	overrideAskConfirm(t, PromptContinue)
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"pomo": {Kind: config.RulePomodoro, FinishEach: "exit 3"},
		},
	}
	m := NewManager(cfg, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Rule:       "pomo",
		Work:       20 * time.Millisecond,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)

	waitForPomodoroPhase(t, p, phaseShortBreak, 2*time.Second)
	p.Stop()
}

// TestPomodoroWaitingAndNext 验证阶段结束确认被拒绝时进入 waiting（phase 保持
// 已完成阶段、剩余归零、无活动任务），tempo pomodoro next 推进后进入
// pendingPhase 对应阶段并清除 waiting。
func TestPomodoroWaitingAndNext(t *testing.T) {
	overrideAskConfirm(t, PromptPause)
	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       30 * time.Millisecond,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)
	// newPomodoroTracker 只创建 tracker 并注册阶段任务；写入 m.pomodoros 是
	// Manager.PomodoroStart 的职责。测试直接构造 tracker 时需补注册，否则
	// Manager 层的 PomodoroNext/PomodoroStatus 查不到该序列。
	m.mu.Lock()
	m.pomodoros["pomo"] = p
	m.mu.Unlock()

	waitForWaiting(t, p, 2*time.Second)
	st := p.Status()
	if !st.Waiting || st.Phase != phaseWork || st.Completed != 1 || st.Remaining != 0 {
		t.Errorf("waiting 状态 = %+v, want waiting=true phase=work completed=1 remaining=0", st)
	}
	waitForTasks(t, m, 0, time.Second)

	if err := m.PomodoroNext("pomo"); err != nil {
		t.Fatalf("PomodoroNext: %v", err)
	}
	st = p.Status()
	if st.Waiting || st.Phase != phaseShortBreak || st.Completed != 1 {
		t.Errorf("next 后状态 = %+v, want waiting=false phase=short_break completed=1", st)
	}
	waitForTasks(t, m, 1, time.Second)

	p.Stop()
}

// TestPomodoroPromptTimeoutEntersWaiting 验证确认弹窗超时（PromptTimeout）与
// 用户选择暂停同样静默进入 waiting：不自动继续、不发通知，tempo pomo next
// 可照常推进（README「确认弹窗的等待时限」承诺的行为）。
func TestPomodoroPromptTimeoutEntersWaiting(t *testing.T) {
	overrideAskConfirm(t, PromptTimeout)
	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       30 * time.Millisecond,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)
	m.mu.Lock()
	m.pomodoros["pomo"] = p
	m.mu.Unlock()

	waitForWaiting(t, p, 2*time.Second)
	st := p.Status()
	if !st.Waiting || st.Phase != phaseWork || st.Completed != 1 || st.Remaining != 0 {
		t.Errorf("超时后状态 = %+v, want waiting=true phase=work completed=1 remaining=0", st)
	}
	waitForTasks(t, m, 0, time.Second)

	if err := m.PomodoroNext("pomo"); err != nil {
		t.Fatalf("PomodoroNext: %v", err)
	}
	st = p.Status()
	if st.Waiting || st.Phase != phaseShortBreak || st.Completed != 1 {
		t.Errorf("next 后状态 = %+v, want waiting=false phase=short_break completed=1", st)
	}
	waitForTasks(t, m, 1, time.Second)

	p.Stop()
}

// TestPomodoroNextGuarded 验证推进的防御路径：未知名称报错；阶段任务运行中
// Advance 为幂等 no-op。
func TestPomodoroNextGuarded(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.PomodoroNext("ghost"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("推进不存在的番茄钟应报错，得到: %v", err)
	}

	overrideAskConfirm(t, PromptPause)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       time.Hour, // 运行中不会结束，验证幂等
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)
	defer p.Stop()

	if err := p.Advance(); err != nil {
		t.Errorf("阶段任务运行中 Advance 应幂等返回 nil，得到: %v", err)
	}
}

// TestPomodoroPromptStopTerminatesSequence 验证确认弹窗选择停止时：tracker 经
// Manager.PomodoroStop 从注册表移除、阶段任务清理，序列不再推进。
func TestPomodoroPromptStopTerminatesSequence(t *testing.T) {
	overrideAskConfirm(t, PromptStop)
	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Work:       30 * time.Millisecond,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
	}, m)
	m.mu.Lock()
	m.pomodoros["pomo"] = p
	m.mu.Unlock()

	// 停止由确认 goroutine 异步执行，轮询等待注册表移除。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		_, active := m.pomodoros["pomo"]
		m.mu.Unlock()
		if !active {
			waitForTasks(t, m, 0, time.Second)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("确认选择停止后序列应从注册表移除")
}
