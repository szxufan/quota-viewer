package syncstate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDownloadOK 200 响应返回完整内容。
func TestDownloadOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("encrypted-data"))
	}))
	defer srv.Close()

	data, err := Download(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Download 出错: %v", err)
	}
	if string(data) != "encrypted-data" {
		t.Errorf("内容 = %q, 应为 %q", data, "encrypted-data")
	}
}

// TestDownloadNotFound 非 200 响应返回带状态码的错误。
func TestDownloadNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := Download(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("404 未报错")
	}
}

// TestDownloadEmptyURL 空地址报错。
func TestDownloadEmptyURL(t *testing.T) {
	if _, err := Download(context.Background(), ""); err == nil {
		t.Fatal("空地址未报错")
	}
}
