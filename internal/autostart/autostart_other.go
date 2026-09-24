//go:build !windows

// Package autostart 开机自启动管理。非 Windows 平台暂不支持,返回桩错误。
package autostart

import "errors"

var errUnsupported = errors.New("当前平台不支持开机自启动")

// Enable 非 Windows 平台不支持。
func Enable() error { return errUnsupported }

// Disable 非 Windows 平台不支持。
func Disable() error { return errUnsupported }
