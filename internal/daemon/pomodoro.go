package daemon

import (
	"fmt"
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

	phase        string // 当前阶段：phaseWork | phaseShortBreak | phaseLongBreak
	completed    int    // 当前 cycle 内已完成专注数，0..Cycle，long_break 结束后归零
	current      *Task  // 当前阶段任务；阶段切换间隙、待确认或停止后为 nil
	stopped      bool   // 序列是否已被用户终止
	waiting      bool   // 阶段结束待确认：等待用户确认或 tempo pomodoro next 推进
	pendingPhase string // 阶段结束即记录的下一阶段，Advance 时据此创建任务
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
// （F16/F17，后两级由 Config.PomodoroDefaults 统一解析与兜底）；
// FinishOn 空值或非法值一律按缺省 each 处理（F21）。
func (p *PomodoroTracker) fillDefaults() {
	work, short, long, cycle := p.mgr.cfg.PomodoroDefaults()

	p.opts.Work = firstPositive(p.opts.Work, work)
	p.opts.ShortBreak = firstPositive(p.opts.ShortBreak, short)
	p.opts.LongBreak = firstPositive(p.opts.LongBreak, long)
	if p.opts.Cycle <= 0 {
		p.opts.Cycle = cycle
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
	p.startStageLocked(phase, d)
}

// startStageLocked 是 startStage 的持锁版本，供 Advance 等已持有 p.mu 的调用方复用。
// 全程持 p.mu 并在 mgr.mu 内注册：既与 Stop 互斥（防止停止后注册出僵尸任务），
// 也与 Manager.Start 相同地防御极短时长下回调先于注册执行。
func (p *PomodoroTracker) startStageLocked(phase string, d time.Duration) {
	if p.stopped {
		return
	}
	p.phase = phase

	t := newTask(p.name, p.opts.Rule, "", d, p.onStageComplete)
	p.mgr.mu.Lock()
	p.mgr.tasks[p.name] = t
	p.mgr.mu.Unlock()

	p.current = t
}

// removeCurrentTask 将已结束的阶段任务从 Manager.tasks 删除。阶段结束的用户提醒
// 由确认弹窗（askConfirm）承担，不再走 Manager.onTaskFinish 的默认通知路径。
func (p *PomodoroTracker) removeCurrentTask() {
	p.mgr.mu.Lock()
	delete(p.mgr.tasks, p.name)
	p.mgr.mu.Unlock()
}

// onStageComplete 是阶段任务正常结束的回调：从 Manager.tasks 移除已完成的阶段
// 任务，按状态机计算下一阶段记入 pendingPhase，然后在独立 goroutine 中弹出
// 确认框（F15、4.5 节）；用户确认后推进到下一阶段，拒绝或无法弹窗则进入
// waiting，等待 tempo pomodoro next 手动推进。finish_command 仍按 FinishOn
// 语义在阶段结束时调度，与确认流程互不影响。
//
// 本回调由 time.AfterFunc 触发，必须立即返回：确认弹窗最长阻塞 promptTimeout，
// 因此放入独立 goroutine 执行，不占用定时器回调线程。
//
// completed（当前 cycle 内已完成专注数，0..Cycle）在各分支的变化：
//   - work 正常结束：completed++，completed%Cycle==0 时下一阶段为长休，否则短休；
//   - long_break 正常结束：completed 归零，开启新 cycle，下一阶段为 work；
//   - short_break 正常结束：不变，下一阶段为 work。
// 暂停/继续只作用于当前阶段任务，不会触发本回调，completed 不受影响（F20）。
func (p *PomodoroTracker) onStageComplete(t *Task) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	from := p.phase
	switch from {
	case phaseWork:
		p.completed++
	case phaseLongBreak:
		p.completed = 0
	}
	completed := p.completed
	next := p.nextStageLocked(from)
	p.pendingPhase = next
	p.current = nil
	// 持锁读取注入点，与测试恢复 askConfirmFn 之间经 p.mu 形成 happens-before。
	confirm := askConfirmFn
	p.mu.Unlock()

	// 旧阶段任务收尾：先 Stop 兜底确保计时器不可能再次触发（fire 已触发过
	// onFinish，由此保证每个 Task 的 onFinish 至多执行一次），再从 Manager.tasks
	// 删除；下一阶段任务在确认后由 Advance 创建。
	t.Stop()
	p.removeCurrentTask()

	p.mgr.logger.Info("番茄钟阶段结束",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", from,
		"pending", next,
		"completed", completed,
		"cycle", p.opts.Cycle,
		"action", "pomodoro_stage_end")

	if p.opts.FinishOn == config.FinishOnEach {
		// 异步执行，避免自定义命令阻塞回调（F12）。
		go p.runFinishCommand(from)
	}

	// 确认弹窗可能阻塞至用户响应（上限 promptTimeout），放入独立 goroutine，
	// 不阻塞 time.AfterFunc 的回调线程；结果由 settleConfirm 统一结算。
	go func() {
		p.settleConfirm(confirm(p.mgr.logger, confirmTitle, confirmMessage))
	}()
}

// settleConfirm 结算阶段结束确认弹窗的结果：序列已停止或已被 tempo pomodoro next
// 手动推进（current != nil）时忽略迟到结果；确认时推进到 pendingPhase 对应阶段，
// 拒绝或无法弹窗时置 waiting，等待手动推进。
func (p *PomodoroTracker) settleConfirm(ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.current != nil {
		return
	}
	if ok {
		if err := p.advanceLocked(); err != nil {
			p.mgr.logger.Error("推进番茄钟阶段失败", "task", p.name, "action", "pomodoro_advance", "error", err)
		}
		return
	}
	p.waiting = true
	p.mgr.logger.Info("番茄钟等待确认推进",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", p.phase,
		"pending", p.pendingPhase,
		"action", "pomodoro_waiting")
}

// nextStageLocked 按 4.5 节状态机返回当前阶段结束后的下一阶段：专注完成后，
// 当前 cycle 内已完成专注数对 Cycle 取模为 0（即达到 Cycle）时进入长休，否则
// 进入短休；休息完成后进入下一个专注。调用方须持有 p.mu，且 completed 已按
// 本阶段结果更新（0..Cycle，该不变式下取模判断与 == Cycle 等价）。
func (p *PomodoroTracker) nextStageLocked(from string) string {
	if from != phaseWork {
		return phaseWork
	}
	if p.completed%p.opts.Cycle == 0 {
		return phaseLongBreak
	}
	return phaseShortBreak
}

// stageDuration 返回指定阶段的时长；未知阶段按 work 处理（防御 pendingPhase 异常值）。
func (p *PomodoroTracker) stageDuration(phase string) time.Duration {
	switch phase {
	case phaseShortBreak:
		return p.opts.ShortBreak
	case phaseLongBreak:
		return p.opts.LongBreak
	default:
		return p.opts.Work
	}
}

// Advance 推进到待确认的下一阶段（tempo pomodoro next）：处于 waiting 或确认
// 弹窗尚未返回时，根据 pendingPhase 创建下一阶段任务；当前阶段任务仍在运行时
// 不做任何事（幂等）。序列已停止或无待推进阶段时返回错误。
func (p *PomodoroTracker) Advance() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return fmt.Errorf("番茄钟 %s 已停止", p.name)
	}
	if p.current != nil {
		// 当前阶段任务运行中，无需推进（幂等）。
		return nil
	}
	return p.advanceLocked()
}

// advanceLocked 在持锁状态下根据 pendingPhase 启动下一阶段任务并记录阶段切换。
// 调用方须持有 p.mu，且已确认 stopped 为假、当前无活动任务。
func (p *PomodoroTracker) advanceLocked() error {
	if p.pendingPhase == "" {
		return fmt.Errorf("番茄钟 %s 没有待推进的阶段", p.name)
	}
	next := p.pendingPhase
	prev := p.phase
	p.waiting = false
	p.pendingPhase = ""
	p.startStageLocked(next, p.stageDuration(next))

	p.mgr.logger.Info("番茄钟阶段切换",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", prev+"->"+next,
		"completed", p.completed,
		"cycle", p.opts.Cycle,
		"action", "pomodoro_transition")
	return nil
}

// Status 返回当前阶段与周期进度（F18）：phase 为当前阶段名，completed 为当前
// cycle 内已完成的专注数（0..Cycle，长休结束后归零），cycle 为一个长休周期包含
// 的专注数，remaining 为当前阶段任务剩余时长；waiting 表示阶段结束待确认，
// 需 tempo pomodoro next 推进。
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
		Waiting:   p.waiting,
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
	// waiting 状态下无 current 可停，仅复位待确认标记，清理路径与运行态一致。
	p.waiting = false
	p.pendingPhase = ""
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
