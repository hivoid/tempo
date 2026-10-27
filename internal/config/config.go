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

// finish_on 的合法取值。
const (
	// FinishOnEach 表示自定义结束命令在每个阶段结束时执行。
	FinishOnEach = "each"
	// FinishOnSequence 表示自定义结束命令在整个序列结束时执行。
	FinishOnSequence = "sequence"
)

// RuleKind 区分预置规则的类型，按规则中出现的是 duration 还是番茄钟字段判别，
// 两类字段互斥。
type RuleKind int

const (
	// RuleTimer 计时规则：提供 duration 与可选 finish_command，供 tempo start 使用。
	RuleTimer RuleKind = iota
	// RulePomodoro 番茄钟规则：提供 work/short_break/long_break/cycle 时长档位
	// 与可选 finish_command/finish_on，未填字段回退全局默认，供 tempo pomo start
	// 的 -r 引用。
	RulePomodoro
)

// Rule 是一条预置规则，对应配置中的 [rules.NAME] 段。计时与番茄钟规则共用同一
// 命名空间与 -r 标志，按 Kind 区分、字段二选一。
type Rule struct {
	Kind          RuleKind      // 规则类型
	Duration      time.Duration // RuleTimer：计时时长
	Work          time.Duration // RulePomodoro：专注时长，0 表示未设置
	ShortBreak    time.Duration // RulePomodoro：短休时长，0 表示未设置
	LongBreak     time.Duration // RulePomodoro：长休时长，0 表示未设置
	Cycle         int           // RulePomodoro：长休前的专注次数，0 表示未设置
	FinishCommand string        // 自定义结束命令，为空时使用平台默认提醒
	FinishOn      string        // FinishOnEach | FinishOnSequence，缺省为 FinishOnEach
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
type rawRule struct {
	Duration      string `toml:"duration"`
	Work          string `toml:"work"`
	ShortBreak    string `toml:"short_break"`
	LongBreak     string `toml:"long_break"`
	Cycle         int    `toml:"cycle"`
	FinishCommand string `toml:"finish_command"`
	FinishOn      string `toml:"finish_on"`
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

// toConfig 将 TOML 中间结构转换为 Config，并校验规则类型、时长与 finish_on 取值。
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

// toTimerRule 构造计时规则：duration 必填且须为正时长。
func (rr rawRule) toTimerRule(name string) (Rule, error) {
	duration, err := parseRuleDuration(name, "duration", rr.Duration)
	if err != nil {
		return Rule{}, err
	}
	finishOn, err := normalizeFinishOn(name, rr.FinishOn)
	if err != nil {
		return Rule{}, err
	}
	return Rule{
		Kind:          RuleTimer,
		Duration:      duration,
		FinishCommand: rr.FinishCommand,
		FinishOn:      finishOn,
	}, nil
}

// toPomodoroRule 构造番茄钟规则：时长字段可留空表示未设置（由全局默认与内置
// 默认值补全），显式给出时须为正；cycle 不得为负。
func (rr rawRule) toPomodoroRule(name string) (Rule, error) {
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
	finishOn, err := normalizeFinishOn(name, rr.FinishOn)
	if err != nil {
		return Rule{}, err
	}
	return Rule{
		Kind:          RulePomodoro,
		Work:          work,
		ShortBreak:    shortBreak,
		LongBreak:     longBreak,
		Cycle:         rr.Cycle,
		FinishCommand: rr.FinishCommand,
		FinishOn:      finishOn,
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

// normalizeFinishOn 校验并归一化 finish_on：空值按 FinishOnEach 处理。
func normalizeFinishOn(name, raw string) (string, error) {
	if raw == "" {
		return FinishOnEach, nil
	}
	if raw != FinishOnEach && raw != FinishOnSequence {
		return "", fmt.Errorf("规则 %s: 非法的 finish_on %q（允许 %q 或 %q）",
			name, raw, FinishOnEach, FinishOnSequence)
	}
	return raw, nil
}
