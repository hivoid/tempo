package daemon

import (
	"errors"
	"strings"
	"testing"
	"time"

	"tempo/internal/config"
)

func TestNewManagerDefaults(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if m.cfg == nil {
		t.Error("cfg 为 nil 时应回退为空配置")
	}
	if m.logger == nil {
		t.Error("logger 为 nil 时应回退为丢弃日志器")
	}
	if m.tasks == nil {
		t.Error("tasks map 应已初始化")
	}
}

func TestListEmpty(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if tasks := m.List(); len(tasks) != 0 {
		t.Errorf("空 manager 应返回空列表: %+v", tasks)
	}
}

func TestStartWithExplicitDuration(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("tea", "", "30s", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tasks := m.List()
	if len(tasks) != 1 {
		t.Fatalf("List 数量 = %d, want 1", len(tasks))
	}
	ti := tasks[0]
	if ti.Name != "tea" || ti.State != "running" || ti.Rule != "" {
		t.Errorf("TaskInfo = %+v", ti)
	}
	if ti.Remaining == "" || ti.Remaining == "0s" {
		t.Errorf("剩余时长异常: %q", ti.Remaining)
	}
}

func TestStartValidationErrors(t *testing.T) {
	m := NewManager(nil, nil, nil)

	if err := m.Start("", "", "10s", ""); err == nil || !strings.Contains(err.Error(), "任务名称不能为空") {
		t.Errorf("空名称应报错，得到: %v", err)
	}
	if err := m.Start("bad", "", "abc", ""); err == nil || !strings.Contains(err.Error(), "解析时长") {
		t.Errorf("非法时长应报错，得到: %v", err)
	}
	if err := m.Start("ghost", "no-such-rule", "", ""); err == nil || !strings.Contains(err.Error(), "未定义") {
		t.Errorf("未定义规则应报错，得到: %v", err)
	}
	if err := m.Start("empty", "", "", ""); err == nil || !strings.Contains(err.Error(), "必须提供有效时长") {
		t.Errorf("无时长且无规则应报错，得到: %v", err)
	}
}

func TestStartUsesRuleDuration(t *testing.T) {
	cfg := &config.Config{
		Rules: map[string]config.Rule{
			"tea": {Duration: 10 * time.Minute, FinishCommand: "echo done"},
		},
	}
	m := NewManager(cfg, nil, nil)
	if err := m.Start("tea", "tea", "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tasks := m.List()
	if len(tasks) != 1 {
		t.Fatalf("List 数量 = %d, want 1", len(tasks))
	}
	if tasks[0].Rule != "tea" {
		t.Errorf("Rule = %q, want tea", tasks[0].Rule)
	}
	if got := tasks[0].Remaining; got != "10m0s" {
		t.Errorf("Remaining = %q, want 10m0s", got)
	}
}

func TestStartRejectsDuplicateName(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("tea", "", "30s", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Start("tea", "", "30s", ""); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Errorf("重复名称应报错，得到: %v", err)
	}
}

func TestListSortedByName(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("b", "", "10s", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("a", "", "10s", ""); err != nil {
		t.Fatal(err)
	}
	tasks := m.List()
	if len(tasks) != 2 || tasks[0].Name != "a" || tasks[1].Name != "b" {
		t.Fatalf("应按名称排序: %+v", tasks)
	}
}

func TestPauseResumeLifecycle(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("tea", "", "10s", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := m.Pause("tea"); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	ti := m.List()[0]
	if ti.State != "paused" {
		t.Errorf("暂停后状态 = %q, want paused", ti.State)
	}
	if ti.Remaining == "" || ti.Remaining == "0s" {
		t.Errorf("暂停后应保留剩余时长: %q", ti.Remaining)
	}

	if err := m.Resume("tea"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	ti = m.List()[0]
	if ti.State != "running" {
		t.Errorf("继续后状态 = %q, want running", ti.State)
	}
}

func TestStopRemovesTask(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("tea", "", "10s", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop("tea"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if tasks := m.List(); len(tasks) != 0 {
		t.Errorf("停止后应无任务: %+v", tasks)
	}
	if err := m.Stop("tea"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("停止不存在的任务应报错，得到: %v", err)
	}
}

func TestUnknownTaskErrors(t *testing.T) {
	m := NewManager(nil, nil, nil)
	for _, tc := range []struct {
		name string
		fn   func(string) error
	}{
		{"Pause", m.Pause},
		{"Resume", m.Resume},
		{"Stop", m.Stop},
	} {
		if err := tc.fn("ghost"); err == nil || !strings.Contains(err.Error(), "不存在") {
			t.Errorf("%s 不存在的任务应报错，得到: %v", tc.name, err)
		}
	}
}

func TestTaskRemovedOnFinish(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("flash", "", "30ms", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(m.List()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("计时结束后任务应被移除")
}

func TestPomodoroNotImplemented(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.PomodoroStart("p", PomodoroOptions{}); !errors.Is(err, errNotImplemented) {
		t.Errorf("PomodoroStart 应返回 errNotImplemented，得到: %v", err)
	}
	if _, err := m.PomodoroStatus("p"); !errors.Is(err, errNotImplemented) {
		t.Errorf("PomodoroStatus 应返回 errNotImplemented，得到: %v", err)
	}
	if err := m.PomodoroStop("p"); !errors.Is(err, errNotImplemented) {
		t.Errorf("PomodoroStop 应返回 errNotImplemented，得到: %v", err)
	}
}

func TestFormatRemaining(t *testing.T) {
	cases := map[time.Duration]string{
		0:                       "0s",
		-5 * time.Second:        "0s",
		90 * time.Second:        "1m30s",
		2400 * time.Millisecond: "2s",
	}
	for d, want := range cases {
		if got := formatRemaining(d); got != want {
			t.Errorf("formatRemaining(%v) = %q, want %q", d, got, want)
		}
	}
}
