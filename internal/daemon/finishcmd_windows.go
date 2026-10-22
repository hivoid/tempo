//go:build windows

package daemon

import "os/exec"

// runFinishShellCommand 在 Windows 经 cmd /c 执行自定义结束命令，
// 合并返回 stdout 与 stderr 供日志记录。
func runFinishShellCommand(cmd string) ([]byte, error) {
	return exec.Command("cmd", "/c", cmd).CombinedOutput()
}
