package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWritesToFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "tempo.log")
	logger, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	logger.Info("测试消息", "action", "test")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件: %v", err)
	}
	if !strings.Contains(string(data), "测试消息") {
		t.Errorf("日志文件应包含消息内容，得到: %q", string(data))
	}
	if !strings.Contains(string(data), "action=test") {
		t.Errorf("日志文件应包含结构化字段，得到: %q", string(data))
	}
}

func TestInitWithoutPath(t *testing.T) {
	logger, err := Init("")
	if err != nil {
		t.Fatalf(`Init("") 不应报错: %v`, err)
	}
	if logger == nil {
		t.Fatal("应返回非 nil logger")
	}
}
