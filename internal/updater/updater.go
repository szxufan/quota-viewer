// Package updater 实现应用自动升级:
// 拉取版本清单 → 语义化版本比较 → 下载安装包并校验 SHA256 → 静默安装。
// 清单地址 ManifestURL 由发布构建经 ldflags 注入,源码中不含任何 URL。
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ManifestURL 版本清单地址,发布构建时通过
// -ldflags "-X quota-viewer/internal/updater.ManifestURL=<url>" 注入。
// 空字符串 = 未配置发布渠道,不做更新检查。
var ManifestURL string

const (
	// manifestTimeout 清单下载超时。
	manifestTimeout = 15 * time.Second
	// maxManifestBytes 清单大小上限(正常载荷远小于此,防御异常响应)。
	maxManifestBytes = 1 << 20
	// maxInstallerBytes 安装包大小上限(防御异常响应)。
	maxInstallerBytes = 200 << 20
	// installerTimeout 单次安装包下载超时。
	installerTimeout = 10 * time.Minute
)

// PlatformEntry 是单个平台(GOOS/GOARCH)的发布产物信息。
type PlatformEntry struct {
	// URL 安装包下载地址(HTTPS)。
	URL string `json:"url"`
	// SHA256 安装包十六进制摘要(小写,可选 v 前缀容错不处理,约定无前缀)。
	SHA256 string `json:"sha256"`
}

// Manifest 是 version.json 的结构:顶层版本信息 + 各平台产物映射。
// 平台键为 "GOOS/GOARCH" 形式(如 "windows/amd64"),未来扩展平台无需改协议。
type Manifest struct {
	Version   string                    `json:"version"`
	Notes     string                    `json:"notes,omitempty"`
	Platforms map[string]*PlatformEntry `json:"platforms"`
}

// manifestHTTPClient 清单请求客户端(显式超时,不复用全局 DefaultClient 便于测试注入)。
var manifestHTTPClient = &http.Client{Timeout: manifestTimeout}

// installerHTTPClient 安装包下载客户端(流式下载,超时约束整个 context 而非单请求)。
var installerHTTPClient = &http.Client{}

// CheckLatest 拉取清单并判断是否有比 current 更新的版本。
// 返回当前平台(GOOS/GOARCH)对应的产物条目、最新版本号、是否有更新。
// 清单中无本平台条目视为"无更新"(如未来 darwin 构建走同一客户端)。
func CheckLatest(ctx context.Context, current string) (entry *PlatformEntry, version string, ok bool, err error) {
	m, err := fetchManifest(ctx)
	if err != nil {
		return nil, "", false, err
	}
	entry, ok = m.Platforms[currentPlatformKey()]
	if !ok || entry == nil || entry.URL == "" {
		return nil, m.Version, false, nil
	}
	if !Newer(m.Version, current) {
		return entry, m.Version, false, nil
	}
	return entry, m.Version, true, nil
}

// fetchManifest 下载并解析版本清单(匿名 GET,1MB 上限)。
func fetchManifest(ctx context.Context) (*Manifest, error) {
	if ManifestURL == "" {
		return nil, fmt.Errorf("未配置更新清单地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ManifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("更新清单地址无效: %w", err)
	}
	resp, err := manifestHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取更新清单失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取更新清单失败(HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes))
	if err != nil {
		return nil, fmt.Errorf("读取更新清单失败: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析更新清单失败: %w", err)
	}
	if m.Version == "" {
		return nil, fmt.Errorf("更新清单缺少版本号")
	}
	return &m, nil
}

// DownloadInstaller 下载安装包到临时目录并校验 SHA256:
// 先写 .tmp 文件(边下边算摘要),校验通过才重命名为正式名,失败时清理残留。
// 返回校验通过后的本地安装包路径。
func DownloadInstaller(ctx context.Context, e *PlatformEntry, version string) (string, error) {
	if e == nil || e.URL == "" {
		return "", fmt.Errorf("安装包地址为空")
	}
	want := strings.ToLower(e.SHA256)
	if want == "" {
		return "", fmt.Errorf("安装包缺少 SHA256 校验值")
	}

	dir, err := updateDir()
	if err != nil {
		return "", err
	}
	final := filepath.Join(dir, version+"-installer.exe")
	tmp := final + ".tmp"
	defer func() {
		_ = os.Remove(tmp) // 成功 rename 后 Remove 报错可忽略;失败路径负责清理
	}()

	ctx, cancel := context.WithTimeout(ctx, installerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return "", fmt.Errorf("安装包地址无效: %w", err)
	}
	resp, err := installerHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载安装包失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载安装包失败(HTTP %d)", resp.StatusCode)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建更新临时目录失败: %w", err)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %w", err)
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxInstallerBytes))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("下载安装包失败: %w", err)
	}
	if n >= maxInstallerBytes {
		return "", fmt.Errorf("安装包超过大小上限(%d MB)", maxInstallerBytes>>20)
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("安装包校验失败(期望 %s,实际 %s)", want, shortSHA(want))
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", fmt.Errorf("保存安装包失败: %w", err)
	}
	return final, nil
}

// tempDirOverride 供测试重定向更新目录(正常为空,用 os.TempDir())。
var tempDirOverride string

// updateDir 返回升级工作目录(%TEMP%\quota-viewer-update)。
func updateDir() (string, error) {
	base := tempDirOverride
	if base == "" {
		base = os.TempDir()
	}
	if base == "" {
		return "", fmt.Errorf("无法确定临时目录")
	}
	return filepath.Join(base, "quota-viewer-update"), nil
}

// currentPlatformKey 返回运行平台在清单中的键(GOOS/GOARCH)。
func currentPlatformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// shortSHA 截断摘要用于日志展示。
func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12] + "..."
	}
	return s
}

// Newer 判断 latest 是否比 current 更新(语义化版本,仅比较数字段):
// 容忍 v 前缀与不足 3 段(补 0),如 Newer("v1.1", "1.0.0") == true。
func Newer(latest, current string) bool {
	return compareVersions(latest, current) > 0
}

// compareVersions 比较 two 版本号,返回 -1/0/1。
func compareVersions(a, b string) int {
	as, bs := splitVersion(a), splitVersion(b)
	n := max(len(as), len(bs))
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// splitVersion 去掉 v 前缀后按 "." 拆分为数字段,非数字段按 0 处理。
func splitVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "v"), "V")
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				n = 0 // 含非数字(如 1.0.0-beta 的 beta 段)按 0,仅支持纯数字段比较
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}

// sha256OfFile 计算文件 SHA256 十六进制摘要。
func sha256OfFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
