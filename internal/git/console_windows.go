//go:build windows

package git

import (
	"os/exec"
	"syscall"
)

// hideConsole 阻止 git 子进程弹出控制台黑框：GUI 应用（windowsgui）没有
// 自己的控制台，默认行为会给每个子进程新建一个控制台窗口。
// CREATE_NO_WINDOW 同时抑制 shim 类包装器的窗口。
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
