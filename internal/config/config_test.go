package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tempo.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidConfig(t *testing.T) {
	path := writeConfig(t, `
[rules.pomodoro]
duration = "25m"
finish_command = "notify-send -t 30000 '番茄钟结束'"
finish_on = "sequence"

[rules.tea]
duration = "10m"

[pomodoro]
work = "30m"
short_break = "5m"
long_break = "15m"
cycle = 3
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	pomoRule, ok := cfg.Rules["pomodoro"]
	if !ok {
		t.Fatal("缺少规则 pomodoro")
	}
	if pomoRule.Duration != 25*time.Minute {
		t.Errorf("pomodoro.Duration = %v, want 25m", pomoRule.Duration)
	}
	if pomoRule.FinishCommand != "notify-send -t 30000 '番茄钟结束'" {
		t.Errorf("pomodoro.FinishCommand = %q", pomoRule.FinishCommand)
	}
	if pomoRule.FinishOn != FinishOnSequence {
		t.Errorf("pomodoro.FinishOn = %q, want %q", pomoRule.FinishOn, FinishOnSequence)
	}

	teaRule, ok := cfg.Rules["tea"]
	if !ok {
		t.Fatal("缺少规则 tea")
	}
	if teaRule.Duration != 10*time.Minute {
		t.Errorf("tea.Duration = %v, want 10m", teaRule.Duration)
	}
	if teaRule.FinishOn != FinishOnEach {
		t.Errorf("缺省 finish_on = %q, want %q", teaRule.FinishOn, FinishOnEach)
	}

	// [pomodoro] 段保留原始字符串，经 PomodoroDefaults 解析为时长。
	if cfg.Pomodoro.Work != "30m" || cfg.Pomodoro.ShortBreak != "5m" ||
		cfg.Pomodoro.LongBreak != "15m" || cfg.Pomodoro.Cycle != 3 {
		t.Errorf("Pomodoro 原始字段 = %+v", cfg.Pomodoro)
	}
	work, short, long, cycle := cfg.PomodoroDefaults()
	if work != 30*time.Minute || short != 5*time.Minute ||
		long != 15*time.Minute || cycle != 3 {
		t.Errorf("PomodoroDefaults() = (%v, %v, %v, %d), want (30m, 5m, 15m, 3)",
			work, short, long, cycle)
	}
}

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatalf("缺失配置文件应返回空配置而非错误: %v", err)
	}
	if cfg == nil || len(cfg.Rules) != 0 {
		t.Errorf("应返回空 Rules，得到: %+v", cfg)
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	path := writeConfig(t, "not [valid")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "加载配置") {
		t.Errorf("非法 TOML 应返回错误，得到: %v", err)
	}
}

func TestLoadInvalidRuleDuration(t *testing.T) {
	path := writeConfig(t, `
[rules.bad]
duration = "abc"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "解析 duration") {
		t.Errorf("非法规则时长应报错，得到: %v", err)
	}
}

func TestLoadInvalidFinishOn(t *testing.T) {
	path := writeConfig(t, `
[rules.bad]
duration = "10m"
finish_on = "sometimes"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "finish_on") {
		t.Errorf("非法 finish_on 应报错，得到: %v", err)
	}
}

func TestPomodoroDefaultsFallback(t *testing.T) {
	// [pomodoro] 时长为空、非法或非正、cycle 非正时回退内置默认值，Load 不报错。
	path := writeConfig(t, `
[pomodoro]
work = "abc"
short_break = "0s"
long_break = ""
cycle = -1
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("非法 [pomodoro] 不应使 Load 报错: %v", err)
	}
	defWork, defShort, defLong, defCycle := DefaultPomodoro()
	work, short, long, cycle := cfg.PomodoroDefaults()
	if work != defWork || short != defShort || long != defLong || cycle != defCycle {
		t.Errorf("非法配置应全部回退默认值，得到 (%v, %v, %v, %d)",
			work, short, long, cycle)
	}
}

func TestDefaultPomodoro(t *testing.T) {
	work, short, long, cycle := DefaultPomodoro()
	if work != 25*time.Minute || short != 5*time.Minute ||
		long != 15*time.Minute || cycle != 4 {
		t.Errorf("DefaultPomodoro() = (%v, %v, %v, %d)", work, short, long, cycle)
	}
}
