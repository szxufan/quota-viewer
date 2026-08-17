package fetcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGLM_EmptyToken_ReturnsError(t *testing.T) {
	f := NewGLMFetcher("", "", "")
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error for empty token")
	}
}

func TestGLM_OK_ParsesQuota(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "eyJtest" {
			t.Errorf("expected Authorization eyJtest, got %s", auth)
		}
		if org := r.Header.Get("bigmodel-organization"); org != "org-123" {
			t.Errorf("expected bigmodel-organization org-123, got %s", org)
		}
		if proj := r.Header.Get("bigmodel-project"); proj != "proj-456" {
			t.Errorf("expected bigmodel-project proj-456, got %s", proj)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"code": 200,
			"msg": "操作成功",
			"data": {
				"limits": [
					{
						"type": "CREDIT_LIMIT",
						"unit": 3,
						"number": 5,
						"usage": 28000,
						"currentValue": 0,
						"remaining": 27999,
						"percentage": 1,
						"nextResetTime": 1786953113005
					},
					{
						"type": "CREDIT_LIMIT",
						"unit": 6,
						"number": 1,
						"usage": 140000,
						"currentValue": 0,
						"remaining": 139999,
						"percentage": 1,
						"nextResetTime": 1787539506998
					}
				],
				"level": "max"
			},
			"success": true
		}`))
	}))
	defer server.Close()

	f := NewGLMFetcher("eyJtest", "org-123", "proj-456")
	f.apiURL = server.URL
	result := f.Fetch()

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// 两个窗口百分比相同(0.0035%),小时窗口优先
	if result.Total != 28000 {
		t.Errorf("expected Total=28000, got %f", result.Total)
	}
	if result.Used != 1 {
		t.Errorf("expected Used=1, got %f", result.Used)
	}
	if !strings.Contains(result.Remaining, "5小时") || !strings.Contains(result.Remaining, "1个月") {
		t.Errorf("expected both windows in Remaining, got %s", result.Remaining)
	}
	if !strings.Contains(result.Remaining, "1 / 28,000") {
		t.Errorf("expected '1 / 28000' in Remaining, got %s", result.Remaining)
	}
	if result.ResetAt == "" {
		t.Error("expected non-empty ResetAt")
	}
}

func TestGLM_MonthWindowHigherPercent_Chosen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"code": 200,
			"data": {
				"limits": [
					{
						"type": "CREDIT_LIMIT",
						"unit": 3,
						"number": 5,
						"usage": 100,
						"remaining": 90,
						"nextResetTime": 1786953113005
					},
					{
						"type": "CREDIT_LIMIT",
						"unit": 6,
						"number": 1,
						"usage": 200,
						"remaining": 50,
						"nextResetTime": 1787539506998
					}
				]
			},
			"success": true
		}`))
	}))
	defer server.Close()

	f := NewGLMFetcher("eyJtest", "", "")
	f.apiURL = server.URL
	result := f.Fetch()

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	// 月窗口已用 150/200=75% > 小时窗口 10/100=10%
	if result.Total != 200 {
		t.Errorf("expected Total=200, got %f", result.Total)
	}
	if result.Used != 150 {
		t.Errorf("expected Used=150, got %f", result.Used)
	}
}

func TestGLM_401_ReturnsInvalidToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	f := NewGLMFetcher("eyJtest", "", "")
	f.apiURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "无效") {
		t.Errorf("expected invalid token error, got %s", result.Error)
	}
}

func TestGLM_BusinessError_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code": 401, "msg": "登录已过期", "success": false}`))
	}))
	defer server.Close()

	f := NewGLMFetcher("eyJtest", "", "")
	f.apiURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "登录已过期") {
		t.Errorf("expected business error message, got %s", result.Error)
	}
}

func TestGLM_BadJSON_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	f := NewGLMFetcher("eyJtest", "", "")
	f.apiURL = server.URL
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error for bad JSON")
	}
}

func TestGLM_EmptyLimits_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code": 200, "data": {"limits": []}, "success": true}`))
	}))
	defer server.Close()

	f := NewGLMFetcher("eyJtest", "", "")
	f.apiURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "未找到") {
		t.Errorf("expected no-data error, got %s", result.Error)
	}
}
