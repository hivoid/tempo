//go:build linux

package notify

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeFakeNotifySend 在临时目录放置内容为 body 的 notify-send 脚本，
// 并把 PATH 指向该目录，测试结束后恢复。
func writeFakeNotifySend(t *testing.T, body string) {
	t.Helper()
	binDir := t.TempDir()
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(filepath.Join(binDir, "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
}

func TestNotifyPassesTimeoutInMilliseconds(t *testing.T) {
	writeFakeNotifySend(t, `printf '%s\n' "$@" >> "$TEMPO_ARGS_FILE"
`)
	logFile := filepath.Join(t.TempDir(), "args.txt")
	t.Setenv("TEMPO_ARGS_FILE", logFile)

	if err := NewNotifier().Notify("tempo", "计时结束", 10*time.Second); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("读取参数记录: %v", err)
	}
	got := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	want := []string{"-i", "clock", "-a", "tempo", "-u", "normal", "-t", "10000", "tempo", "计时结束"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notify-send 参数 = %v, want %v", got, want)
	}
}

// TestNotifyUsesDefaultTimeoutWhenDurationNotPositive 验证 duration 非正或按秒
// 取整为 0（不足 1 秒）时，-t 采用默认 30 秒（300000ms 语义下的 30000ms）。
func TestNotifyUsesDefaultTimeoutWhenDurationNotPositive(t *testing.T) {
	writeFakeNotifySend(t, `printf '%s\n' "$@" >> "$TEMPO_ARGS_FILE"
`)

	for _, tc := range []struct {
		name     string
		duration time.Duration
	}{
		{"zero", 0},
		{"negative", -time.Second},
		{"subSecond", 500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logFile := filepath.Join(t.TempDir(), "args.txt")
			t.Setenv("TEMPO_ARGS_FILE", logFile)

			if err := NewNotifier().Notify("tempo", "计时结束", tc.duration); err != nil {
				t.Fatalf("Notify: %v", err)
			}

			data, err := os.ReadFile(logFile)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			want := []string{"-i", "clock", "-a", "tempo", "-u", "normal", "-t", "30000", "tempo", "计时结束"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("notify-send 参数 = %v, want %v（duration=%v 应取默认 30 秒）",
					got, want, tc.duration)
			}
		})
	}
}

func TestNotifyReturnsErrorWhenCommandFails(t *testing.T) {
	writeFakeNotifySend(t, "echo boom >&2\nexit 1\n")

	err := NewNotifier().Notify("tempo", "x", time.Second)
	if err == nil {
		t.Fatal("notify-send 失败时应返回错误")
	}
	if !strings.Contains(err.Error(), "notify-send") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("错误应包含命令名与输出，得到: %v", err)
	}
}
