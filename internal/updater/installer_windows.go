//go:build windows

package updater

import (
	"os/exec"
	"syscall"
)

// installerCmd 构造 NSIS 安装器静默安装命令。
// 分离运行:隐藏窗口、不继承本进程句柄,本进程退出不影响安装器继续安装。
func installerCmd(installerPath string) *exec.Cmd {
	cmd := exec.Command(installerPath, "/S")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
