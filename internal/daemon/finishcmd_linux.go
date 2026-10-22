//go:build linux

package daemon

import "os/exec"

// runFinishShellCommand 在 Linux 经 sh -c 执行自定义结束命令（CONTEXT.md §5.4），
// 合并返回 stdout 与 stderr 供日志记录。
func runFinishShellCommand(cmd string) ([]byte, error) {
	return exec.Command("sh", "-c", cmd).CombinedOutput()
}
