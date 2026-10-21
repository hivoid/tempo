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
type Manager struct {
	mu sync.Mutex

	cfg        *config.Config
	notifier   notify.Notifier
	logger     *slog.Logger
	tasks      map[string]*Task
	pomodoros  map[string]*PomodoroTracker
}

// NewManager 创建任务管理器。cfg、notifier、logger 由调用方注入：
// cfg 为 nil 时按空配置处理；notifier 为 nil 时不发送通知；
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
// finishCmd 优先级：显式传入 > 规则 finish_command > 平台默认提醒（PRD F11）。
func (m *Manager) Start(name, rule, duration, finishCmd string) error {
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

	ruleFinishCmd := ""
	if rule != "" {
		r, ok := m.cfg.Rules[rule]
		if !ok {
			return fmt.Errorf("任务 %s: 规则 %q 未定义", name, rule)
		}
		if d == 0 {
			d = r.Duration
		}
		ruleFinishCmd = r.FinishCommand
	}
	if d <= 0 {
		return fmt.Errorf("任务 %s: 必须提供有效时长或含有效时长的规则", name)
	}
	if finishCmd == "" {
		finishCmd = ruleFinishCmd
	}

	m.mu.Lock()
	if _, exists := m.tasks[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("任务 %s 已存在", name)
	}
	// 在持锁状态下创建并注册：若时长极短导致计时器立即到点，
	// onTaskFinish 须等锁释放后才能执行，避免插入一个已结束的任务。
	t := newTask(name, rule, finishCmd, d, m.onTaskFinish)
	m.tasks[name] = t
	m.mu.Unlock()

	m.logger.Info("任务已创建", "task", name, "rule", rule, "duration", d, "finish", finishCmd, "action", "start")
	return nil
}

// onTaskFinish 是任务计时到点的结束回调：从注册表移除任务、发送平台默认提醒并记日志。
// 由 Task.fire 在锁外调用。自定义结束命令的执行（PRD F9/F11/F12，
// Linux sh -c / Windows cmd /c）待平台适配层完成后在此接入。
func (m *Manager) onTaskFinish(t *Task) {
	m.mu.Lock()
	delete(m.tasks, t.name)
	m.mu.Unlock()

	m.logger.Info("任务计时结束", "task", t.name, "rule", t.rule, "trigger", "timer", "action", "finish")

	if m.notifier == nil {
		return
	}
	if err := m.notifier.Notify("tempo", fmt.Sprintf("任务 %s 计时结束", t.name), 10*time.Second); err != nil {
		m.logger.Error("发送通知失败", "task", t.name, "error", err)
		return
	}
	m.logger.Info("通知已发送", "task", t.name)
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
	t, ok := m.tasks[name]
	if ok {
		delete(m.tasks, name)
	}
	p, isPomo := m.pomodoros[name]
	if isPomo {
		delete(m.pomodoros, name)
	}
	m.mu.Unlock()

	if !ok && !isPomo {
		return fmt.Errorf("任务 %s 不存在", name)
	}
	if ok {
		t.Stop()
		m.logger.Info("任务已停止", "task", name, "trigger", "manual", "action", "stop")
	}
	if isPomo {
		// tracker.Stop 内部完成序列停止日志与 sequence 模式的 finish_command。
		p.Stop()
	}
	return nil
}

// lookup 返回指定名称的任务。
func (m *Manager) lookup(name string) (*Task, bool) {
	m.mu.Lock()
	t, ok := m.tasks[name]
	m.mu.Unlock()
	return t, ok
}

// PomodoroOptions 描述番茄钟序列参数。零值字段与 FinishOn 的缺省值由
// PomodoroTracker.fillDefaults 在构造时按「显式传入 > 全局 [pomodoro] 配置 >
// PRD 默认值」补全。
type PomodoroOptions struct {
	Work       time.Duration // 专注时长
	ShortBreak time.Duration // 短休时长
	LongBreak  time.Duration // 长休时长
	Cycle      int           // 长休前的专注次数
	Rule       string        // 关联预置规则名
	FinishOn   string        // "each" | "sequence"，控制自定义命令执行时机
}

// PomodoroStart 启动番茄钟序列，自动在专注/短休/长休间切换（PRD F15）。
// 序列名同时作为各阶段任务名注册到 tasks，用户可直接对当前阶段执行
// pause/resume/stop（F20）；与已有任务或番茄钟重名时返回错误。
func (m *Manager) PomodoroStart(name string, opts PomodoroOptions) error {
	if name == "" {
		return errors.New("番茄钟名称不能为空")
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
func (m *Manager) PomodoroStatus(name string) (*protocol.PomodoroInfo, error) {
	m.mu.Lock()
	p, ok := m.pomodoros[name]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("番茄钟 %s 不存在", name)
	}
	info := p.Status()
	return &info, nil
}

// PomodoroStop 终止整个番茄钟序列并停止当前阶段任务（PRD F19）。
// 序列停止日志与 sequence 模式的 finish_command 由 PomodoroTracker.Stop 处理。
func (m *Manager) PomodoroStop(name string) error {
	m.mu.Lock()
	p, ok := m.pomodoros[name]
	if ok {
		delete(m.pomodoros, name)
	}
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("番茄钟 %s 不存在", name)
	}
	p.Stop()
	return nil
}

// PomodoroNext 手动推进指定番茄钟到待确认的下一阶段：阶段结束确认被拒绝或
// 无法弹窗时序列进入 waiting，由本方法作为恢复入口（对应 CLI tempo pomodoro next）。
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
