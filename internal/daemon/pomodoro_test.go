package daemon

import (
	"os"
	"path/filepath"
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

// waitForFile 轮询等待文件出现，用于观察异步执行的 finish_command 副作用。
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
	if st.Remaining == "" || st.Remaining == "0s" {
		t.Errorf("初始剩余时长异常: %q", st.Remaining)
	}
	waitForTasks(t, m, 1, time.Second)
	if tasks := m.List(); tasks[0].Name != "pomo" || tasks[0].State != "running" {
		t.Errorf("应以序列名注册运行中的阶段任务: %+v", tasks)
	}
	p.Stop()
}

// TestPomodoroFillDefaults 验证参数补全优先级：显式传入 > 全局配置 > PRD 默认值。
func TestPomodoroFillDefaults(t *testing.T) {
	def := config.DefaultPomodoro()

	m := NewManager(nil, nil, nil)
	p := newPomodoroTracker("p1", PomodoroOptions{}, m)
	if p.opts.Work != def.Work || p.opts.ShortBreak != def.ShortBreak ||
		p.opts.LongBreak != def.LongBreak || p.opts.Cycle != def.Cycle {
		t.Errorf("空参数应回退 PRD 默认值: %+v", p.opts)
	}
	if p.opts.FinishOn != config.FinishOnEach {
		t.Errorf("FinishOn 缺省应为 %q, got %q", config.FinishOnEach, p.opts.FinishOn)
	}
	p.Stop()

	cfg := &config.Config{
		Pomodoro: config.Pomodoro{
			Work: time.Minute, ShortBreak: 2 * time.Minute, LongBreak: 3 * time.Minute, Cycle: 3,
		},
	}
	m2 := NewManager(cfg, nil, nil)
	p2 := newPomodoroTracker("p2", PomodoroOptions{}, m2)
	if p2.opts.Work != time.Minute || p2.opts.ShortBreak != 2*time.Minute ||
		p2.opts.LongBreak != 3*time.Minute || p2.opts.Cycle != 3 {
		t.Errorf("应回退全局 [pomodoro] 配置: %+v", p2.opts)
	}
	p2.Stop()
}

// TestPomodoroWorkToShortBreak 验证专注正常完成后进入短休、completed 递增，
// 且旧阶段任务被同名新任务替换。
func TestPomodoroWorkToShortBreak(t *testing.T) {
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
	if st.Remaining == "" || st.Remaining == "0s" {
		t.Errorf("短休应已开始计时: %q", st.Remaining)
	}
	if tasks := m.List(); tasks[0].Name != "pomo" {
		t.Errorf("阶段任务应以序列名注册: %+v", tasks)
	}
	p.Stop()
}

// TestPomodoroLongBreakAfterCycle 验证每完成 Cycle 个专注后进入长休（验收 7）。
func TestPomodoroLongBreakAfterCycle(t *testing.T) {
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

// TestPomodoroEachFinishCommandRunsPerStage 验证 each 模式下每阶段结束执行
// 规则的 finish_command。
func TestPomodoroEachFinishCommandRunsPerStage(t *testing.T) {
	flagFile := filepath.Join(t.TempDir(), "stage-done")
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"pomo": {Duration: time.Second, FinishCommand: "touch " + flagFile},
		},
	}
	m := NewManager(cfg, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Rule:       "pomo",
		Work:       30 * time.Millisecond,
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
		FinishOn:   config.FinishOnEach,
	}, m)

	waitForFile(t, flagFile, 2*time.Second)
	p.Stop()
}

// TestPomodoroSequenceFinishCommandOnStop 验证 sequence 模式仅在用户停止序列时
// 执行一次 finish_command，且停止后不残留阶段任务。
func TestPomodoroSequenceFinishCommandOnStop(t *testing.T) {
	flagFile := filepath.Join(t.TempDir(), "sequence-done")
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"pomo": {Duration: time.Second, FinishCommand: "touch " + flagFile},
		},
	}
	m := NewManager(cfg, nil, nil)
	p := newPomodoroTracker("pomo", PomodoroOptions{
		Rule:       "pomo",
		Work:       time.Hour, // 阶段远未结束，命令只能来自 Stop
		ShortBreak: time.Second,
		LongBreak:  time.Second,
		Cycle:      4,
		FinishOn:   config.FinishOnSequence,
	}, m)

	p.Stop()
	waitForFile(t, flagFile, 2*time.Second)
	if tasks := m.List(); len(tasks) != 0 {
		t.Errorf("停止后应无阶段任务: %+v", tasks)
	}
}

// TestPomodoroFinishCommandFailureNotBlocking 验证 finish_command 失败仅记日志，
// 不阻塞阶段推进（F12）；同时覆盖 FinishOn 缺省为 each。
func TestPomodoroFinishCommandFailureNotBlocking(t *testing.T) {
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"pomo": {FinishCommand: "exit 3"},
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
