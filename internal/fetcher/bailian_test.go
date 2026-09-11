package fetcher

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeFakeCLI 在临时目录写一个假 bl 脚本并返回其路径(注入 f.execPath)。
// Windows 上写 .cmd(经 cmd /c 分支执行),其余平台写 shell 脚本 + 可执行位。
// stdout 行为由脚本输出内容决定;exit 非 0 用 exit 命令模拟。
func writeFakeCLI(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	name := "fakebl"
	if runtime.GOOS == "windows" {
		name = "fakebl.cmd"
		// cmd 脚本:@echo off 后逐行输出;参数经 %* 透传无影响
		script = "@echo off\r\n" + script
	} else {
		script = "#!/bin/sh\n" + script
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestBailian_CLINotFound(t *testing.T) {
	// execPath 指向不存在的文件
	f := NewBailianFetcher(filepath.Join(t.TempDir(), "no-such-bl.exe"))
	result := f.Fetch()
	if result.Error == "" {
		t.Fatal("expected error for missing CLI")
	}
	if !strings.Contains(result.Error, "bl") {
		t.Errorf("expected error mentioning bl, got %s", result.Error)
	}
}

func TestBailian_OK_BothWindows(t *testing.T) {
	// 5小时 50% + 周 10% → 主窗口为 5小时(50%)
	script := "echo {\"per5HourPercentage\":0.5,\"per5HourResetTime\":1789696860000,\"per1WeekPercentage\":0.1,\"per1WeekResetTime\":1789696800000}"
	f := NewBailianFetcher(writeFakeCLI(t, script))
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent < 49.9 || result.Percent > 50.1 {
		t.Errorf("expected ~50%%, got %f", result.Percent)
	}
	// Remaining 两行,各窗口一行
	lines := strings.Split(result.Remaining, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines in Remaining, got %d: %q", len(lines), result.Remaining)
	}
	if !strings.Contains(result.Remaining, "5小时窗口") || !strings.Contains(result.Remaining, "周窗口") {
		t.Errorf("expected window labels in Remaining, got %q", result.Remaining)
	}
	// ResetAt 为主窗口(5小时)毫秒时间戳的 RFC3339
	want := time.UnixMilli(1789696860000).Format(time.RFC3339)
	if result.ResetAt != want {
		t.Errorf("expected ResetAt %s, got %s", want, result.ResetAt)
	}
	if result.Platform != "百炼" {
		t.Errorf("expected Platform 百炼, got %s", result.Platform)
	}
}

func TestBailian_OK_OnlyWeek(t *testing.T) {
	script := "echo {\"per1WeekPercentage\":0.005016167,\"per1WeekResetTime\":1789696860000}"
	f := NewBailianFetcher(writeFakeCLI(t, script))
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent < 0.45 || result.Percent > 0.55 {
		t.Errorf("expected ~0.5%%, got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "周窗口") {
		t.Errorf("expected 周窗口 in Remaining, got %q", result.Remaining)
	}
	if result.ResetAt == "" {
		t.Error("expected ResetAt set")
	}
}

func TestBailian_EmptyData(t *testing.T) {
	// 空对象 = 无任何窗口数据(未订阅或额度无限)
	script := "echo {}"
	f := NewBailianFetcher(writeFakeCLI(t, script))
	result := f.Fetch()
	if !strings.Contains(result.Error, "未找到用量数据") {
		t.Errorf("expected 'no usage data' error, got %s", result.Error)
	}
}

func TestBailian_CLIError(t *testing.T) {
	// 退出码非 0 + stderr 报错
	script := "echo oops >&2" + exitScript(1)
	f := NewBailianFetcher(writeFakeCLI(t, script))
	result := f.Fetch()
	if !strings.Contains(result.Error, "bl 命令失败") {
		t.Errorf("expected CLI failure error, got %s", result.Error)
	}
}

func TestBailian_UnknownCommand(t *testing.T) {
	// 旧版本无 usage 子命令 → stderr 含 "Unknown command" → 提示升级
	script := "echo Unknown command: usage >&2" + exitScript(1)
	f := NewBailianFetcher(writeFakeCLI(t, script))
	result := f.Fetch()
	if !strings.Contains(result.Error, "升级") {
		t.Errorf("expected upgrade hint, got %s", result.Error)
	}
}

func TestBailian_BadJSON(t *testing.T) {
	// stdout 无 JSON
	script := "echo not json"
	f := NewBailianFetcher(writeFakeCLI(t, script))
	result := f.Fetch()
	if !strings.Contains(result.Error, "解析") {
		t.Errorf("expected parse error, got %s", result.Error)
	}
}

func TestBailian_ToleratesLeadingNoise(t *testing.T) {
	// stdout 前导告警行(如自动更新提示)不应阻断解析
	script := "echo warning: update available"
	if runtime.GOOS == "windows" {
		// cmd 连续两行输出
		script = "echo warning: update available\r\necho {\"per1WeekPercentage\":0.25}"
	} else {
		script = "echo warning: update available; echo '{\"per1WeekPercentage\":0.25}'"
	}
	f := NewBailianFetcher(writeFakeCLI(t, script))
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent < 24.9 || result.Percent > 25.1 {
		t.Errorf("expected ~25%%, got %f", result.Percent)
	}
}

// exitScript 生成对应平台的非零退出命令。
func exitScript(code int) string {
	if runtime.GOOS == "windows" {
		return "\r\nexit /b " + itoa(code)
	}
	return "\nexit " + itoa(code)
}

func itoa(n int) string {
	return string(rune('0' + n)) // 测试仅用个位数
}
