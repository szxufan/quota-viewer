package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// BailianFetcher 通过百炼 CLI(bl) 的 usage token-plan 命令查询 Token Plan 用量。
// 端点: bl usage token-plan --output json
// 认证: CLI 全局登录态(bl auth login --console 或 bl auth login --config token-plan --api-key sk-sp-xxx),
// 命令本身不支持按次注入 API Key,因此凭证字段只有可选的 bl 命令路径。
//
// 响应(stdout JSON):{"per5HourPercentage":0.1,"per5HourResetTime":1789696860000,
//
//	"per1WeekPercentage":0.05,"per1WeekResetTime":1789696860000}
//
// Percentage ∈ [0,1] 为已用比例;ResetTime 为毫秒时间戳;四个字段均可选
// (缺失 = 无数据,可能额度无限)。
type BailianFetcher struct {
	execPath string // bl 可执行路径(空 = 从 PATH 查找)
}

func NewBailianFetcher(execPath string) *BailianFetcher {
	return &BailianFetcher{execPath: execPath}
}

// bailianUsage 对应 bl usage token-plan --output json 的输出;字段均为指针,缺失 = 无数据。
type bailianUsage struct {
	Per5HourPercentage *float64 `json:"per5HourPercentage"`
	Per5HourResetTime  *int64   `json:"per5HourResetTime"`
	Per1WeekPercentage *float64 `json:"per1WeekPercentage"`
	Per1WeekResetTime  *int64   `json:"per1WeekResetTime"`
}

func (f *BailianFetcher) Fetch() QuotaResult {
	result := QuotaResult{
		Platform:    "百炼",
		LastUpdated: time.Now(),
	}

	// 构造命令并执行(20s 超时,容忍 Node 启动开销)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var stdout, stderr bytes.Buffer
	if err := f.run(ctx, &stdout, &stderr); err != nil {
		result.Error = err.Error()
		return result
	}

	// 解析 stdout:容忍前导告警与尾部更新提示,从首个 '{' 起 JSON 解码
	out := stdout.String()
	idx := strings.Index(out, "{")
	if idx < 0 {
		result.Error = fmt.Sprintf("解析 bl 输出失败: 未找到 JSON(%s)", truncateErr(out))
		return result
	}
	var usage bailianUsage
	if err := json.NewDecoder(strings.NewReader(out[idx:])).Decode(&usage); err != nil {
		result.Error = fmt.Sprintf("解析 bl 输出失败: %v", err)
		return result
	}

	if usage.Per5HourPercentage == nil && usage.Per1WeekPercentage == nil {
		result.Error = "响应中未找到用量数据(可能未订阅 Token Plan 或额度无限)"
		return result
	}

	// 双窗口(5小时/周):百分比最高者为主窗口;相等时 5小时窗口优先(与 Kimi/GLM 规则一致)
	type blWindow struct {
		label   string
		pct     float64 // 已用比例 [0,1]
		resetMs *int64
	}
	windows := []blWindow{}
	if usage.Per5HourPercentage != nil {
		windows = append(windows, blWindow{"5小时窗口", *usage.Per5HourPercentage, usage.Per5HourResetTime})
	}
	if usage.Per1WeekPercentage != nil {
		windows = append(windows, blWindow{"周窗口", *usage.Per1WeekPercentage, usage.Per1WeekResetTime})
	}
	best := windows[0]
	for _, w := range windows[1:] {
		if w.pct > best.pct {
			best = w
		}
	}

	result.Percent = best.pct * 100
	if best.resetMs != nil && *best.resetMs > 0 {
		result.ResetAt = time.UnixMilli(*best.resetMs).Format(time.RFC3339)
	}
	var lines []string
	for _, w := range windows {
		lines = append(lines, fmt.Sprintf("%.1f%% 已用 (%s)", w.pct*100, w.label))
	}
	result.Remaining = strings.Join(lines, "\n")

	return result
}

// run 构造并执行 bl 命令。Windows 上 npm 全局安装的 bl 是 bl.cmd shim,
// os/exec 无法直接执行,需经 cmd /c 包装;原生二进制安装为 bl.exe 可直接执行。
func (f *BailianFetcher) run(ctx context.Context, stdout, stderr *bytes.Buffer) error {
	args := []string{"usage", "token-plan", "--output", "json", "--quiet"}
	var cmd *exec.Cmd

	if runtime.GOOS == "windows" {
		if f.execPath == "" {
			if _, err := exec.LookPath("bl"); err != nil {
				return fmt.Errorf("未找到 bl 命令,请先安装百炼 CLI (npm install -g bailian-cli)")
			}
			cmd = exec.CommandContext(ctx, "cmd", append([]string{"/c", "bl"}, args...)...)
		} else {
			lp := strings.ToLower(f.execPath)
			if strings.HasSuffix(lp, ".cmd") || strings.HasSuffix(lp, ".bat") {
				cmd = exec.CommandContext(ctx, "cmd", append([]string{"/c", f.execPath}, args...)...)
			} else {
				cmd = exec.CommandContext(ctx, f.execPath, args...)
			}
		}
	} else {
		path := f.execPath
		if path == "" {
			path = "bl"
		}
		cmd = exec.CommandContext(ctx, path, args...)
	}

	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(cmd.Environ(), "NO_COLOR=1")
	// GUI 程序调用控制台子进程时 Windows 会弹出黑色命令行窗口,统一隐藏
	hideChildWindow(cmd)

	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			// 命令执行但返回非零:区分"子命令不存在"(版本过低)与其它失败
			msg := stderr.String()
			if strings.Contains(strings.ToLower(msg), "unknown command") {
				return fmt.Errorf("bl 版本过低,请升级: npm update -g bailian-cli")
			}
			return fmt.Errorf("bl 命令失败: %s", truncateErr(msg))
		}
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("bl 命令超时(20s)")
		}
		// LookPath 失败或无法启动
		return fmt.Errorf("未找到 bl 命令,请先安装百炼 CLI (npm install -g bailian-cli)")
	}
	return nil
}

// truncateErr 截断错误输出,避免超长日志撑爆展示。
func truncateErr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
