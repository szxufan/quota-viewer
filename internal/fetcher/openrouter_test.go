package fetcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenRouter_EmptyKey_ReturnsError(t *testing.T) {
	f := NewOpenRouterFetcher("")
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error for empty key")
	}
}

func TestOpenRouter_OK_ParsesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk-or-v1-test" {
			t.Errorf("expected Bearer sk-or-v1-test, got %s", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"total_credits":14,"total_usage":0.004243457}}`))
	}))
	defer server.Close()

	f := NewOpenRouterFetcher("sk-or-v1-test")
	f.apiURL = server.URL
	result := f.Fetch()

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Total != 14 {
		t.Errorf("expected Total=14, got %f", result.Total)
	}
	if result.Used < 0.004 || result.Used > 0.005 {
		t.Errorf("expected Used ~0.004243, got %f", result.Used)
	}
	if result.Percent <= 0 || result.Percent >= 1 {
		t.Errorf("expected small Percent (< 1%%), got %f", result.Percent)
	}
	if !strings.Contains(result.Remaining, "$14") {
		t.Errorf("expected '$14' in Remaining, got %s", result.Remaining)
	}
}

func TestOpenRouter_ZeroCredits_ReturnsZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"total_credits":0,"total_usage":0}}`))
	}))
	defer server.Close()

	f := NewOpenRouterFetcher("sk-or-v1-test")
	f.apiURL = server.URL
	result := f.Fetch()

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Total != 0 || result.Used != 0 {
		t.Errorf("expected Total=0, Used=0, got Total=%f Used=%f", result.Total, result.Used)
	}
}

func TestOpenRouter_401_ReturnsInvalidKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	f := NewOpenRouterFetcher("sk-or-v1-test")
	f.apiURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "无效") {
		t.Errorf("expected invalid key error, got %s", result.Error)
	}
}

func TestOpenRouter_BadJSON_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	f := NewOpenRouterFetcher("sk-or-v1-test")
	f.apiURL = server.URL
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error for bad JSON")
	}
}
