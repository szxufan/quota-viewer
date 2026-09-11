//go:build !windows

package fetcher

import "os/exec"

// hideChildWindow 非 Windows 平台无控制台窗口问题,空实现。
func hideChildWindow(cmd *exec.Cmd) {}
