//go:build windows

// Package autostart 开机自启动管理。
// Windows 实现写当前用户的 HKCU Run 注册表键,登录后由系统拉起,无需管理员权限。
package autostart

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath 当前用户登录自启动注册表键。
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// valueName 注册表值名,也是任务管理器"启动应用"里显示的条目名。
const valueName = "quota-viewer"

// Enable 写入自启动注册表值,指向当前 exe(带引号,防含空格路径被拆参)。
// 幂等覆盖写,顺带修正升级/移动安装位置后记录的旧路径。
func Enable() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取程序路径失败: %w", err)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表启动项失败: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue(valueName, `"`+exe+`"`); err != nil {
		return fmt.Errorf("写入开机自启动失败: %w", err)
	}
	return nil
}

// Disable 删除自启动注册表值。值本就不存在视为已关闭,不算错误。
func Disable() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("打开注册表启动项失败: %w", err)
	}
	defer key.Close()
	if err := key.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("删除开机自启动失败: %w", err)
	}
	return nil
}
