package fetcher

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// OpenCodeGoFetcher 通过抓取 OpenCode Dashboard 页面获取 Go 套餐的滚动窗口额度百分比。
// 端点: GET https://opencode.ai/workspace/{workspaceID}/go
// 认证: auth Cookie(与 Xfyun/MiMo 相同通过 Cookie 头传递)
type OpenCodeGoFetcher struct {
	workspaceID  string
	sessionToken string
	baseURL      string // 可重写,用于测试
}

// NewOpenCodeGoFetcher 创建一个新的 OpenCodeGoFetcher。
func NewOpenCodeGoFetcher(workspaceID string, sessionToken string) *OpenCodeGoFetcher {
	return &OpenCodeGoFetcher{
		workspaceID:  workspaceID,
		sessionToken: sessionToken,
		baseURL:      "https://opencode.ai",
	}
}

// windowInfo 描述一个配额窗口。
type windowInfo struct {
	windowType   string  // "rolling", "weekly", "monthly"
	usagePercent float64 // 页面已输出小数百分比(如 13.6)
	resetInSec   int
}

// windowTypeOrder 用于 tiebreaker: 相同百分比时 rolling < weekly < monthly(monthly 优先级最高)。
var windowTypeOrder = map[string]int{
	"rolling": 2,
	"weekly":  1,
	"monthly": 0,
}

// windowLabel 窗口类型的中文描述。
func windowLabel(wt string) string {
	switch wt {
	case "rolling":
		return "5小时窗口"
	case "weekly":
		return "周窗口"
	case "monthly":
		return "月窗口"
	}
	return wt
}

// ssrPattern 匹配内嵌在 HTML 中的 SSR hydration 配额数据。
// 格式: rollingUsage:$R[10]={usagePercent:7,resetInSec:18000}
var ssrPattern = regexp.MustCompile(`(rolling|weekly|monthly)Usage:\$R\[\d+\]=\{([^}]+)\}`)

// slotNameMap 将 data-slot label 映射到内部窗口类型。
// 页面 label 随 oc_locale 变化: 英文 "Rolling Usage"/"Weekly Usage"/"Monthly Usage",
// 中文 "5 小时用量"/"每周用量"/"每月用量",其他语言回退 aria/进度条结构;
// 此处收录已确认的中英文本,均按包含关键词匹配(见 slotWindowType)。
var slotNameMap = map[string]string{
	"rolling": "rolling",
	"weekly":  "weekly",
	"monthly": "monthly",
	"小时用量":   "rolling", // "5 小时用量"
	"每周用量":   "weekly",
	"每周":     "weekly",
	"每月用量":   "monthly",
	"每月":     "monthly",
}

// slotWindowType 依据 label 文本判断窗口类型(小写化后做关键词匹配)。
// 匹配顺序: 精确 → 前缀/包含。中文 label 无词边界,必须用包含匹配。
func slotWindowType(label string) (string, bool) {
	lower := strings.ToLower(strings.TrimSpace(label))
	if wt, ok := slotNameMap[lower]; ok {
		return wt, true
	}
	// 中文优先匹配多字关键词,避免 "每周" 误吞 "每小时" 之类(当前页面无此冲突)
	for _, kw := range []struct{ key, wt string }{
		{"rolling", "rolling"},
		{"5 小时", "rolling"},
		{"5小时", "rolling"},
		{"weekly", "weekly"},
		{"每周", "weekly"},
		{"monthly", "monthly"},
		{"每月", "monthly"},
	} {
		if strings.Contains(lower, kw.key) {
			return kw.wt, true
		}
	}
	return "", false
}

// slotItemStartRe 定位新版页面 data-slot="usage-item" 块的起始标签。
var slotItemStartRe = regexp.MustCompile(`<div[^>]*data-slot="usage-item"`)

// slotLabelRe 在块内匹配 data-slot="usage-label" 的文本(label 中的 HTML 注释先剥离)。
var slotLabelRe = regexp.MustCompile(`data-slot="usage-label"[^>]*>([^<]+)<`)

// slotValueRe 在块内匹配 data-slot="usage-value" 的百分比
// (允许 SolidStart 流式注释 <!--$-->…<!--/--> 夹在数字两侧;新版为小数,如 13.6)。
var slotValueRe = regexp.MustCompile(`data-slot="usage-value"[\s\S]*?<!--\$-->\s*(\d+(?:\.\d+)?)\s*<!--\/-->`)

// slotResetRe 在块内捕获 data-slot="reset-time" 的完整文本内容
// (新版页面不再内嵌 resetInSec 数字,而是 "Resets in 2 hours 29 minutes" 这类文本;
// SolidStart 流式注释 <!--$-->/<!--/--> 可能出现在任意位置,捕获后统一剥离)。
var slotResetRe = regexp.MustCompile(`data-slot="reset-time"[^>]*>([\s\S]*?)</span>`)

// htmlCommentRe 匹配 SolidStart SSR 流式注释。
var htmlCommentRe = regexp.MustCompile(`<!--[\s\S]*?-->`)

func (f *OpenCodeGoFetcher) Fetch() QuotaResult {
	result := QuotaResult{
		Platform:    "OpenCode Go",
		Total:       100,
		LastUpdated: time.Now(),
	}

	if f.sessionToken == "" {
		result.Error = "未配置 OpenCode Go Cookie"
		return result
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Timeout: 10 * time.Second,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	url := fmt.Sprintf("%s/workspace/%s/go", f.baseURL, f.workspaceID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		result.Error = fmt.Sprintf("创建请求失败: %v", err)
		return result
	}

	req.Header.Set("Cookie", "auth="+f.sessionToken)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("请求失败: %v", err)
		return result
	}
	defer resp.Body.Close()

	// 错误分类
	switch resp.StatusCode {
	case 302, 303:
		result.Error = "会话已过期"
		return result
	case 401, 403:
		result.Error = "凭据无效"
		return result
	case 404:
		result.Error = "Workspace 不存在"
		return result
	}

	if resp.StatusCode != 200 {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Error = fmt.Sprintf("读取响应失败: %v", err)
		return result
	}

	// 1) 尝试 SSR hydration 数据
	windows := parseSSRWindows(string(body))
	if len(windows) == 0 {
		// 2) 回退到 data-slot HTML 解析
		windows = parseSlotWindows(string(body))
	}

	if len(windows) == 0 {
		result.Error = "页面结构已变化"
		return result
	}

	// 主窗口: usagePercent 最高的窗口; 相同则 rolling > weekly > monthly
	best := windows[0]
	for _, w := range windows[1:] {
		if w.usagePercent > best.usagePercent ||
			(w.usagePercent == best.usagePercent && windowTypeOrder[w.windowType] < windowTypeOrder[best.windowType]) {
			best = w
		}
	}

	result.Percent = best.usagePercent
	// 所有解析到的窗口全部展示
	var lines []string
	for _, w := range windows {
		lines = append(lines, fmt.Sprintf("%s · 已用 %s%% · 剩余 %s%%",
			windowLabel(w.windowType), formatPercent(w.usagePercent), formatPercent(100-w.usagePercent)))
	}
	result.Remaining = strings.Join(lines, "\n")
	result.ResetAt = time.Now().Add(time.Duration(best.resetInSec) * time.Second).Format(time.RFC3339)

	return result
}

// parseSSRWindows 从 HTML 中解析 SSR hydration 配额窗口数据。
// usagePercent=0(额度未使用)也是有效窗口,一并返回;仅当字段缺失时跳过。
func parseSSRWindows(html string) []windowInfo {
	matches := ssrPattern.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		return nil
	}

	var windows []windowInfo
	for _, m := range matches {
		wt := m[1]
		inner := m[2]
		percent, resetSec, hasPercent := parseSSRFields(inner)
		if !hasPercent {
			continue // 无 usagePercent 字段 = 结构异常,跳过
		}
		windows = append(windows, windowInfo{
			windowType:   wt,
			usagePercent: percent,
			resetInSec:   resetSec,
		})
	}
	return windows
}

// parseSSRFields 解析 SSR 窗口数据内部的 key=value 对。
// 内部格式: status:"ok",usagePercent:13.6,resetInSec:3343 (顺序不固定)
// usagePercent 已为小数;usage/limit 为字节数,忽略。
// 返回 hasPercent: 是否存在 usagePercent 字段(区分真实 0% 与字段缺失)。
func parseSSRFields(inner string) (percent float64, resetSec int, hasPercent bool) {
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "usagePercent:") {
			percent, _ = strconv.ParseFloat(strings.TrimPrefix(part, "usagePercent:"), 64)
			hasPercent = true
		} else if strings.HasPrefix(part, "resetInSec:") {
			resetSec, _ = strconv.Atoi(strings.TrimPrefix(part, "resetInSec:"))
		}
	}
	return
}

// parseSlotWindows 从 HTML data-slot 结构中解析配额窗口数据(SSR 解析失败时的备选)。
// 新版页面每个窗口渲染为 <div data-slot="usage-item"> 块,块内含嵌套 div
// (usage-header/进度条等),因此按"下一个 usage-item 起始标签"切块再在块内匹配,
// 而非假设单行紧凑结构。解析不到 resetInSec 时回退为 0(未知)。
func parseSlotWindows(html string) []windowInfo {
	var starts []int
	for _, loc := range slotItemStartRe.FindAllStringIndex(html, -1) {
		starts = append(starts, loc[0])
	}
	if len(starts) == 0 {
		return nil
	}

	var windows []windowInfo
	for i, start := range starts {
		end := len(html)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		block := html[start:end]

		labelMatch := slotLabelRe.FindStringSubmatch(block)
		valueMatch := slotValueRe.FindStringSubmatch(block)
		if labelMatch == nil || valueMatch == nil {
			continue
		}
		label := htmlCommentRe.ReplaceAllString(labelMatch[1], "")
		wt, ok := slotWindowType(label)
		if !ok {
			continue
		}
		percent, _ := strconv.ParseFloat(strings.TrimSpace(valueMatch[1]), 64)
		windows = append(windows, windowInfo{
			windowType:   wt,
			usagePercent: percent,
			resetInSec:   parseSlotResetInSec(block),
		})
	}
	return windows
}

// formatPercent 格式化百分比: 整数不带小数点,小数保留一位(如 13 → "13", 13.6 → "13.6")。
func formatPercent(p float64) string {
	if p == float64(int(p)) {
		return strconv.Itoa(int(p))
	}
	return strconv.FormatFloat(p, 'f', 1, 64)
}

// parseSlotResetInSec 从块的 reset-time 文本短语解析秒数。
// 中文(oc_locale=zh): "重置于 55 分钟"/"重置于 2 天 8 小时";
// 英文: "Resets in 2 hours 29 minutes"。识别失败返回 0 表示未知。
// SolidStart 流式注释 <!--$-->/<!--/--> 可能出现在任意位置,先剥离再解析。
func parseSlotResetInSec(block string) int {
	m := slotResetRe.FindStringSubmatch(block)
	if m == nil {
		return 0
	}
	text := htmlCommentRe.ReplaceAllString(m[1], " ")
	// 剥掉 "重置于" / "Resets in" 前缀,保留纯时长短语
	if idx := strings.Index(text, "重置于"); idx >= 0 {
		text = text[idx+len("重置于"):]
	} else if idx := strings.Index(strings.ToLower(text), "resets in"); idx >= 0 {
		text = strings.ToLower(text)[idx+len("resets in"):]
	}
	phrase := strings.Join(strings.Fields(text), " ")
	return parseDurationToSec(phrase)
}

// durationUnitsRe 匹配"数字+单位"片段;中英文单位均收录,大小写不敏感靠预处理。
var durationUnitsRe = regexp.MustCompile(`(\d+)\s*(秒|分钟|分|小时|时|天|日|周|星期|个月|个月份|月|年|seconds?|mins?|minutes?|hours?|days?|weeks?|months?|years?)`)

// durationUnitSec 单位到秒数的映射(月按 30 天粗略估算)。
var durationUnitSec = map[string]int{
	"秒":       1,
	"分":       60,
	"分钟":      60,
	"时":       3600,
	"小时":      3600,
	"天":       86400,
	"日":       86400,
	"周":       604800,
	"星期":      604800,
	"月":       2592000,
	"年":       31536000,
	"second":  1,
	"sec":     1,
	"min":     60,
	"minute":  60,
	"hour":    3600,
	"day":     86400,
	"week":    604800,
	"month":   2592000,
	"year":    31536000,
}

// parseDurationToSec 解析中/英时长短语为秒数。
// 例: "2 hours 29 minutes"→8940, "55 分钟"→3300, "2 天 8 小时"→208800。
// 无法识别任何"数字+单位"片段时返回 0。
func parseDurationToSec(phrase string) int {
	if phrase == "" {
		return 0
	}
	total, matched := 0, false
	for _, m := range durationUnitsRe.FindAllStringSubmatch(phrase, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		unit := strings.TrimSuffix(strings.ToLower(m[2]), "s")
		sec, ok := durationUnitSec[unit]
		if !ok {
			continue
		}
		total += n * sec
		matched = true
	}
	if !matched {
		return 0
	}
	return total
}
