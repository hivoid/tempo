// Package config 加载并解析 TOML 规则配置（rules.toml）。
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

// Rule 是一条预置计时规则，对应配置中的 [rules.NAME] 段。
type Rule struct {
	Duration      time.Duration // 计时时长
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

// Config 是 rules.toml 的内存表示。
type Config struct {
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

// rawRule 用于 TOML 反序列化；时长保持字符串以便统一校验解析错误。
type rawRule struct {
	Duration      string `toml:"duration"`
	FinishCommand string `toml:"finish_command"`
	FinishOn      string `toml:"finish_on"`
}

type rawConfig struct {
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

// toConfig 将 TOML 中间结构转换为 Config，并校验时长与 finish_on 取值。
func (r rawConfig) toConfig() (*Config, error) {
	cfg := &Config{
		Rules:    make(map[string]Rule, len(r.Rules)),
		Pomodoro: r.Pomodoro,
	}

	for name, rr := range r.Rules {
		duration, err := time.ParseDuration(rr.Duration)
		if err != nil {
			return nil, fmt.Errorf("规则 %s: 解析时长 %q: %w", name, rr.Duration, err)
		}
		finishOn := rr.FinishOn
		if finishOn == "" {
			finishOn = FinishOnEach
		}
		if finishOn != FinishOnEach && finishOn != FinishOnSequence {
			return nil, fmt.Errorf("规则 %s: 非法的 finish_on %q（允许 %q 或 %q）",
				name, finishOn, FinishOnEach, FinishOnSequence)
		}
		cfg.Rules[name] = Rule{
			Duration:      duration,
			FinishCommand: rr.FinishCommand,
			FinishOn:      finishOn,
		}
	}
	return cfg, nil
}
