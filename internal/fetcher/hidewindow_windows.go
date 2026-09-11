//go:build windows

package fetcher

import (
	"os/exec"
	"syscall"
)

// hideChildWindow 隐藏子进程的控制台窗口。
// quota-viewer 是 GUI 程序(无控制台),启动 cmd/bl 这类控制台子进程时
// Windows 会为其分配并弹出黑色命令行窗口;CREATE_NO_WINDOW 标志阻止该行为。
func hideChildWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
