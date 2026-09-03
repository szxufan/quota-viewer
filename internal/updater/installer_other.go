//go:build !windows

package updater

import (
	"os/exec"
)

// installerCmd 非 Windows 占位:NSIS 仅 Windows 支持。
// 实际升级流程在非 Windows 构建中因清单无平台条目不会触达此处。
func installerCmd(installerPath string) *exec.Cmd {
	return exec.Command(installerPath)
}
