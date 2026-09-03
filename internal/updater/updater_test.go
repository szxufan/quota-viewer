package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.1.0", "1.0.9", true},
		{"2.0.0", "1.9.9", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"1.0", "1.0.0", false},   // 补零后相等
		{"1.1", "1.0.0", true},    // 补零后更新
		{"v1.0.1", "1.0.0", true}, // v 前缀
		{"v2.0", "1.9.9", true},
		{"", "1.0.0", false},    // 空版本不触发
		{"abc", "1.0.0", false}, // 非数字按 0
		{"10.0.0", "9.0.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

// serveManifest 起一个返回指定清单内容/状态码的测试服务器。
func serveManifest(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustManifest(t *testing.T, version string) string {
	t.Helper()
	data, err := json.Marshal(Manifest{
		Version: version,
		Platforms: map[string]*PlatformEntry{
			platformKeyForTest(): {URL: "https://example.com/installer.exe", SHA256: strings.Repeat("ab", 32)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// platformKeyForTest 返回测试进程所在平台的清单键。
func platformKeyForTest() string { return currentPlatformKey() }

func TestCheckLatest_HasUpdate(t *testing.T) {
	srv := serveManifest(t, http.StatusOK, mustManifest(t, "9.9.9"))
	orig := ManifestURL
	ManifestURL = srv.URL
	defer func() { ManifestURL = orig }()

	entry, version, ok, err := CheckLatest(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("CheckLatest 出错: %v", err)
	}
	if !ok {
		t.Fatal("期望检测到更新")
	}
	if version != "9.9.9" {
		t.Fatalf("version = %q, want 9.9.9", version)
	}
	if entry == nil || entry.URL != "https://example.com/installer.exe" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestCheckLatest_NoUpdate(t *testing.T) {
	srv := serveManifest(t, http.StatusOK, mustManifest(t, "1.0.0"))
	orig := ManifestURL
	ManifestURL = srv.URL
	defer func() { ManifestURL = orig }()

	_, _, ok, err := CheckLatest(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("CheckLatest 出错: %v", err)
	}
	if ok {
		t.Fatal("同版本不应触发更新")
	}
}

func TestCheckLatest_MissingPlatform(t *testing.T) {
	// 清单只有 darwin/arm64 条目,本平台应静默跳过
	data, _ := json.Marshal(Manifest{
		Version: "9.9.9",
		Platforms: map[string]*PlatformEntry{
			"darwin/arm64": {URL: "https://example.com/x", SHA256: "aa"},
		},
	})
	srv := serveManifest(t, http.StatusOK, string(data))
	orig := ManifestURL
	ManifestURL = srv.URL
	defer func() { ManifestURL = orig }()

	_, _, ok, err := CheckLatest(context.Background(), "1.0.0")
	if err != nil || ok {
		t.Fatalf("缺平台条目应 ok=false 且无错, got ok=%v err=%v", ok, err)
	}
}

func TestCheckLatest_Errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"404", http.StatusNotFound, "not found"},
		{"坏JSON", http.StatusOK, "{not json"},
		{"缺版本号", http.StatusOK, `{"platforms":{}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := serveManifest(t, tt.status, tt.body)
			orig := ManifestURL
			ManifestURL = srv.URL
			defer func() { ManifestURL = orig }()

			if _, _, _, err := CheckLatest(context.Background(), "1.0.0"); err == nil {
				t.Fatal("期望返回错误")
			}
		})
	}
}

func TestCheckLatest_NoManifestURL(t *testing.T) {
	orig := ManifestURL
	ManifestURL = ""
	defer func() { ManifestURL = orig }()

	if _, _, _, err := CheckLatest(context.Background(), "1.0.0"); err == nil {
		t.Fatal("ManifestURL 为空应返回错误")
	}
}

// serveFile 起一个返回固定内容的文件服务器,并返回内容 SHA256。
func serveFile(t *testing.T, content []byte) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	t.Cleanup(srv.Close)
	sum := sha256.Sum256(content)
	return srv, hex.EncodeToString(sum[:])
}

func TestDownloadInstaller_Success(t *testing.T) {
	content := []byte("fake installer content for quota-viewer update")
	srv, sum := serveFile(t, content)

	orig := updateDirForTest(t)
	defer func() { restoreUpdateDir(orig) }()

	entry := &PlatformEntry{URL: srv.URL, SHA256: sum}
	path, err := DownloadInstaller(context.Background(), entry, "1.2.3")
	if err != nil {
		t.Fatalf("DownloadInstaller 出错: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Fatal("下载内容不一致")
	}
	// 最终文件不带 .tmp 后缀
	if filepath.Ext(path) != ".exe" {
		t.Fatalf("产物应为正式名, got %q", path)
	}
	// tmp 已被 rename 走,不应残留
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal(".tmp 文件不应残留")
	}
}

func TestDownloadInstaller_BadSHA256(t *testing.T) {
	content := []byte("tampered content")
	srv, _ := serveFile(t, content)

	orig := updateDirForTest(t)
	defer func() { restoreUpdateDir(orig) }()

	entry := &PlatformEntry{URL: srv.URL, SHA256: strings.Repeat("cd", 32)}
	if _, err := DownloadInstaller(context.Background(), entry, "1.2.3"); err == nil {
		t.Fatal("SHA256 不匹配应返回错误")
	}

	// 失败后目录中不应残留任何文件
	dir, _ := updateDir()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("校验失败应清理残留, 目录中还有 %d 个文件", len(entries))
	}
}

func TestDownloadInstaller_EmptyEntry(t *testing.T) {
	if _, err := DownloadInstaller(context.Background(), nil, "1.0.0"); err == nil {
		t.Fatal("nil entry 应返回错误")
	}
	if _, err := DownloadInstaller(context.Background(), &PlatformEntry{URL: "", SHA256: "aa"}, "1.0.0"); err == nil {
		t.Fatal("空 URL 应返回错误")
	}
	if _, err := DownloadInstaller(context.Background(), &PlatformEntry{URL: "https://x/y"}, "1.0.0"); err == nil {
		t.Fatal("缺 SHA256 应返回错误")
	}
}

func TestSha256OfFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "h*")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("hello")
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])
	got, err := sha256OfFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("sha256OfFile = %q, want %q", got, want)
	}
}

// ---- 以下为测试辅助:把更新目录重定向到临时目录,避免污染真实 %TEMP% ----

func updateDirForTest(t *testing.T) string {
	t.Helper()
	orig := tempDirOverride
	tempDirOverride = t.TempDir()
	return orig
}

func restoreUpdateDir(orig string) { tempDirOverride = orig }
