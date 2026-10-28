// Package config 加载并解析 TOML 配置（tempo.toml）：daemon 设置、番茄钟全局
// 默认参数与预置规则（计时与番茄钟统一命名空间）。
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/BurntSushi/toml"
)

// RuleKind 区分预置规则的类型，按规则中出现的是 duration 还是番茄钟字段判别，
// 两类字段互斥。
type RuleKind int

const (
	// RuleTimer 计时规则：提供 duration、可选 finish_notify（结束提醒命令）与
	// finish（结束事件命令），供 tempo start 使用。
	RuleTimer RuleKind = iota
	// RulePomodoro 番茄钟规则：提供 work/short_break/long_break/cycle 时长档位
	// 与可选 finish_each/finish_sequence 结束事件，未填字段回退全局默认，
	// 供 tempo pomo start 的 -r 引用。
	RulePomodoro
)

// Rule 是一条预置规则，对应配置中的 [rules.NAME] 段。计时与番茄钟规则共用同一
// 命名空间与 -r 标志，按 Kind 区分、字段二选一：通知（finish_notify）定制计时
// 结束提醒、有平台默认动作；结束事件（finish/finish_each/finish_sequence）没有
// 默认动作，留空或 false 一律不执行。
type Rule struct {
	Kind           RuleKind      // 规则类型
	Duration       time.Duration // RuleTimer：计时时长
	Work           time.Duration // RulePomodoro：专注时长，0 表示未设置
	ShortBreak     time.Duration // RulePomodoro：短休时长，0 表示未设置
	LongBreak      time.Duration // RulePomodoro：长休时长，0 表示未设置
	Cycle          int           // RulePomodoro：长休前的专注次数，0 表示未设置
	FinishNotify   string        // RuleTimer：计时结束提醒命令，空时使用平台默认通知
	Finish         string        // RuleTimer：计时结束事件命令，空 = 不执行
	FinishEach     string        // RulePomodoro：每阶段结束事件命令，空 = 不执行
	FinishSequence string        // RulePomodoro：整个序列结束事件命令，空 = 不执行
}

// PomodoroConfig 是 [pomodoro] 段的原始 TOML 表示；时长保持字符串，
// 由 (*Config).PomodoroDefaults 统一解析，解析失败时回退内置默认值。
type PomodoroConfig struct {
	Work       string `toml:"work"`
	ShortBreak string `toml:"short_break"`
	LongBreak  string `toml:"long_break"`
	Cycle      int    `toml:"cycle"`
}

// Config 是 tempo.toml 的内存表示。
type Config struct {
	LogPath  string          // daemon 日志路径，空表示使用平台默认路径
	Rules    map[string]Rule // 预置规则，按名称索引
	Pomodoro PomodoroConfig  // 番茄钟全局默认参数，经 PomodoroDefaults 解析
}

// DefaultPomodoro 返回番茄钟的内置默认参数（PRD F16）：
// 专注 25m、短休 5m、长休 15m、每 4 个专注后长休。
func DefaultPomodoro() (work, short, long time.Duration, cycle int) {
	return 25 * time.Minute, 5 * time.Minute, 15 * time.Minute, 4
}

// PomodoroDefaults 把 [pomodoro] 段的全局默认参数解析为时长后返回：
// 时长字段为空、非法或非正，或 cycle 非正时，回退到 DefaultPomodoro 的内置默认值。
func (c *Config) PomodoroDefaults() (work, short, long time.Duration, cycle int) {
	defWork, defShort, defLong, defCycle := DefaultPomodoro()
	work = parseDurationOr(c.Pomodoro.Work, defWork)
	short = parseDurationOr(c.Pomodoro.ShortBreak, defShort)
	long = parseDurationOr(c.Pomodoro.LongBreak, defLong)
	cycle = c.Pomodoro.Cycle
	if cycle <= 0 {
		cycle = defCycle
	}
	return work, short, long, cycle
}

// parseDurationOr 解析时长字符串；为空、非法或非正值时返回 fallback。
func parseDurationOr(raw string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return fallback
}

// rawRule 用于 TOML 反序列化；时长保持字符串以便统一校验解析错误。规则类型按
// 字段判别：出现任一番茄钟字段即为番茄钟规则，出现 duration 即为计时规则。
// 结束事件（finish/finish_each/finish_sequence）的值可为命令字符串或布尔
// false（显式禁用），故以 interface{} 承接后经 parseFinishEvent 归一化；
// finish_command 与 finish_on 为已移除的旧键，出现即报错提示迁移。
type rawRule struct {
	Duration       string      `toml:"duration"`
	Work           string      `toml:"work"`
	ShortBreak     string      `toml:"short_break"`
	LongBreak      string      `toml:"long_break"`
	Cycle          int         `toml:"cycle"`
	FinishNotify   string      `toml:"finish_notify"`
	Finish         interface{} `toml:"finish"`
	FinishEach     interface{} `toml:"finish_each"`
	FinishSequence interface{} `toml:"finish_sequence"`
	FinishCommand  string      `toml:"finish_command"` // 旧键：出现即报错提示迁移
	FinishOn       string      `toml:"finish_on"`      // 旧键：出现即报错提示迁移
}

type rawConfig struct {
	LogPath  string             `toml:"log_path"`
	Rules    map[string]rawRule `toml:"rules"`
	Pomodoro PomodoroConfig     `toml:"pomodoro"`
}

// Load 从 path 读取 TOML 配置并解析。文件不存在时返回空配置而非错误。
func Load(path string) (*Config, error) {
	var raw rawConfig
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("加载配置 %s: %w", path, err)
	}
	return raw.toConfig()
}

// toConfig 将 TOML 中间结构转换为 Config，并校验规则类型、时长与结束事件取值。
// 全局 [pomodoro] 段维持宽松语义（非法值回退内置默认）；规则内的字段属显式
// 预置配置，非法即报错，尽早暴露错误。
func (r rawConfig) toConfig() (*Config, error) {
	cfg := &Config{
		LogPath:  r.LogPath,
		Rules:    make(map[string]Rule, len(r.Rules)),
		Pomodoro: r.Pomodoro,
	}

	for name, rr := range r.Rules {
		rule, err := rr.toRule(name)
		if err != nil {
			return nil, err
		}
		cfg.Rules[name] = rule
	}
	return cfg, nil
}

// toRule 按字段判别规则类型并校验：出现任一番茄钟字段（work/short_break/
// long_break/cycle）即为番茄钟规则，出现 duration 即为计时规则；两类字段互斥
// 且至少配置其一。
func (rr rawRule) toRule(name string) (Rule, error) {
	// finish_on 已被分别配置的结束事件取代，出现即报错提示迁移，避免旧配置被
	// 静默忽略后行为悄然改变。
	if rr.FinishOn != "" {
		return Rule{}, fmt.Errorf("规则 %s: finish_on 已移除，结束事件请分别用 finish（计时）/ finish_each、finish_sequence（番茄钟）配置", name)
	}
	hasTimer := rr.Duration != ""
	hasPomodoro := rr.Work != "" || rr.ShortBreak != "" || rr.LongBreak != "" || rr.Cycle != 0
	switch {
	case hasTimer && hasPomodoro:
		return Rule{}, fmt.Errorf("规则 %s: duration 与番茄钟字段（work/short_break/long_break/cycle）不能同时配置", name)
	case hasPomodoro:
		return rr.toPomodoroRule(name)
	case hasTimer:
		return rr.toTimerRule(name)
	default:
		return Rule{}, fmt.Errorf("规则 %s: 缺少 duration 或番茄钟字段（work/short_break/long_break/cycle）之一", name)
	}
}

// toTimerRule 构造计时规则：duration 必填且须为正时长；finish_notify 为计时结束
// 的提醒命令（空时使用平台默认通知），finish 为计时结束事件（空或 false 不执行）。
// 仅番茄钟可用的结束事件键与旧键 finish_command 出现即报错提示迁移。
func (rr rawRule) toTimerRule(name string) (Rule, error) {
	if rr.FinishEach != nil || rr.FinishSequence != nil {
		return Rule{}, fmt.Errorf("规则 %s: 计时规则不支持 finish_each/finish_sequence，请改用 finish 配置计时结束事件", name)
	}
	if rr.FinishCommand != "" {
		return Rule{}, fmt.Errorf("规则 %s: finish_command 已更名为 finish_notify，请改名后使用", name)
	}
	duration, err := parseRuleDuration(name, "duration", rr.Duration)
	if err != nil {
		return Rule{}, err
	}
	finish, err := parseFinishEvent(name, "finish", rr.Finish)
	if err != nil {
		return Rule{}, err
	}
	return Rule{
		Kind:         RuleTimer,
		Duration:     duration,
		FinishNotify: rr.FinishNotify,
		Finish:       finish,
	}, nil
}

// toPomodoroRule 构造番茄钟规则：时长字段可留空表示未设置（由全局默认与内置
// 默认值补全），显式给出时须为正；cycle 不得为负。结束事件 finish_each 在每个
// 阶段结束时执行、finish_sequence 在整个序列终止时执行一次，空或 false 均不
// 执行；番茄钟的阶段确认弹窗即其通知机制，不提供 finish_notify，旧键
// finish_command 与计时专用的 finish/finish_notify 出现即报错提示迁移。
func (rr rawRule) toPomodoroRule(name string) (Rule, error) {
	if rr.FinishCommand != "" {
		return Rule{}, fmt.Errorf("规则 %s: 番茄钟规则不支持 finish_command，请改用 finish_each 与 finish_sequence 分别配置结束事件", name)
	}
	if rr.FinishNotify != "" || rr.Finish != nil {
		return Rule{}, fmt.Errorf("规则 %s: 番茄钟规则不支持 finish_notify/finish，请改用 finish_each 与 finish_sequence 分别配置结束事件", name)
	}
	work, err := parseRuleDuration(name, "work", rr.Work)
	if err != nil {
		return Rule{}, err
	}
	shortBreak, err := parseRuleDuration(name, "short_break", rr.ShortBreak)
	if err != nil {
		return Rule{}, err
	}
	longBreak, err := parseRuleDuration(name, "long_break", rr.LongBreak)
	if err != nil {
		return Rule{}, err
	}
	if rr.Cycle < 0 {
		return Rule{}, fmt.Errorf("规则 %s: cycle 不能为负，得到 %d", name, rr.Cycle)
	}
	finishEach, err := parseFinishEvent(name, "finish_each", rr.FinishEach)
	if err != nil {
		return Rule{}, err
	}
	finishSequence, err := parseFinishEvent(name, "finish_sequence", rr.FinishSequence)
	if err != nil {
		return Rule{}, err
	}
	return Rule{
		Kind:           RulePomodoro,
		Work:           work,
		ShortBreak:     shortBreak,
		LongBreak:      longBreak,
		Cycle:          rr.Cycle,
		FinishEach:     finishEach,
		FinishSequence: finishSequence,
	}, nil
}

// parseRuleDuration 解析规则内的时长字段：空串返回 0 表示未设置；非法或非正
// 时长报错（与全局段静默回退不同，显式预置配置写错应尽早失败）。
func parseRuleDuration(rule, key, raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("规则 %s: 解析 %s %q: %w", rule, key, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("规则 %s: %s 必须为正时长，得到 %q", rule, key, raw)
	}
	return d, nil
}

// parseFinishEvent 归一化结束事件配置（finish / finish_each / finish_sequence）：
// 字符串按命令处理（空串表示不执行），布尔 false 表示显式禁用，true 无命令可
// 执行属配置错误，其余类型报错。结束事件不同于通知：没有默认动作，未配置即
// 不执行。
func parseFinishEvent(rule, key string, v interface{}) (string, error) {
	switch val := v.(type) {
	case nil:
		return "", nil
	case string:
		return val, nil
	case bool:
		if !val {
			return "", nil
		}
		return "", fmt.Errorf("规则 %s: %s = true 无意义，应为命令字符串或 false", rule, key)
	default:
		return "", fmt.Errorf("规则 %s: %s 应为命令字符串或 false，得到 %T", rule, key, v)
	}
}
