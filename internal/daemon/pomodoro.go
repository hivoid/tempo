package daemon

import (
	"fmt"
	"sync"
	"time"

	"tempo/internal/config"
)

// 番茄钟阶段名称，取值与 protocol.PomodoroInfo.Phase 的约定一致。
const (
	phaseWork       = "work"
	phaseShortBreak = "short_break"
	phaseLongBreak  = "long_break"
)

// 结束事件名称，用于日志 event 字段：each 每阶段结束、sequence 整个序列结束。
const (
	finishEventEach     = "each"
	finishEventSequence = "sequence"
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
	waiting      bool   // 阶段结束被暂停：等待 tempo pomo next 手动推进
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

// fillDefaults 补全序列参数，回退优先级：显式传入（CLI）> 规则档位 > 全局
// [pomodoro] 配置 > PRD 默认值（后两级由 Config.PomodoroDefaults 统一解析与
// 兜底）；结束事件命令为空表示不执行（无默认动作），CLI 未传时回退规则档位。
func (p *PomodoroTracker) fillDefaults() {
	work, short, long, cycle := p.mgr.cfg.PomodoroDefaults()

	// 调用前 Manager.PomodoroStart 已校验规则存在且为番茄钟类型；此处对规则
	// 缺失或类型不符按无规则档位处理，保持构造路径健壮。
	var rWork, rShort, rLong time.Duration
	var rCycle int
	if r, ok := p.mgr.cfg.Rules[p.opts.Rule]; ok && r.Kind == config.RulePomodoro {
		rWork, rShort, rLong, rCycle = r.Work, r.ShortBreak, r.LongBreak, r.Cycle
		if p.opts.FinishEach == "" {
			p.opts.FinishEach = r.FinishEach
		}
		if p.opts.FinishSequence == "" {
			p.opts.FinishSequence = r.FinishSequence
		}
	}

	p.opts.Work = firstPositive(p.opts.Work, rWork, work)
	p.opts.ShortBreak = firstPositive(p.opts.ShortBreak, rShort, short)
	p.opts.LongBreak = firstPositive(p.opts.LongBreak, rLong, long)
	switch {
	case p.opts.Cycle > 0:
		cycle = p.opts.Cycle
	case rCycle > 0:
		cycle = rCycle
	}
	p.opts.Cycle = cycle
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
// 全程持 p.mu 并经 Manager.registerTask 注册（允许的 p.mu→m.mu 锁序）：既与
// Stop 互斥（防止停止后注册出僵尸任务），也与 Manager.Start 相同地防御极短
// 时长下回调先于注册执行。
func (p *PomodoroTracker) startStageLocked(phase string, d time.Duration) {
	if p.stopped {
		return
	}
	p.phase = phase

	t := newTask(p.name, p.opts.Rule, "", d, p.onStageComplete)
	p.mgr.registerTask(t)

	p.current = t
}

// removeCurrentTask 经 Manager.unregisterTask 将当前阶段同名任务从注册表删除。
// 阶段结束的用户提醒由确认弹窗（askConfirm）承担，不再走 Manager.onTaskFinish
// 的默认通知路径。可在持有 p.mu 时调用（构成允许的 p.mu→m.mu 锁序）。
func (p *PomodoroTracker) removeCurrentTask() {
	p.mgr.unregisterTask(p.name)
}

// onStageComplete 是阶段任务正常结束的回调：从 Manager.tasks 移除已完成的阶段
// 任务，按状态机计算下一阶段记入 pendingPhase，然后在独立 goroutine 中弹出
// 三选确认框（F15、4.5 节）；继续→推进到下一阶段，停止→终止整个序列，
// 暂停、确认超时未响应（或无法弹窗）→静默进入 waiting，等待 tempo pomo next
// 手动推进，超时不发送通知、不自动继续。
// finish_each 结束事件在本回调中异步调度，与确认流程互不影响。
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
	// 旧阶段任务收尾须在本临界区内完成（锁序 p.mu→t.mu、p.mu→m.mu 均为允许
	// 方向）：先 Stop 兜底确保计时器不可能再次触发（fire 已触发过 onFinish，
	// 由此保证每个 Task 的 onFinish 至多执行一次），再从注册表删除。若推迟到
	// 解锁之后，并发的 Advance 可能已在间隙注册同名新阶段任务而被此处按名误删。
	t.Stop()
	p.removeCurrentTask()
	p.mu.Unlock()

	p.mgr.logger.Info("番茄钟阶段结束",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", from,
		"pending", next,
		"completed", completed,
		"cycle", p.opts.Cycle,
		"action", "pomodoro_stage_end")

	// 每阶段结束事件：异步执行，避免命令阻塞回调（F12）；命令为空时不执行。
	go p.runFinishEvent(from, finishEventEach, p.opts.FinishEach)

	title, message, fallback := buildPromptText(from, next, p.opts)

	// 确认弹窗可能阻塞至用户响应（上限 promptTimeout），放入独立 goroutine，
	// 不阻塞 time.AfterFunc 的回调线程；各态结果分别驱动推进/停止/等待。
	go func() {
		result := confirm(p.mgr.logger, title, message, fallback)
		switch result {
		case PromptContinue:
			if err := p.Advance(); err != nil {
				p.mgr.logger.Error("推进番茄钟阶段失败",
					"task", p.name,
					"rule", p.opts.Rule,
					"action", "pomodoro_advance",
					"error", err)
			}
		case PromptStop:
			// 弹窗中选择停止：经 Manager 从注册表移除并终止整个序列。
			p.requestStop()
		default:
			// PromptPause、PromptTimeout（弹窗超时未响应）及异常值：静默进入
			// waiting，等待 tempo pomo next 手动推进；超时不发通知。
			p.setWaiting()
		}
	}()
}

// setWaiting 将序列置为 waiting：等待用户执行 tempo pomo next 手动推进。
// 序列已停止、或结果已过期（弹窗打开期间序列已被 next 推进，current != nil）
// 时忽略本结果，防止覆盖新状态。
func (p *PomodoroTracker) setWaiting() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.current != nil {
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

// requestStop 处理确认弹窗的停止选择：经 Manager 的 PomodoroStop 从注册表
// 移除并终止整个序列。须在未持有 p.mu 的上下文调用（确认 goroutine 满足）；
// PomodoroStop 按短暂持 m.mu 查删注册表、解锁后再调用 tracker.Stop 的顺序
// 加锁，与 Manager.Stop 相同，不会形成锁序死锁。
func (p *PomodoroTracker) requestStop() {
	if err := p.mgr.PomodoroStop(p.name); err != nil {
		p.mgr.logger.Error("停止番茄钟序列失败",
			"task", p.name,
			"rule", p.opts.Rule,
			"action", "pomodoro_stop",
			"error", err)
	}
}

// nextStageLocked 按 4.5 节状态机返回当前阶段结束后的下一阶段：专注完成后，
// 当前 cycle 内已完成专注数达到 Cycle 时进入长休，否则进入短休；休息完成后
// 进入下一个专注。调用方须持有 p.mu，且 completed 已按本阶段结果更新
// （0..Cycle）。用 >= 而非取模判断：消除 Cycle 异常为 0 时的除零风险，
// completed 越过不变式上界时也能正确落到长休。
func (p *PomodoroTracker) nextStageLocked(from string) string {
	if from != phaseWork {
		return phaseWork
	}
	if p.completed >= p.opts.Cycle {
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

// formatStageDuration 格式化阶段时长用于提示文案：整分钟输出「N 分钟」，
// 否则使用 time.Duration.String()（如 "5s"、"1m30s"）。
func formatStageDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		return fmt.Sprintf("%d 分钟", int(d/time.Minute))
	}
	return d.String()
}

// buildPromptText 按「当前阶段 → 下一阶段」组合生成确认弹窗的标题、交互正文
// 与回退通知文案；时长与周期数取自 opts。未知组合回退到通用文案。
func buildPromptText(curPhase, nextPhase string, opts PomodoroOptions) (title, message, fallback string) {
	work := formatStageDuration(opts.Work)
	shortBreak := formatStageDuration(opts.ShortBreak)
	longBreak := formatStageDuration(opts.LongBreak)

	switch {
	case curPhase == phaseWork && nextPhase == phaseShortBreak:
		return "专注结束",
			fmt.Sprintf("专注阶段结束，进入 %s 短休？", shortBreak),
			fmt.Sprintf("专注结束，未检测到交互工具，已自动进入 %s 短休。", shortBreak)
	case curPhase == phaseWork && nextPhase == phaseLongBreak:
		return "专注结束",
			fmt.Sprintf("已完成 %d 个专注周期，进入 %s 长休？", opts.Cycle, longBreak),
			fmt.Sprintf("已完成 %d 个专注周期，未检测到交互工具，已自动进入 %s 长休。", opts.Cycle, longBreak)
	case curPhase == phaseShortBreak && nextPhase == phaseWork:
		return "短休结束",
			fmt.Sprintf("短休结束，开始下一个 %s 专注？", work),
			fmt.Sprintf("短休结束，未检测到交互工具，已自动开始下一个 %s 专注。", work)
	case curPhase == phaseLongBreak && nextPhase == phaseWork:
		return "长休结束",
			"长休结束，开始新的专注周期？",
			"长休结束，未检测到交互工具，已自动开始新的专注周期。"
	default:
		return "阶段结束",
			"阶段结束，是否开始下一阶段？",
			"阶段结束，未检测到交互工具，已自动进入下一阶段。"
	}
}

// Advance 推进到待确认的下一阶段（tempo pomo next）：处于 waiting 或确认
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

// PomodoroState 是番茄钟序列的领域状态快照（F18），与 IPC 协议结构
// protocol.PomodoroInfo 解耦：Remaining 保留原始时长，人类可读格式化由 IPC 层
// （server.go）集中完成。
type PomodoroState struct {
	Name      string        // 序列名称，同时是当前阶段任务名（与 tempo list 中任务名一致）
	Phase     string        // 当前阶段：phaseWork | phaseShortBreak | phaseLongBreak
	Completed int           // 当前 cycle 内已完成的专注数，0..Cycle，长休结束后归零
	Cycle     int           // 一个长休周期包含的专注数
	Remaining time.Duration // 当前阶段任务剩余时长，无活动任务时为 0
	Waiting   bool          // 阶段结束待确认，等待 tempo pomo next 推进
}

// Status 返回当前阶段与周期进度的领域状态（F18），字段语义见 PomodoroState。
func (p *PomodoroTracker) Status() PomodoroState {
	p.mu.Lock()
	defer p.mu.Unlock()

	var remaining time.Duration
	if p.current != nil {
		remaining = p.current.Remaining()
	}
	return PomodoroState{
		Name:      p.name,
		Phase:     p.phase,
		Completed: p.completed,
		Cycle:     p.opts.Cycle,
		Remaining: remaining,
		Waiting:   p.waiting,
	}
}

// Stop 终止整个序列（F19）：停止并移除当前阶段任务，序列不再推进；
// 配置了 finish_sequence 结束事件时在此执行一次。
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
	p.removeCurrentTask()

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

	// 整个序列结束事件：序列仅经 Stop 终止，故在此执行一次；命令为空不执行。
	go p.runFinishEvent(phase, finishEventSequence, p.opts.FinishSequence)
}

// runFinishEvent 执行序列的结束事件命令（finish_each 每阶段结束 / finish_sequence
// 整个序列结束）：执行失败仅记日志，不 panic、不阻塞（F12）；命令为空时不执行
// ——结束事件不同于通知，没有默认动作。命令取自 opts（构造时已按规则档位补全）。
// shell 选择由平台文件决定：Linux sh -c（CONTEXT.md §5.4）/ Windows cmd /c。
func (p *PomodoroTracker) runFinishEvent(phase, event, cmdText string) {
	if cmdText == "" {
		return
	}
	if _, err := runFinishShellCommand(cmdText); err != nil {
		p.mgr.logger.Error("执行结束事件命令失败",
			"task", p.name,
			"rule", p.opts.Rule,
			"phase", phase,
			"event", event,
			"action", "finish_event",
			"error", err)
		return
	}
	p.mgr.logger.Info("结束事件命令已执行",
		"task", p.name,
		"rule", p.opts.Rule,
		"phase", phase,
		"event", event,
		"action", "finish_event")
}
