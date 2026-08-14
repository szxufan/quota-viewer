package fetcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiMoFetcher_EmptyCookie_ReturnsError(t *testing.T) {
	f := NewMiMoFetcher("", "", "")
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error for empty cookie")
	}
	if result.Platform != "小米MiMo" {
		t.Errorf("expected platform '小米MiMo', got '%s'", result.Platform)
	}
}

func TestMiMoFetcher_401_ReturnsCookieExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	f := NewMiMoFetcher("session=abc", "", server.URL)
	result := f.Fetch()
	if !strings.Contains(result.Error, "Cookie 已过期") {
		t.Errorf("expected 'Cookie 已过期', got '%s'", result.Error)
	}
}

func TestMiMoFetcher_302_ReturnsCookieExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://platform.xiaomimimo.com/login")
		w.WriteHeader(302)
	}))
	defer server.Close()

	f := NewMiMoFetcher("session=abc", "", server.URL)
	result := f.Fetch()
	if !strings.Contains(result.Error, "Cookie 已过期") {
		t.Errorf("expected 'Cookie 已过期', got '%s'", result.Error)
	}
}

func TestMiMoFetcher_JSONResponse_CreditsFields_ParsesUsage(t *testing.T) {
	// 响应字段名尚未确认,使用常见命名 usedCredits/totalCredits
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 验证 Accept header 是 application/json
		accept := r.Header.Get("Accept")
		if accept != "application/json" {
			t.Errorf("expected Accept 'application/json', got '%s'", accept)
		}
		// 验证 Referer
		ref := r.Header.Get("Referer")
		if ref != "https://platform.xiaomimimo.com/console/plan-manage" {
			t.Errorf("expected Referer 'https://platform.xiaomimimo.com/console/plan-manage', got '%s'", ref)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"code": 0,
			"message": "",
			"data": {
				"usage": {
					"percent": 0.22,
					"items": [
						{"name": "plan_total_token", "used": 8331114938, "limit": 38000000000, "percent": 0.22},
						{"name": "compensation_total_token", "used": 0, "limit": 0, "percent": 0}
					]
				}
			}
		}`))
	}))
	defer server.Close()

	f := NewMiMoFetcher("session=abc", "", server.URL)
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Used != 8331114938 {
		t.Errorf("expected Used=8331114938, got %f", result.Used)
	}
	if result.Total != 38000000000 {
		t.Errorf("expected Total=38000000000, got %f", result.Total)
	}
	// 8331114938/38000000000 ≈ 21.9%
	if result.Percent < 21.8 || result.Percent > 22.0 {
		t.Errorf("expected ~21.9%%, got %f%%", result.Percent)
	}
	if !strings.Contains(result.Remaining, "Credits") {
		t.Errorf("expected 'Credits' in Remaining, got '%s'", result.Remaining)
	}
}

func TestMiMoFetcher_JSONResponse_MonthUsage_FallbackPercent(t *testing.T) {
	// 当 items 为空时,用顶层 percent 兜底
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"code": 0,
			"data": {
				"usage": {
					"percent": 0.35,
					"items": []
				}
			}
		}`))
	}))
	defer server.Close()

	f := NewMiMoFetcher("session=abc", "", server.URL)
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Percent < 34.9 || result.Percent > 35.1 {
		t.Errorf("expected ~35%%, got %f%%", result.Percent)
	}
}

func TestMiMoFetcher_NoUsageData_FallsBackToBalance(t *testing.T) {
	// usage 无有效数据时,回退查询 /api/v1/balance 并像 DeepSeek 一样展示余额
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"usage":{"percent":0,"items":[]}}}`))
	}))
	defer usageServer.Close()

	balanceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/balance" {
			t.Errorf("expected balance path, got '%s'", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"balance":"247.51","cashBalance":"200","giftBalance":"47.51","currency":"CNY"}}`))
	}))
	defer balanceServer.Close()

	f := NewMiMoFetcher("session=abc", "", usageServer.URL)
	f.balanceURL = balanceServer.URL + "/api/v1/balance"
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Kind != KindBalance {
		t.Errorf("expected Kind=balance, got '%s'", result.Kind)
	}
	if result.Balance != 247.51 {
		t.Errorf("expected Balance=247.51, got %f", result.Balance)
	}
	if result.Currency != "CNY" {
		t.Errorf("expected Currency=CNY, got '%s'", result.Currency)
	}
	if !strings.Contains(result.Remaining, "余额") || !strings.Contains(result.Remaining, "247.51") {
		t.Errorf("expected balance display in Remaining, got '%s'", result.Remaining)
	}
}

func TestMiMoFetcher_BalanceNumericField_AlsoParses(t *testing.T) {
	// balance 字段也可能是数字形式,应同样兼容
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"usage":{"percent":0,"items":[]}}}`))
	}))
	defer usageServer.Close()

	balanceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"balance":100,"currency":"CNY"}}`))
	}))
	defer balanceServer.Close()

	f := NewMiMoFetcher("session=abc", "", usageServer.URL)
	f.balanceURL = balanceServer.URL + "/api/v1/balance"
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Balance != 100 {
		t.Errorf("expected Balance=100, got %f", result.Balance)
	}
	if !strings.Contains(result.Remaining, "100") {
		t.Errorf("expected '100' in Remaining, got '%s'", result.Remaining)
	}
}

func TestMiMoFetcher_NoUsageData_BalanceFails_ReturnsError(t *testing.T) {
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"usage":{"percent":0,"items":[]}}}`))
	}))
	defer usageServer.Close()

	balanceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer balanceServer.Close()

	f := NewMiMoFetcher("session=abc", "", usageServer.URL)
	f.balanceURL = balanceServer.URL + "/api/v1/balance"
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error when usage has no data and balance fails")
	}
}

// startMiMoSTSChain 启动模拟小米 STS 换取链路的测试服务器:
// genLoginUrl 302 -> serviceLogin(校验小米账号 Cookie)302 -> sts(下发 MiMo Cookie)。
func startMiMoSTSChain(t *testing.T) {
	t.Helper()
	stsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sts" {
			t.Errorf("expected /sts, got %s", r.URL.Path)
		}
		w.Header().Add("Set-Cookie", `api-platform_serviceToken="tok123+/="; Version=1; Domain=platform.xiaomimimo.com; Path=/; HttpOnly`)
		w.Header().Add("Set-Cookie", "userId=2398921322; Domain=xiaomimimo.com; Path=/")
		w.Header().Add("Set-Cookie", `api-platform_slh="slh1"; Version=1; Domain=platform.xiaomimimo.com; Path=/`)
		w.Header().Add("Set-Cookie", "api-platform_ph=ph1; Version=1; Domain=platform.xiaomimimo.com; Path=/")
		w.Header().Set("Location", "https://platform.xiaomimimo.com/console/balance?userId=2398921322")
		w.WriteHeader(302)
	}))
	t.Cleanup(stsServer.Close)

	loginServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "passToken=pt") {
			t.Errorf("expected xiaomi cookie, got %q", r.Header.Get("Cookie"))
		}
		w.Header().Set("Location", stsServer.URL+"/sts?sign=s&auth=a")
		w.WriteHeader(302)
	}))
	t.Cleanup(loginServer.Close)

	genServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/genLoginUrl" {
			t.Errorf("expected genLoginUrl, got %s", r.URL.Path)
		}
		w.Header().Set("Location", loginServer.URL+"/pass/serviceLogin?callback=cb&sid=api-platform")
		w.WriteHeader(302)
	}))
	t.Cleanup(genServer.Close)

	old := mimoGenLoginURL
	mimoGenLoginURL = genServer.URL + "/api/v1/genLoginUrl"
	t.Cleanup(func() { mimoGenLoginURL = old })
}

func TestMiMoFetcher_XiaomiCookieOnly_ExchangesAndFetches(t *testing.T) {
	// 只配置小米账号 Cookie:应先自动换取 MiMo Cookie 再查询
	startMiMoSTSChain(t)

	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验请求携带的是换取出的新 MiMo Cookie
		if !strings.Contains(r.Header.Get("Cookie"), "api-platform_serviceToken=tok123+/=") {
			t.Errorf("expected new mimo cookie, got %q", r.Header.Get("Cookie"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"usage":{"percent":0.2,"items":[{"name":"plan_total_token","used":1,"limit":10,"percent":0.2}]}}}`))
	}))
	defer usageServer.Close()

	f := NewMiMoFetcher("", "passToken=pt", usageServer.URL)
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Total != 10 {
		t.Errorf("expected Total=10, got %f", result.Total)
	}
	// 换取出的新 Cookie 应上报,供上层写回配置
	got := result.UpdatedCreds["cookie"]
	if !strings.Contains(got, "api-platform_serviceToken=tok123+/=") || !strings.Contains(got, "userId=2398921322") {
		t.Errorf("expected new cookie in UpdatedCreds, got %q", got)
	}
}

func TestMiMoFetcher_ExpiredCookie_RefreshesViaXiaomi(t *testing.T) {
	// MiMo Cookie 失效(401)时,应自动用小米账号 Cookie 换取新 Cookie 并重试
	startMiMoSTSChain(t)

	calls := 0
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.Header.Get("Cookie"), "serviceToken=old") {
			w.WriteHeader(401) // 旧 Cookie 拒绝
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":0,"data":{"usage":{"percent":0.1,"items":[{"name":"plan_total_token","used":1,"limit":10,"percent":0.1}]}}}`))
	}))
	defer usageServer.Close()

	f := NewMiMoFetcher("serviceToken=old", "passToken=pt", usageServer.URL)
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if calls < 2 {
		t.Errorf("expected retry after refresh, calls=%d", calls)
	}
	if got := result.UpdatedCreds["cookie"]; !strings.Contains(got, "api-platform_serviceToken=") {
		t.Errorf("expected refreshed cookie in UpdatedCreds, got %q", got)
	}
}

func TestMiMoFetcher_InvalidXiaomiCookie_ReturnsError(t *testing.T) {
	// 小米账号 Cookie 失效时 serviceLogin 返回登录页(200)而非跳转
	loginServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer loginServer.Close()

	genServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", loginServer.URL+"/pass/serviceLogin?callback=cb")
		w.WriteHeader(302)
	}))
	defer genServer.Close()

	old := mimoGenLoginURL
	mimoGenLoginURL = genServer.URL + "/api/v1/genLoginUrl"
	defer func() { mimoGenLoginURL = old }()

	f := NewMiMoFetcher("", "passToken=bad", "https://example.com/api")
	result := f.Fetch()
	if !strings.Contains(result.Error, "小米账号 Cookie 已失效") {
		t.Errorf("expected xiaomi cookie expired error, got %q", result.Error)
	}
	if len(result.UpdatedCreds) != 0 {
		t.Errorf("expected no updated creds, got %v", result.UpdatedCreds)
	}
}
