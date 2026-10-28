package daemon

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
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
	if m.pomodoros == nil {
		t.Error("pomodoros map 应已初始化")
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
	if err := m.Start("tea", "", "30s", "", ""); err != nil {
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

	if err := m.Start("", "", "10s", "", ""); err == nil || !strings.Contains(err.Error(), "任务名称不能为空") {
		t.Errorf("空名称应报错，得到: %v", err)
	}
	if err := m.Start("bad", "", "abc", "", ""); err == nil || !strings.Contains(err.Error(), "解析时长") {
		t.Errorf("非法时长应报错，得到: %v", err)
	}
	if err := m.Start("ghost", "no-such-rule", "", "", ""); err == nil || !strings.Contains(err.Error(), "未定义") {
		t.Errorf("未定义规则应报错，得到: %v", err)
	}
	if err := m.Start("empty", "", "", "", ""); err == nil || !strings.Contains(err.Error(), "必须提供有效时长") {
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
	if err := m.Start("tea", "tea", "", "", ""); err != nil {
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
	if err := m.Start("tea", "", "30s", "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Start("tea", "", "30s", "", ""); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Errorf("重复名称应报错，得到: %v", err)
	}
}

func TestListSortedByName(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("b", "", "10s", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("a", "", "10s", "", ""); err != nil {
		t.Fatal(err)
	}
	tasks := m.List()
	if len(tasks) != 2 || tasks[0].Name != "a" || tasks[1].Name != "b" {
		t.Fatalf("应按名称排序: %+v", tasks)
	}
}

func TestPauseResumeLifecycle(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("tea", "", "10s", "", ""); err != nil {
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
	if err := m.Start("tea", "", "10s", "", ""); err != nil {
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
	if err := m.Start("flash", "", "30ms", "", ""); err != nil {
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

// TestPomodoroStartRegistersStageTask 验证启动番茄钟后以序列名注册运行中的阶段任务。
func TestPomodoroStartRegistersStageTask(t *testing.T) {
	m := NewManager(nil, nil, nil)
	t.Cleanup(func() { _ = m.PomodoroStop("focus") })

	if err := m.PomodoroStart("focus", PomodoroOptions{Work: time.Second}); err != nil {
		t.Fatalf("PomodoroStart: %v", err)
	}
	waitForTasks(t, m, 1, time.Second)
	if tasks := m.List(); tasks[0].Name != "focus" || tasks[0].State != "running" {
		t.Errorf("阶段任务应以序列名注册: %+v", tasks)
	}
}

// TestPomodoroStartRejectsDuplicateNames 验证与已有任务或其他番茄钟重名时报错。
func TestPomodoroStartRejectsDuplicateNames(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.Start("tea", "", "10s", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop("tea") })

	if err := m.PomodoroStart("", PomodoroOptions{}); err == nil {
		t.Error("空名称应报错")
	}
	if err := m.PomodoroStart("tea", PomodoroOptions{}); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Errorf("与已有任务重名应报错，得到: %v", err)
	}

	if err := m.PomodoroStart("focus", PomodoroOptions{Work: time.Second}); err != nil {
		t.Fatalf("PomodoroStart: %v", err)
	}
	t.Cleanup(func() { _ = m.PomodoroStop("focus") })
	if err := m.PomodoroStart("focus", PomodoroOptions{}); err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Errorf("重复启动同名番茄钟应报错，得到: %v", err)
	}
}

// TestPomodoroStatusAndStopLifecycle 验证 status/stop 的正常路径与不存在时的错误。
func TestPomodoroStatusAndStopLifecycle(t *testing.T) {
	m := NewManager(nil, nil, nil)

	if _, err := m.PomodoroStatus("ghost"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("不存在的番茄钟 status 应报错，得到: %v", err)
	}
	if err := m.PomodoroStop("ghost"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("不存在的番茄钟 stop 应报错，得到: %v", err)
	}

	if err := m.PomodoroStart("focus", PomodoroOptions{Work: time.Minute}); err != nil {
		t.Fatalf("PomodoroStart: %v", err)
	}
	info, err := m.PomodoroStatus("focus")
	if err != nil {
		t.Fatalf("PomodoroStatus: %v", err)
	}
	if info.Phase != phaseWork || info.Completed != 0 || info.Cycle != 4 {
		t.Errorf("PomodoroInfo = %+v, want phase=work completed=0 cycle=4", info)
	}

	if err := m.PomodoroStop("focus"); err != nil {
		t.Fatalf("PomodoroStop: %v", err)
	}
	if tasks := m.List(); len(tasks) != 0 {
		t.Errorf("停止后应无阶段任务: %+v", tasks)
	}
	if _, err := m.PomodoroStatus("focus"); err == nil {
		t.Error("停止后 status 应报错")
	}
	if err := m.PomodoroStop("focus"); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Errorf("重复停止应报错，得到: %v", err)
	}
}

// TestStopStageTaskTerminatesSequence 验证按阶段任务名 stop 会连带终止整个序列（F19/F20）。
func TestStopStageTaskTerminatesSequence(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if err := m.PomodoroStart("focus", PomodoroOptions{Work: time.Minute}); err != nil {
		t.Fatalf("PomodoroStart: %v", err)
	}
	waitForTasks(t, m, 1, time.Second)

	if err := m.Stop("focus"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if tasks := m.List(); len(tasks) != 0 {
		t.Errorf("停止后应无阶段任务: %+v", tasks)
	}
	if _, err := m.PomodoroStatus("focus"); err == nil {
		t.Error("停止阶段任务应连带终止番茄钟序列")
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

// syncBuffer 是并发安全的 bytes.Buffer：计时器 goroutine 写日志、
// 测试 goroutine 轮询读取，需要互斥保护以通过 -race。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *syncBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf.Reset()
}

// newRecordingLogger 返回写入 buf 的 *slog.Logger，供日志内容断言使用。
func newRecordingLogger(buf *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

// TestStartLogsTaskData 验证启动任务时记录名称、规则、实际时长与结束命令。
func TestStartLogsTaskData(t *testing.T) {
	buf := &syncBuffer{}
	m := NewManager(&config.Config{}, nil, newRecordingLogger(buf))

	if err := m.Start("tea", "", "10m", "notify-done", "echo synced"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"task=tea", "duration=10m0s", "finish_notify=notify-done", "finish_event=echo synced", "action=start"} {
		if !strings.Contains(out, want) {
			t.Errorf("启动日志缺少 %q，实际输出：%s", want, out)
		}
	}
}

// TestStopLogsTrigger 验证手动停止记录触发来源 manual。
func TestStopLogsTrigger(t *testing.T) {
	buf := &syncBuffer{}
	m := NewManager(&config.Config{}, nil, newRecordingLogger(buf))
	if err := m.Start("tea", "", "10m", "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	buf.Reset()

	if err := m.Stop("tea"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"task=tea", "trigger=manual", "action=stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("停止日志缺少 %q，实际输出：%s", want, out)
		}
	}
}

// TestFinishLogsTrigger 验证计时到点记录触发来源 timer。
func TestFinishLogsTrigger(t *testing.T) {
	buf := &syncBuffer{}
	m := NewManager(&config.Config{}, nil, newRecordingLogger(buf))
	if err := m.Start("tea", "", "10ms", "", ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 轮询等待计时器到点触发 onTaskFinish 写入结束日志。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(buf.String(), "action=finish") {
		time.Sleep(5 * time.Millisecond)
	}

	out := buf.String()
	for _, want := range []string{"task=tea", "trigger=timer", "action=finish"} {
		if !strings.Contains(out, want) {
			t.Errorf("结束日志缺少 %q，实际输出：%s", want, out)
		}
	}
}

// TestTimerFinishEventRuns 验证计时结束事件（--finish / 规则 finish）在计时到点
// 时执行一次，且不影响默认提醒路径。
func TestTimerFinishEventRuns(t *testing.T) {
	flagFile := filepath.Join(t.TempDir(), "timer-event")
	m := NewManager(&config.Config{}, nil, nil)
	if err := m.Start("flash", "", "30ms", "", "touch "+flagFile); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitForFile(t, flagFile, 2*time.Second)
}
