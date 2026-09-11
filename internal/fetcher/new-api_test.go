package fetcher

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAPIFetcher_OK_ParsesBalanceAndUsedAmount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/channel/42" {
			t.Errorf("expected path /api/channel/42, got %s", r.URL.Path)
		}
		if got := r.Header.Get("New-Api-User"); got != "alice" {
			t.Errorf("expected New-Api-User 'alice', got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("expected raw Authorization 'Bearer token', got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"balance":12.34,"used_quota":12345}}`))
	}))
	defer server.Close()

	result := NewNewAPIFetcher(server.URL+"/", "42", "alice", "Bearer token").Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Kind != KindUsage {
		t.Errorf("expected Kind=%s, got %s", KindUsage, result.Kind)
	}
	if result.Balance != 12.34 {
		t.Errorf("expected Balance=12.34, got %f", result.Balance)
	}
	wantUsed := 12345 / newAPIQuotaScale
	if math.Abs(result.Used-wantUsed) > 1e-9 {
		t.Errorf("expected Used=%f, got %f", wantUsed, result.Used)
	}
	if result.Total != 0 {
		t.Errorf("expected Total=0, got %f", result.Total)
	}
	if result.Percent != 0 {
		t.Errorf("expected Percent=0, got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "12.34") || !strings.Contains(result.Remaining, "0.02469") {
		t.Errorf("unexpected Remaining: %q", result.Remaining)
	}
}

func TestNewAPIFetcher_MissingConfig_ReturnsError(t *testing.T) {
	tests := []struct {
		name          string
		baseURL       string
		channelID     string
		user          string
		authorization string
		want          string
	}{
		{name: "base url", channelID: "42", user: "alice", authorization: "Bearer token", want: "BaseUrl"},
		{name: "channel id", baseURL: "https://api.example.com", user: "alice", authorization: "Bearer token", want: "channel_id"},
		{name: "user", baseURL: "https://api.example.com", channelID: "42", authorization: "Bearer token", want: "User"},
		{name: "authorization", baseURL: "https://api.example.com", channelID: "42", user: "alice", want: "Authorization"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewNewAPIFetcher(tt.baseURL, tt.channelID, tt.user, tt.authorization).Fetch()
			if !strings.Contains(result.Error, tt.want) {
				t.Errorf("expected error containing %q, got %q", tt.want, result.Error)
			}
		})
	}
}

func TestNewAPIFetcher_BadBaseURL_ReturnsError(t *testing.T) {
	result := NewNewAPIFetcher("not-a-url", "42", "alice", "Bearer token").Fetch()
	if !strings.Contains(result.Error, "创建请求失败") {
		t.Errorf("expected URL creation error, got %q", result.Error)
	}
}

func TestNewAPIFetcher_401_ReturnsInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	result := NewNewAPIFetcher(server.URL, "42", "alice", "Bearer token").Fetch()
	if !strings.Contains(result.Error, "凭证无效") {
		t.Errorf("expected invalid credentials error, got %q", result.Error)
	}
}

func TestNewAPIFetcher_Non200_ReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	result := NewNewAPIFetcher(server.URL, "42", "alice", "Bearer token").Fetch()
	if result.Error != "HTTP 502" {
		t.Errorf("expected HTTP 502 error, got %q", result.Error)
	}
}

func TestNewAPIFetcher_BadJSON_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	defer server.Close()

	result := NewNewAPIFetcher(server.URL, "42", "alice", "Bearer token").Fetch()
	if !strings.Contains(result.Error, "解析响应失败") {
		t.Errorf("expected JSON parse error, got %q", result.Error)
	}
}

func TestNewAPIFetcher_MissingData_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer server.Close()

	result := NewNewAPIFetcher(server.URL, "42", "alice", "Bearer token").Fetch()
	if !strings.Contains(result.Error, "未找到渠道数据") {
		t.Errorf("expected missing data error, got %q", result.Error)
	}
}
