package fetcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// GLMFetcher 通过智谱 GLM 开放平台监控接口查询 Coding Plan 用量。
// 端点: GET https://bigmodel.cn/api/monitor/usage/quota/limit
// 认证: authorization: <token>(浏览器 F12 复制),可选 bigmodel-organization / bigmodel-project 头。
//
// 响应 data.limits 为多窗口额度列表(unit=3 小时窗口 / unit=6 月窗口),
// 其中 usage 为窗口总额度,remaining 为剩余,used = usage - remaining。
type GLMFetcher struct {
	token   string
	org     string
	project string
	apiURL  string // 可为空,默认为线上端点(便于测试覆盖)
}

func NewGLMFetcher(token, org, project string) *GLMFetcher {
	return &GLMFetcher{token: token, org: org, project: project}
}

// glmQuotaResponse 对应 GET /api/monitor/usage/quota/limit 的响应。
type glmQuotaResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Limits []glmLimit `json:"limits"`
		Level  string     `json:"level"`
	} `json:"data"`
	Success bool `json:"success"`
}

type glmLimit struct {
	Type          string `json:"type"`
	Unit          int    `json:"unit"`
	Number        int    `json:"number"`
	Usage         int64  `json:"usage"`         // 窗口总额度
	CurrentValue  int64  `json:"currentValue"`
	Remaining     int64  `json:"remaining"`
	Percentage    int    `json:"percentage"`
	NextResetTime int64  `json:"nextResetTime"` // 毫秒时间戳
}

// glmUnitNames unit 枚举 → 窗口标签时间单位名(未知 unit 回退为 "窗口")。
var glmUnitNames = map[int]string{
	3: "小时",
	6: "个月",
}

// glmHourUnits 小时级窗口(unit=3),相同百分比时优先展示。
var glmHourUnits = map[int]bool{3: true}

// glmWindowLabel 生成窗口标签,如 "5小时" / "1个月"。
func glmWindowLabel(number, unit int) string {
	name, ok := glmUnitNames[unit]
	if !ok {
		return "窗口"
	}
	return fmt.Sprintf("%d%s", number, name)
}

func (f *GLMFetcher) Fetch() QuotaResult {
	result := QuotaResult{
		Platform:    "GLM",
		LastUpdated: time.Now(),
	}

	if f.token == "" {
		result.Error = "未配置 GLM Token"
		return result
	}

	url := f.apiURL
	if url == "" {
		url = "https://bigmodel.cn/api/monitor/usage/quota/limit"
	}

	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		result.Error = fmt.Sprintf("创建请求失败: %v", err)
		return result
	}
	req.Header.Set("Authorization", f.token)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://bigmodel.cn/coding-plan/personal/overview")
	if f.org != "" {
		req.Header.Set("bigmodel-organization", f.org)
	}
	if f.project != "" {
		req.Header.Set("bigmodel-project", f.project)
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("请求失败: %v", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		result.Error = "Token 无效或已过期"
		return result
	}
	if resp.StatusCode != 200 {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	var body glmQuotaResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		result.Error = fmt.Sprintf("解析响应失败: %v", err)
		return result
	}
	if body.Code != 200 {
		result.Error = fmt.Sprintf("接口返回错误: %s", body.Msg)
		return result
	}

	// 收集所有窗口(5小时/月),按百分比最高选主窗口;
	// 相同百分比时小时窗口优先,与 Kimi 规则一致。
	type glmWindow struct {
		priority   int // 越小越优先(小时窗口 0,其余 1)
		label      string
		used, total, percent float64
		resetAt    string
	}
	var windows []glmWindow
	for _, l := range body.Data.Limits {
		if l.Type != "CREDIT_LIMIT" || l.Usage <= 0 {
			continue
		}
		used := float64(l.Usage - l.Remaining)
		if used < 0 {
			used = 0
		}
		var pct float64
		if l.Usage > 0 {
			pct = used / float64(l.Usage) * 100
		}
		priority := 1
		if glmHourUnits[l.Unit] {
			priority = 0
		}
		resetAt := ""
		if l.NextResetTime > 0 {
			resetAt = time.UnixMilli(l.NextResetTime).Format(time.RFC3339)
		}
		windows = append(windows, glmWindow{priority, glmWindowLabel(l.Number, l.Unit), used, float64(l.Usage), pct, resetAt})
	}

	if len(windows) == 0 {
		result.Error = "响应中未找到用量数据"
		return result
	}

	best := windows[0]
	for _, w := range windows[1:] {
		if w.percent > best.percent ||
			(w.percent == best.percent && w.priority < best.priority) {
			best = w
		}
	}
	result.Used = best.used
	result.Total = best.total
	result.Percent = best.percent
	result.ResetAt = best.resetAt
	// 全部窗口展示在 Remaining(每个窗口一行)
	var lines []string
	for _, w := range windows {
		lines = append(lines, fmt.Sprintf("%s / %s (%s)", formatNum(w.used), formatNum(w.total), w.label))
	}
	result.Remaining = strings.Join(lines, "\n")

	return result
}
