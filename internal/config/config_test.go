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
	path := filepath.Join(t.TempDir(), "rules.toml")
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

	if cfg.Pomodoro.Work != 30*time.Minute {
		t.Errorf("Pomodoro.Work = %v, want 30m", cfg.Pomodoro.Work)
	}
	if cfg.Pomodoro.ShortBreak != 5*time.Minute {
		t.Errorf("Pomodoro.ShortBreak = %v, want 5m", cfg.Pomodoro.ShortBreak)
	}
	if cfg.Pomodoro.LongBreak != 15*time.Minute {
		t.Errorf("Pomodoro.LongBreak = %v, want 15m", cfg.Pomodoro.LongBreak)
	}
	if cfg.Pomodoro.Cycle != 3 {
		t.Errorf("Pomodoro.Cycle = %d, want 3", cfg.Pomodoro.Cycle)
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
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "解析时长") {
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

func TestLoadInvalidPomodoroDuration(t *testing.T) {
	path := writeConfig(t, `
[pomodoro]
work = "abc"
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "pomodoro.work") {
		t.Errorf("非法番茄钟时长应报错，得到: %v", err)
	}
}

func TestDefaultPomodoro(t *testing.T) {
	p := DefaultPomodoro()
	if p.Work != 25*time.Minute || p.ShortBreak != 5*time.Minute ||
		p.LongBreak != 15*time.Minute || p.Cycle != 4 {
		t.Errorf("DefaultPomodoro() = %+v", p)
	}
}
