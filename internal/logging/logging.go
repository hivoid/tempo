// Package logging 提供 slog 日志初始化，支持 stdout 与文件双写。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Init 创建同时写入 stdout 与 path 指定文件的 *slog.Logger。
// 文件父目录不存在时自动创建；文件以追加模式打开，随 daemon 生命周期存续至进程退出。
// path 为空时仅写 stdout（如由 journald 收集 stdout 的场景）。
// CLI 模式不应调用本函数：CLI 只向 stderr 输出错误，不写文件日志。
func Init(path string) (*slog.Logger, error) {
	var w io.Writer = os.Stdout
	if path != "" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建日志目录 %s: %w", dir, err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("打开日志文件 %s: %w", path, err)
		}
		w = io.MultiWriter(os.Stdout, f)
	}
	return slog.New(slog.NewTextHandler(w, nil)), nil
}
