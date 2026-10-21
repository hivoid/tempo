package daemon

import (
	"os/exec"
	"sync"
	"time"

	"tempo/internal/config"
	"tempo/internal/protocol"
)

// 番茄钟阶段名称，取值与 protocol.PomodoroInfo.Phase 的约定一致。
const (
	phaseWork       = "work"
	phaseShortBreak = "short_break"
	phaseLongBreak  = "long_break"
)

// PomodoroTracker 驱动一个番茄钟序列：按各阶段时长在专注/短休/长休间自动切换
// （PRD F15–F21、4.5 节状态机）。序列以 name 标识，当前阶段任务与序列同名注册在
// Manager 的任务表中，任务级 pause/resume/stop 因此可直接作用于当前阶段（F20）。
type PomodoroTracker struct {
	mu sync.Mutex

	name string          // 序列名称，同时用作各阶段任务名
	opts PomodoroOptions // 序列参数，构造时补全默认值后不再变更
	mgr  *Manager        // 所属管理器，负责任务注册、日志与默认提醒

	phase     string // 当前阶段：phaseWork | phaseShortBreak | phaseLongBreak
	completed int    // 已正常完成的专注阶段数
	current   *Task  // 当前阶段任务；阶段切换间隙或停止后为 nil
	stopped   bool   // 序列是否已被用户终止
}

// newPomodoroTracker 创建番茄钟序列并立即启动第一个 work 阶段任务（F15）。
func newPomodoroTracker(name string, opts PomodoroOptions, mgr *Manager) *PomodoroTracker {
	p := &PomodoroTracker{
		name:  name,
		opts:  opts,
		mgr:   mgr,
		phase: phaseWork,
	}
	p.fillDefaults()
	p.startStage(phaseWork, p.opts.Work)
	return p
}

// fillDefaults 补全序列参数，优先级：显式传入 > 全局 [pomodoro] 配置 > PRD 默认值
// （F16/F17）；FinishOn 空值或非法值一律按缺省 each 处理（F21）。
func (p *PomodoroTracker) fillDefaults() {
	def := config.DefaultPomodoro()
	global := p.mgr.cfg.Pomodoro

	p.opts.Work = firstPositive(p.opts.Work, global.Work, def.Work)
	p.opts.ShortBreak = firstPositive(p.opts.ShortBreak, global.ShortBreak, def.ShortBreak)
	p.opts.LongBreak = firstPositive(p.opts.LongBreak, global.LongBreak, def.LongBreak)
	if p.opts.Cycle <= 0 {
		p.opts.Cycle = global.Cycle
	}
	if p.opts.Cycle <= 0 {
		p.opts.Cycle = def.Cycle
	}
	if p.opts.FinishOn != config.FinishOnSequence {
		p.opts.FinishOn = config.FinishOnEach
	}
}

// firstPositive 返回第一个大于 0 的时长，均为 0 时返回 0。
func firstPositive(ds ...time.Duration) time.Duration {
	for _, d := range ds {
		if d > 0 {
			return d
		}
	}
	return 0
}

// startStage 创建并注册一个阶段任务，其 onFinish 指向 onStageComplete，
// 阶段正常结束时据此推进序列。不经过 Manager.Start：它将 onFinish 固定绑定到
// Manager 的默认结束处理，无法注入驱动序列所需的回调。
func (p *PomodoroTracker) startStage(phase string, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.phase = phase

	// 全程持 p.mu 并在 mgr.mu 内注册：既与 Stop 互斥（防止停止后注册出
	// 僵尸任务），也与 Manager.Start 相同地防御极短时长下回调先于注册执行。
	t := newTask(p.name, p.opts.Rule, "", d, p.onStageComplete)
	p.mgr.mu.Lock()
	p.mgr.tasks[p.name] = t
	p.mgr.mu.Unlock()

	p.current = t
}

// onStageComplete 是阶段任务正常结束的回调：先复用 Manager 的默认结束处理
// （移除任务、记录日志、发送平台默认提醒），再按状态机推进到下一阶段并自动创建
// 下一阶段任务（F15、4.5 节）。finish_command 是默认提醒之外的附加动作，
// 由本方法按 FinishOn 语义调度，两者互不影响。
func (p *PomodoroTracker) onStageComplete(t *Task) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	from := p.phase
	if from == phaseWork {
		p.completed++
	}
	completed := p.completed
	next, d := p.nextStageLocked(from)
	p.phase = next
	p.current = nil
	p.mu.Unlock()

	p.mgr.onTaskFinish(t)

	p.mgr.logger.Info("番茄钟阶段切换",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", from+"->"+next,
		"completed", completed,
		"cycle", p.opts.Cycle,
		"action", "pomodoro_transition")

	if p.opts.FinishOn == config.FinishOnEach {
		// 异步执行，避免自定义命令阻塞阶段切换与 daemon（F12）。
		go p.runFinishCommand(from)
	}

	p.startStage(next, d)
}

// nextStageLocked 按 4.5 节状态机返回下一阶段及其时长：专注完成后，已完成专注数
// 恰为 Cycle 的整数倍时进入长休，否则进入短休；休息完成后进入下一个专注。
// 调用方须持有 p.mu。
func (p *PomodoroTracker) nextStageLocked(from string) (string, time.Duration) {
	if from != phaseWork {
		return phaseWork, p.opts.Work
	}
	if p.completed%p.opts.Cycle == 0 {
		return phaseLongBreak, p.opts.LongBreak
	}
	return phaseShortBreak, p.opts.ShortBreak
}

// Status 返回当前阶段与周期进度（F18）：phase 为当前阶段名，completed 为已完成
// 专注数，cycle 为一个长休周期包含的专注数，remaining 为当前阶段任务剩余时长。
func (p *PomodoroTracker) Status() protocol.PomodoroInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	remaining := "0s"
	if p.current != nil {
		remaining = formatRemaining(p.current.Remaining())
	}
	return protocol.PomodoroInfo{
		Phase:     p.phase,
		Completed: p.completed,
		Cycle:     p.opts.Cycle,
		Remaining: remaining,
	}
}

// Stop 终止整个序列（F19）：停止并移除当前阶段任务，序列不再推进；
// FinishOn 为 sequence 时执行一次规则中的 finish_command。
func (p *PomodoroTracker) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	current := p.current
	phase := p.phase
	completed := p.completed
	p.current = nil
	p.mu.Unlock()

	// 移除当前阶段任务，避免序列终止后残留于任务列表。
	p.mgr.mu.Lock()
	delete(p.mgr.tasks, p.name)
	p.mgr.mu.Unlock()

	if current != nil {
		current.Stop()
	}

	p.mgr.logger.Info("番茄钟序列已停止",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", phase,
		"completed", completed,
		"cycle", p.opts.Cycle,
		"action", "pomodoro_stop")

	if p.opts.FinishOn == config.FinishOnSequence {
		go p.runFinishCommand(phase)
	}
}

// runFinishCommand 执行规则配置的自定义结束命令（F10）：执行失败仅记日志，
// 不 panic、不阻塞（F12）；无规则或未配置 finish_command 时空操作。
// 命令经 sh -c 执行（CONTEXT.md §5.4 Linux 约定），Windows cmd /c 待平台层接入后统一处理。
func (p *PomodoroTracker) runFinishCommand(phase string) {
	cmdText := p.finishCommand()
	if cmdText == "" {
		return
	}
	if _, err := exec.Command("sh", "-c", cmdText).CombinedOutput(); err != nil {
		p.mgr.logger.Error("执行自定义结束命令失败",
			"task", p.name,
			"rule", p.opts.Rule,
			"phase", phase,
			"action", "finish_command",
			"error", err)
		return
	}
	p.mgr.logger.Info("自定义结束命令已执行",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", phase,
		"action", "finish_command")
}

// finishCommand 返回序列关联规则配置的 finish_command；无规则或未配置时返回空串。
// cfg 在 daemon 启动加载后只读，无需加锁访问。
func (p *PomodoroTracker) finishCommand() string {
	if p.opts.Rule == "" {
		return ""
	}
	if r, ok := p.mgr.cfg.Rules[p.opts.Rule]; ok {
		return r.FinishCommand
	}
	return ""
}
