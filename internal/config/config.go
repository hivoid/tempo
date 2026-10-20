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

// Pomodoro 是 [pomodoro] 段的全局默认参数；零值表示未设置，由调用方回退到 DefaultPomodoro。
type Pomodoro struct {
	Work       time.Duration // 专注时长
	ShortBreak time.Duration // 短休时长
	LongBreak  time.Duration // 长休时长
	Cycle      int           // 长休前的专注次数
}

// Config 是 rules.toml 的内存表示。
type Config struct {
	Rules    map[string]Rule // 预置规则，按名称索引
	Pomodoro Pomodoro        // 番茄钟全局默认参数
}

// DefaultPomodoro 返回番茄钟的内置默认参数（PRD F16）。
func DefaultPomodoro() Pomodoro {
	return Pomodoro{
		Work:       25 * time.Minute,
		ShortBreak: 5 * time.Minute,
		LongBreak:  15 * time.Minute,
		Cycle:      4,
	}
}

// rawXxx 系列用于 TOML 反序列化；时长保持字符串以便统一校验解析错误。
type rawRule struct {
	Duration      string `toml:"duration"`
	FinishCommand string `toml:"finish_command"`
	FinishOn      string `toml:"finish_on"`
}

type rawPomodoro struct {
	Work       string `toml:"work"`
	ShortBreak string `toml:"short_break"`
	LongBreak  string `toml:"long_break"`
	Cycle      int    `toml:"cycle"`
}

type rawConfig struct {
	Rules    map[string]rawRule `toml:"rules"`
	Pomodoro rawPomodoro        `toml:"pomodoro"`
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
	work, err := parsePomodoroDuration("work", r.Pomodoro.Work)
	if err != nil {
		return nil, err
	}
	shortBreak, err := parsePomodoroDuration("short_break", r.Pomodoro.ShortBreak)
	if err != nil {
		return nil, err
	}
	longBreak, err := parsePomodoroDuration("long_break", r.Pomodoro.LongBreak)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Rules: make(map[string]Rule, len(r.Rules)),
		Pomodoro: Pomodoro{
			Work:       work,
			ShortBreak: shortBreak,
			LongBreak:  longBreak,
			Cycle:      r.Pomodoro.Cycle,
		},
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

// parsePomodoroDuration 解析 [pomodoro] 段中的单个时长字段，空字符串视为未设置。
func parsePomodoroDuration(name, raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("pomodoro.%s: 解析时长 %q: %w", name, raw, err)
	}
	return d, nil
}
