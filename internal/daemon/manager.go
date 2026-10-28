package daemon

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"tempo/internal/config"
	"tempo/internal/notify"
	"tempo/internal/protocol"
)

// Manager 管理所有活动计时任务，所有方法并发安全。
//
// 锁顺序约定：嵌套加锁只允许 p.mu（PomodoroTracker）→ m.mu 方向（阶段任务经
// registerTask 注册、阶段收尾经 unregisterTask 注销）；禁止在持有 m.mu 时获取
// 任何 Task/PomodoroTracker 的锁，因此对二者的操作一律遵循「持 m.mu 查删注册表
// → 解锁 → 再调用对象方法」的模式（见 Stop、PomodoroStop、PomodoroStatus、
// PomodoroNext）。
type Manager struct {
	mu sync.Mutex

	cfg        *config.Config
	notifier   notify.Notifier
	logger     *slog.Logger
	tasks      map[string]*Task
	pomodoros  map[string]*PomodoroTracker
}

// NewManager 创建任务管理器。cfg、notifier、logger 由调用方注入：
// cfg 为 nil 时按空配置处理；notifier 为 nil 时任务结束提醒无纯通知兜底；
// logger 为 nil 时日志写入被丢弃（仅供测试兜底，生产环境应注入有效 logger）。
func NewManager(cfg *config.Config, notifier notify.Notifier, logger *slog.Logger) *Manager {
	if cfg == nil {
		cfg = &config.Config{}
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{
		cfg:       cfg,
		notifier:  notifier,
		logger:    logger,
		tasks:     make(map[string]*Task),
		pomodoros: make(map[string]*PomodoroTracker),
	}
}

// Start 创建并启动一个计时任务。
// duration 为空时回退到规则 rule 的时长，两者均为空则报错；
// finishNotify（结束提醒）优先级：显式传入 > 规则 finish_notify > 平台默认通知
// （PRD F11）；finishEvent（结束事件）优先级：显式传入 > 规则 finish，为空不执行。
func (m *Manager) Start(name, rule, duration, finishNotify, finishEvent string) error {
	if name == "" {
		return errors.New("任务名称不能为空")
	}

	var d time.Duration
	if duration != "" {
		parsed, err := time.ParseDuration(duration)
		if err != nil {
			return fmt.Errorf("任务 %s: 解析时长 %q: %w", name, duration, err)
		}
		d = parsed
	}

	var ruleFinishNotify, ruleFinishEvent string
	if rule != "" {
		r, ok := m.cfg.Rules[rule]
		if !ok {
			return fmt.Errorf("任务 %s: 规则 %q 未定义", name, rule)
		}
		if r.Kind == config.RulePomodoro {
			return fmt.Errorf("任务 %s: 规则 %q 是番茄钟规则，不能用于计时任务", name, rule)
		}
		if d == 0 {
			d = r.Duration
		}
		ruleFinishNotify = r.FinishNotify
		ruleFinishEvent = r.Finish
	}
	if d <= 0 {
		return fmt.Errorf("任务 %s: 必须提供有效时长或含有效时长的规则", name)
	}
	if finishNotify == "" {
		finishNotify = ruleFinishNotify
	}
	if finishEvent == "" {
		finishEvent = ruleFinishEvent
	}

	m.mu.Lock()
	if _, exists := m.tasks[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("任务 %s 已存在", name)
	}
	// 在持锁状态下创建并注册：若时长极短导致计时器立即到点，
	// onTaskFinish 须等锁释放后才能执行，避免插入一个已结束的任务。
	t := newTask(name, rule, finishNotify, finishEvent, d, m.onTaskFinish)
	m.tasks[name] = t
	m.mu.Unlock()

	m.logger.Info("任务已创建", "task", name, "rule", rule, "duration", d,
		"finish_notify", finishNotify, "finish_event", finishEvent, "action", "start")
	return nil
}

// onTaskFinish 是任务计时到点的结束回调：从注册表移除任务并处理结束动作，
// 由 Task.fire 在锁外调用。两条路径互不影响：结束事件（finishEvent）配置了
// 命令时异步执行一次（无默认动作，空则跳过）；结束提醒（finishNotify）配置了
// 命令时仅执行该命令（Linux sh -c / Windows cmd /c，与番茄钟结束事件共用
// runFinishShellCommand），否则弹出与番茄钟阶段结束一致的交互提醒组件
//（按钮仅一个「知道了」），无可用交互组件时回退 notifier 纯通知。
func (m *Manager) onTaskFinish(t *Task) {
	m.mu.Lock()
	delete(m.tasks, t.name)
	m.mu.Unlock()

	m.logger.Info("任务计时结束", "task", t.name, "rule", t.rule, "trigger", "timer", "action", "finish")

	if t.finishEvent != "" {
		// 结束事件异步执行，不阻塞提醒路径；失败仅记日志（F12）。
		go func() {
			if _, err := runFinishShellCommand(t.finishEvent); err != nil {
				m.logger.Error("执行结束事件命令失败", "task", t.name, "event", "finish", "action", "finish_event", "error", err)
				return
			}
			m.logger.Info("结束事件命令已执行", "task", t.name, "event", "finish", "action", "finish_event")
		}()
	}

	if t.finishNotify != "" {
		if _, err := runFinishShellCommand(t.finishNotify); err != nil {
			m.logger.Error("执行自定义结束提醒命令失败", "task", t.name, "error", err)
		}
		return
	}
	m.alertTaskDone(t.name)
}

// alertTaskDone 弹出任务结束的默认提醒（PRD F11 平台默认提醒）：使用与番茄
// 钟阶段结束一致的交互组件、按钮仅一个「知道了」，阻塞至用户确认或超时；
// 无可用交互组件时回退 notifier 纯通知，二者皆不可用时仅记日志。
func (m *Manager) alertTaskDone(name string) {
	message := fmt.Sprintf("任务 %s 计时结束", name)
	if notifyDoneFn(m.logger, "tempo", message) {
		m.logger.Info("结束提醒已确认", "task", name)
		return
	}
	if m.notifier == nil {
		m.logger.Warn("无可用提醒方式", "task", name)
		return
	}
	if err := m.notifier.Notify("tempo", message, 10*time.Second); err != nil {
		m.logger.Error("发送通知失败", "task", name, "error", err)
		return
	}
	m.logger.Info("通知已发送", "task", name)
}

// List 返回所有活动任务的状态，按名称排序。
func (m *Manager) List() []protocol.TaskInfo {
	m.mu.Lock()
	tasks := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		tasks = append(tasks, t)
	}
	m.mu.Unlock()

	sort.Slice(tasks, func(i, j int) bool { return tasks[i].name < tasks[j].name })

	infos := make([]protocol.TaskInfo, 0, len(tasks))
	for _, t := range tasks {
		infos = append(infos, protocol.TaskInfo{
			Name:      t.name,
			State:     t.State().String(),
			Remaining: formatRemaining(t.Remaining()),
			Rule:      t.rule,
		})
	}
	return infos
}

// Pause 按名称暂停任务，保留剩余时长（PRD F4）。
func (m *Manager) Pause(name string) error {
	t, ok := m.lookup(name)
	if !ok {
		return fmt.Errorf("任务 %s 不存在", name)
	}
	t.Pause()
	m.logger.Info("任务已暂停", "task", name, "action", "pause")
	return nil
}

// Resume 按名称从剩余时长继续任务（PRD F5）。
func (m *Manager) Resume(name string) error {
	t, ok := m.lookup(name)
	if !ok {
		return fmt.Errorf("任务 %s 不存在", name)
	}
	t.Resume()
	m.logger.Info("任务已继续", "task", name, "action", "resume")
	return nil
}

// Stop 按名称停止并移除任务（PRD F6）。重复停止已结束的任务同样视为成功移除。
// 番茄钟阶段任务与序列同名（F20）：若名称同时命中某个番茄钟序列，则在停止
// 当前阶段任务之外一并终止整个序列并将其从注册表移除（F19）。
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	t, hasTask := m.tasks[name]
	if hasTask {
		delete(m.tasks, name)
	}
	p := m.detachPomodoroLocked(name)
	m.mu.Unlock()

	if !hasTask && p == nil {
		return fmt.Errorf("任务 %s 不存在", name)
	}
	if hasTask {
		t.Stop()
		m.logger.Info("任务已停止", "task", name, "trigger", "manual", "action", "stop")
	}
	if p != nil {
		// tracker.Stop 内部完成序列停止日志与 sequence 模式的 finish_command。
		p.Stop()
	}
	return nil
}

// detachPomodoroLocked 从注册表摘除指定番茄钟序列并返回，供 Stop 与 PomodoroStop
// 复用查删逻辑；须持有 m.mu，序列不存在时返回 nil。摘除后对 tracker 的调用在
// 解锁后进行（锁顺序约定见 Manager 文档）。
func (m *Manager) detachPomodoroLocked(name string) *PomodoroTracker {
	p, ok := m.pomodoros[name]
	if ok {
		delete(m.pomodoros, name)
	}
	return p
}

// lookup 返回指定名称的任务。
func (m *Manager) lookup(name string) (*Task, bool) {
	m.mu.Lock()
	t, ok := m.tasks[name]
	m.mu.Unlock()
	return t, ok
}

// registerTask 将任务注册进任务表（覆盖同名条目）。供 PomodoroTracker 注册阶段
// 任务：tracker 持自身锁后调用，构成允许的 p.mu→m.mu 锁序，并保持与 Manager.Start
// 相同的「持锁注册」语义，防御极短时长下回调先于注册执行。
func (m *Manager) registerTask(t *Task) {
	m.mu.Lock()
	m.tasks[t.name] = t
	m.mu.Unlock()
}

// unregisterTask 从任务表移除指定名称的任务。供 PomodoroTracker 在阶段结束、
// 序列停止时清理同名阶段任务，替代跨对象直改 m.tasks。
func (m *Manager) unregisterTask(name string) {
	m.mu.Lock()
	delete(m.tasks, name)
	m.mu.Unlock()
}

// PomodoroOptions 描述番茄钟序列参数。零值时长字段由 PomodoroTracker.fillDefaults
// 在构造时按「显式传入（CLI）> 规则档位 > 全局 [pomodoro] 配置 > PRD 默认值」
// 补全；结束事件命令为空表示不执行（无默认动作），CLI 未传时回退规则配置。
type PomodoroOptions struct {
	Work           time.Duration // 专注时长
	ShortBreak     time.Duration // 短休时长
	LongBreak      time.Duration // 长休时长
	Cycle          int           // 长休前的专注次数
	Rule           string        // 关联预置番茄钟规则名（Kind==RulePomodoro），提供时长档位与结束事件默认值
	FinishEach     string        // 每阶段结束事件命令；空 = 不执行
	FinishSequence string        // 整个序列结束事件命令（序列终止时执行一次）；空 = 不执行
}

// PomodoroStart 启动番茄钟序列，自动在专注/短休/长休间切换（PRD F15）。
// 序列名同时作为各阶段任务名注册到 tasks，用户可直接对当前阶段执行
// pause/resume/stop（F20）；与已有任务或番茄钟重名、或 Rule 关联的预置规则
// 未定义/非番茄钟类型时返回错误。
func (m *Manager) PomodoroStart(name string, opts PomodoroOptions) error {
	if name == "" {
		return errors.New("番茄钟名称不能为空")
	}
	// 规则校验须在创建 tracker 之前完成：newPomodoroTracker 不返回错误，其
	// fillDefaults 依赖规则为已定义的番茄钟类型（时长档位与结束事件默认值）。
	if opts.Rule != "" {
		r, ok := m.cfg.Rules[opts.Rule]
		if !ok {
			return fmt.Errorf("番茄钟 %s: 规则 %q 未定义", name, opts.Rule)
		}
		if r.Kind != config.RulePomodoro {
			return fmt.Errorf("番茄钟 %s: 规则 %q 是计时规则，不能用于番茄钟", name, opts.Rule)
		}
	}

	m.mu.Lock()
	if _, exists := m.tasks[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("任务 %s 已存在", name)
	}
	if _, exists := m.pomodoros[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("番茄钟 %s 已存在", name)
	}
	m.mu.Unlock()

	// 不持 m.mu 创建：newPomodoroTracker 会立即启动第一阶段任务并加锁注册进
	// tasks，持锁调用会与 startStage 内的加锁顺序相互死锁。
	p := newPomodoroTracker(name, opts, m)

	m.mu.Lock()
	m.pomodoros[name] = p
	m.mu.Unlock()

	m.logger.Info("番茄钟已启动",
		"task", name,
		"rule", p.opts.Rule,
		"phase", phaseWork,
		"action", "pomodoro_start")
	return nil
}

// PomodoroStatus 返回指定番茄钟的当前阶段与周期进度（PRD F18）。
// 返回领域状态 PomodoroState，协议结构 protocol.PomodoroInfo 的转换由 IPC 层
// （server.go）完成，避免领域层依赖传输协议。
func (m *Manager) PomodoroStatus(name string) (*PomodoroState, error) {
	m.mu.Lock()
	p, ok := m.pomodoros[name]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("番茄钟 %s 不存在", name)
	}
	state := p.Status()
	return &state, nil
}

// PomodoroStop 终止整个番茄钟序列并停止当前阶段任务（PRD F19），是序列停止的
// 唯一入口：IPC 层的 pomodoro_stop 与 tracker 确认弹窗的停止路径（requestStop）
// 共用，不再单设 StopPomodoroInternal。短暂持 m.mu 查删 m.pomodoros 条目，
// 解锁后调用 tracker.Stop()（其内部再经 unregisterTask 短暂持 m.mu 删除阶段
// 任务）；不得在持有 tracker 锁（p.mu）时调用，否则会与 Stop 内部的加锁顺序
// 互锁。序列停止日志与 sequence 模式的 finish_command 由 PomodoroTracker.Stop 处理。
func (m *Manager) PomodoroStop(name string) error {
	m.mu.Lock()
	p := m.detachPomodoroLocked(name)
	m.mu.Unlock()
	if p == nil {
		return fmt.Errorf("番茄钟 %s 不存在", name)
	}
	p.Stop()
	return nil
}

// PomodoroNext 手动推进指定番茄钟到待确认的下一阶段：阶段结束确认被暂停或
// 无法弹窗时序列进入 waiting，由本方法作为恢复入口（对应 CLI tempo pomo next）。
func (m *Manager) PomodoroNext(name string) error {
	m.mu.Lock()
	p, ok := m.pomodoros[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("番茄钟 %s 不存在", name)
	}
	return p.Advance()
}

// PomodoroNames 返回所有活动番茄钟序列的名称，按名称排序。
// 供 IPC 层在 CLI 未指定名称时解析唯一活动序列。
func (m *Manager) PomodoroNames() []string {
	m.mu.Lock()
	names := make([]string, 0, len(m.pomodoros))
	for name := range m.pomodoros {
		names = append(names, name)
	}
	m.mu.Unlock()
	sort.Strings(names)
	return names
}

// formatRemaining 将剩余时长四舍五入到秒并格式化为人类可读形式（如 "24m30s"），负值钳制为 0。
func formatRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}
