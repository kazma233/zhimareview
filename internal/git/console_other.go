//go:build !windows

package git

import "os/exec"

// hideConsole 在非 Windows 平台无需处理：子进程不产生额外控制台窗口。
func hideConsole(cmd *exec.Cmd) {}
