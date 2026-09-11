package fetcher

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOpenCodeGoFetcher_EmptySession_ReturnsError 验证空 sessionToken 返回错误。
func TestOpenCodeGoFetcher_EmptySession_ReturnsError(t *testing.T) {
	f := NewOpenCodeGoFetcher("ws-123", "")
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error for empty session token")
	}
	if result.Platform != "OpenCode Go" {
		t.Errorf("expected platform 'OpenCode Go', got '%s'", result.Platform)
	}
}

// TestOpenCodeGoFetcher_302_ReturnsSessionExpired 验证 302 跳转返回"会话已过期"。
func TestOpenCodeGoFetcher_302_ReturnsSessionExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://opencode.ai/login")
		w.WriteHeader(302)
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-123", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "会话已过期") {
		t.Errorf("expected '会话已过期', got '%s'", result.Error)
	}
}

// TestOpenCodeGoFetcher_303_ReturnsSessionExpired 验证 303 跳转也返回"会话已过期"。
func TestOpenCodeGoFetcher_303_ReturnsSessionExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://opencode.ai/login")
		w.WriteHeader(303)
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-123", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "会话已过期") {
		t.Errorf("expected '会话已过期', got '%s'", result.Error)
	}
}

// TestOpenCodeGoFetcher_401_ReturnsInvalidCredentials 验证 401 返回"凭据无效"。
func TestOpenCodeGoFetcher_401_ReturnsInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-123", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "凭据无效") {
		t.Errorf("expected '凭据无效', got '%s'", result.Error)
	}
}

// TestOpenCodeGoFetcher_403_ReturnsInvalidCredentials 验证 403 返回"凭据无效"。
func TestOpenCodeGoFetcher_403_ReturnsInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-123", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "凭据无效") {
		t.Errorf("expected '凭据无效', got '%s'", result.Error)
	}
}

// TestOpenCodeGoFetcher_404_ReturnsWorkspaceNotFound 验证 404 返回"Workspace 不存在"。
func TestOpenCodeGoFetcher_404_ReturnsWorkspaceNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-999", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "Workspace 不存在") {
		t.Errorf("expected 'Workspace 不存在', got '%s'", result.Error)
	}
}

// TestOpenCodeGoFetcher_SSRData_PicksHighestPercent 验证 SSR 数据解析正确,选择最高 usagePercent 的窗口。
func TestOpenCodeGoFetcher_SSRData_PicksHighestPercent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证 Cookie 和 User-Agent
		cookie := r.Header.Get("Cookie")
		if !strings.Contains(cookie, "auth=tok_abc") {
			t.Errorf("expected Cookie containing 'auth=tok_abc', got '%s'", cookie)
		}
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "Mozilla") {
			t.Errorf("expected User-Agent containing 'Mozilla', got '%s'", ua)
		}
		// 验证路径包含 workspaceID
		if !strings.Contains(r.URL.Path, "ws-456") {
			t.Errorf("expected path containing 'ws-456', got '%s'", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
<html>
<head><title>Usage</title></head>
<body>
<script>
  var data = {
    rollingUsage:$R[10]={usagePercent:7,resetInSec:18000},
    weeklyUsage:$R[11]={usagePercent:2,resetInSec:540000},
    monthlyUsage:$R[12]={usagePercent:16,resetInSec:2480000}
  };
</script>
</body>
</html>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-456", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 16 {
		t.Errorf("expected Percent=16 (monthly highest), got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "月窗口") {
		t.Errorf("expected '月窗口' in Remaining, got '%s'", result.Remaining)
	}
	if !strings.Contains(result.Remaining, "5小时窗口") {
		t.Errorf("expected '5小时窗口' also shown in Remaining, got '%s'", result.Remaining)
	}
	if result.Total != 100 {
		t.Errorf("expected Total=100, got %f", result.Total)
	}
	// ResetAt 应为将来时间
	_, err := time.Parse(time.RFC3339, result.ResetAt)
	if err != nil {
		t.Errorf("expected valid ISO time in ResetAt, got '%s': %v", result.ResetAt, err)
	}
}

// TestOpenCodeGoFetcher_SSRData_TiebreakerMonthlyWins 验证相同百分比时 rolling < weekly < monthly。
func TestOpenCodeGoFetcher_SSRData_TiebreakerMonthlyWins(t *testing.T) {
	// rolling=10%, weekly=5%, monthly=10% → monthly wins
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<script>
  rollingUsage:$R[1]={usagePercent:10,resetInSec:18000}
  weeklyUsage:$R[2]={usagePercent:5,resetInSec:540000}
  monthlyUsage:$R[3]={usagePercent:10,resetInSec:2480000}
</script>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-1", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 10 {
		t.Errorf("expected Percent=10, got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "月窗口") {
		t.Errorf("expected '月窗口' in Remaining (monthly wins), got '%s'", result.Remaining)
	}
}

// TestOpenCodeGoFetcher_SSRData_ReversedFieldOrder 验证 usagePercent 和 resetInSec 顺序调换也能解析。
func TestOpenCodeGoFetcher_SSRData_ReversedFieldOrder(t *testing.T) {
	// resetInSec 在前, usagePercent 在后
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<script>
  rollingUsage:$R[1]={resetInSec:18000,usagePercent:25}
</script>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-1", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 25 {
		t.Errorf("expected Percent=25, got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "5小时窗口") {
		t.Errorf("expected '5小时窗口' in Remaining, got '%s'", result.Remaining)
	}
}

// TestOpenCodeGoFetcher_DataSlot_Fallback 验证 SSR 匹配失败时回退到 data-slot HTML 解析(旧版紧凑结构兼容)。
func TestOpenCodeGoFetcher_DataSlot_Fallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
<html>
<body>
<div data-slot="usage-item"><span data-slot="usage-label">Rolling Usage</span><span data-slot="usage-value"><!--$-->7<!--/-->%</span></div>
<div data-slot="usage-item"><span data-slot="usage-label">Weekly Usage</span><span data-slot="usage-value"><!--$-->2<!--/-->%</span></div>
<div data-slot="usage-item"><span data-slot="usage-label">Monthly Usage</span><span data-slot="usage-value"><!--$-->16<!--/-->%</span></div>
</body>
</html>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-789", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 16 {
		t.Errorf("expected Percent=16 (monthly highest), got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "月窗口") {
		t.Errorf("expected '月窗口' in Remaining, got '%s', error=%s", result.Remaining, result.Error)
	}
}

// TestOpenCodeGoFetcher_NoData_ReturnsPageStructureChanged 验证无数据返回"页面结构已变化"。
func TestOpenCodeGoFetcher_NoData_ReturnsPageStructureChanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>Welcome to OpenCode</body></html>`))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-000", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "页面结构已变化") {
		t.Errorf("expected '页面结构已变化', got '%s'", result.Error)
	}
}

// TestOpenCodeGoFetcher_SSRData_AllZeroPercent_ReturnsZeroUsage 验证所有窗口 usagePercent=0(额度未使用)时
// 仍返回有效结果:Percent=0,并按 tiebreaker 选取 monthly 窗口。
func TestOpenCodeGoFetcher_SSRData_AllZeroPercent_ReturnsZeroUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<script>
  rollingUsage:$R[10]={status:"ok",resetInSec:1025,usagePercent:0}
  weeklyUsage:$R[11]={status:"ok",resetInSec:568284,usagePercent:0}
  monthlyUsage:$R[12]={status:"ok",resetInSec:2404927,usagePercent:0}
</script>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-1", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 0 {
		t.Errorf("expected Percent=0 (all windows unused), got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "月窗口") {
		t.Errorf("expected '月窗口' in Remaining (monthly wins tiebreaker), got '%s'", result.Remaining)
	}
}

// TestOpenCodeGoFetcher_SSRData_ZeroPercent_NotPicked 验证 usagePercent=0 的窗口在存在更高用量窗口时不被选中。
func TestOpenCodeGoFetcher_SSRData_ZeroPercent_NotPicked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<script>
  rollingUsage:$R[10]={usagePercent:0,resetInSec:18000}
  weeklyUsage:$R[11]={usagePercent:5,resetInSec:540000}
</script>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-1", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 5 {
		t.Errorf("expected Percent=5 (weekly, since rolling=0 is skipped), got %f", result.Percent)
	}
}

// TestOpenCodeGoFetcher_NewPageFormat_ParsesNestedBlocks 验证新版 Dashboard 页面结构:
// usage-item 块内含嵌套 div(usage-header/进度条),百分比夹在流式注释之间,
// reset-time 输出英文重置短语。此前紧凑单行正则匹配失败导致"页面结构已变化"。
func TestOpenCodeGoFetcher_NewPageFormat_ParsesNestedBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<!DOCTYPE html>
<html>
<body>
<div data-slot="usage-item">
  <div data-slot="usage-header">
    <span data-slot="usage-label">Rolling Usage</span>
    <span data-slot="usage-value"><!--$-->42<!--/-->%</span>
  </div>
  <div data-slot="usage-progress"><div style="width: 42%;"></div></div>
  <span data-slot="reset-time"><!--$-->Resets in <!--/-->2 hours 29 minutes</span>
</div>
<div data-slot="usage-item">
  <div data-slot="usage-header">
    <span data-slot="usage-label">Weekly Usage</span>
    <span data-slot="usage-value"><!--$-->30<!--/-->%</span>
  </div>
  <div data-slot="usage-progress"><div style="width: 30%;"></div></div>
  <span data-slot="reset-time"><!--$-->Resets in <!--/-->4 days 13 hours</span>
</div>
<div data-slot="usage-item">
  <div data-slot="usage-header">
    <span data-slot="usage-label">Monthly Usage</span>
    <span data-slot="usage-value"><!--$-->17<!--/-->%</span>
  </div>
  <div data-slot="usage-progress"><div style="width: 17%;"></div></div>
  <span data-slot="reset-time"><!--$-->Resets in <!--/-->28 days 22 hours</span>
</div>
</body>
</html>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-new", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 42 {
		t.Errorf("expected Percent=42 (rolling highest), got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "5小时窗口") {
		t.Errorf("expected '5小时窗口' in Remaining, got '%s'", result.Remaining)
	}
	if !strings.Contains(result.Remaining, "月窗口") {
		t.Errorf("expected '月窗口' in Remaining, got '%s'", result.Remaining)
	}
	// 主窗口(rolling)的重置时间应来自 reset-time 短语: 2h29m = 8940s
	wantReset := time.Now().Add(8940 * time.Second).Format("2006-01-02T15:04")
	gotReset, err := time.Parse(time.RFC3339, result.ResetAt)
	if err != nil {
		t.Errorf("expected valid ISO time in ResetAt, got '%s': %v", result.ResetAt, err)
	} else if gotReset.Format("2006-01-02T15:04") != wantReset {
		t.Errorf("expected ResetAt≈%s (rolling +8940s), got %s", wantReset, result.ResetAt)
	}
}

// TestOpenCodeGoFetcher_NewPageFormat_MissingResetTime 证明 reset-time 缺失时仍解析百分比。
func TestOpenCodeGoFetcher_NewPageFormat_MissingResetTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<div data-slot="usage-item">
  <span data-slot="usage-label">Rolling Usage</span>
  <span data-slot="usage-value"><!--$-->7<!--/-->%</span>
</div>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-1", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 7 {
		t.Errorf("expected Percent=7, got %f", result.Percent)
	}
}

// TestParseDurationToSec 覆盖 parseDurationToSec 的中/英时长短语解析。
func TestParseDurationToSec(t *testing.T) {
	cases := []struct {
		name   string
		phrase string
		want   int
	}{
		{"empty", "", 0},
		{"hours and minutes", "2 hours 29 minutes", 8940},
		{"minutes only", "45 minutes", 2700},
		{"days only", "5 days", 432000},
		{"seconds", "30 seconds", 30},
		{"weeks", "1 week", 604800},
		{"months", "1 month", 2592000},
		{"zh minutes", "55 分钟", 3300},
		{"zh days hours", "2 天 8 小时", 201600},
		{"zh days only", "3 天", 259200},
		{"zh mixed min", "1 小时 30 分", 5400},
		{"zh weeks", "2 周", 1209600},
		{"unrecognized", "soon", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseDurationToSec(tc.phrase); got != tc.want {
				t.Errorf("parseDurationToSec(%q) = %d, want %d", tc.phrase, got, tc.want)
			}
		})
	}
}

// TestOpenCodeGoFetcher_RealPageFormat_ZhLocale 复刻 2026-09 真实页面结构:
// 中文 label(oc_locale=zh)、小数百分比、中文重置短语、嵌套 div 块。
func TestOpenCodeGoFetcher_RealPageFormat_ZhLocale(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<div data-slot="usage"><div data-hk="x" data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">5 小时用量</span><span data-slot="usage-value"><!--$-->13.6<!--/-->%</span></div><div data-slot="progress" role="progressbar" aria-label="5 小时用量" aria-valuemin="0" aria-valuemax="100" aria-valuenow="13.6"><div data-slot="progress-bar" style="width:13.6%"></div></div><span data-slot="reset-time"><!--$-->重置于<!--/--> <!--$-->55 分钟<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">每周用量</span><span data-slot="usage-value"><!--$-->40.6<!--/-->%</span></div><div data-slot="progress" role="progressbar" aria-label="每周用量" aria-valuenow="40.6"><div data-slot="progress-bar" style="width:40.6%"></div></div><span data-slot="reset-time"><!--$-->重置于<!--/--> <!--$-->2 天 8 小时<!--/--></span></div><div data-slot="usage-item"><div data-slot="usage-header"><span data-slot="usage-label">每月用量</span><span data-slot="usage-value"><!--$-->70.4<!--/-->%</span></div><div data-slot="progress" role="progressbar" aria-label="每月用量" aria-valuenow="70.4"><div data-slot="progress-bar" style="width:70.4%"></div></div><span data-slot="reset-time"><!--$-->重置于<!--/--> <!--$-->11 天 23 小时<!--/--></span></div></div>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-real", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 70.4 {
		t.Errorf("expected Percent=70.4 (monthly highest), got %v", result.Percent)
	}
	if !strings.Contains(result.Remaining, "5小时窗口") {
		t.Errorf("expected '5小时窗口' in Remaining, got '%s'", result.Remaining)
	}
	if !strings.Contains(result.Remaining, "已用 13.6%") {
		t.Errorf("expected '已用 13.6%%' (decimal) in Remaining, got '%s'", result.Remaining)
	}
	// 主窗口(monthly)重置短语 "11 天 23 小时" = 11*86400+23*3600 = 1033200s
	gotReset, err := time.Parse(time.RFC3339, result.ResetAt)
	if err != nil {
		t.Errorf("expected valid ISO time in ResetAt, got '%s': %v", result.ResetAt, err)
	} else {
		wantReset := time.Now().Add(1033200 * time.Second)
		if diff := gotReset.Sub(wantReset).Seconds(); diff < -5 || diff > 5 {
			t.Errorf("expected ResetAt≈%v (monthly +1033200s), got %s", wantReset.Format(time.RFC3339), result.ResetAt)
		}
	}
}

// TestOpenCodeGoFetcher_SSRData_DecimalPercent 验证 SSR hydration 小数百分比解析。
func TestOpenCodeGoFetcher_SSRData_DecimalPercent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		html := `<script>
  rollingUsage:$R[34]={status:"ok",resetInSec:3343,usagePercent:13.6,usage:163280342,limit:1200000000},
  weeklyUsage:$R[35]={status:"ok",resetInSec:202863,usagePercent:40.6,usage:1216622326,limit:3000000000},
  monthlyUsage:$R[36]={status:"ok",resetInSec:1035425,usagePercent:70.4,usage:4221595316,limit:6000000000}
</script>`
		w.Write([]byte(html))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-1", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent != 70.4 {
		t.Errorf("expected Percent=70.4 (monthly highest), got %v", result.Percent)
	}
}

// TestOpenCodeGoFetcher_HTTP500_ReturnsStatusCode 验证 500 返回通用错误。
func TestOpenCodeGoFetcher_HTTP500_ReturnsStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	f := NewOpenCodeGoFetcher("ws-123", "tok_abc")
	f.baseURL = server.URL
	result := f.Fetch()
	expected := fmt.Sprintf("HTTP %d", 500)
	if result.Error != expected {
		t.Errorf("expected '%s', got '%s'", expected, result.Error)
	}
}
