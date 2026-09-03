// Command release 本地一键自动发布:构建 → 计算 SHA256 → 上传 OSS → 更新清单。
//
// 用法:在项目根目录配置 release.env 后执行
//
//	go run ./cmd/release
//
// release.env 字段见 release.env.example;真实文件不入 git。
// 版本号单一来源为 wails.json 的 info.productVersion。
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"

	"quota-viewer/internal/updater"
)

// releaseConfig 是 release.env 解析结果。
type releaseConfig struct {
	manifestURL     string // 版本清单公开地址(HTTPS,客户端内置)
	ossAccessKeyID  string
	ossAccessSecret string
	ossEndpoint     string // 如 oss-cn-hangzhou.aliyuncs.com
	ossBucket       string
}

// objectPrefix 发布产物在 bucket 里的前缀。
const objectPrefix = "quota-viewer"

// installerObjectPath 安装包对象 key(按版本分目录,保留历史版本)。
func installerObjectPath(version string) string {
	return objectPrefix + "/" + version + "/quota-viewer-amd64-installer.exe"
}

// manifestObjectPath 清单对象 key(固定路径,每次发布覆盖)。
const manifestObjectPath = objectPrefix + "/version.json"

// platformKey 是清单中本平台的键(GOOS/GOARCH)。
const platformKey = "windows/amd64"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "发布失败:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := projectRoot()
	if err != nil {
		return err
	}

	// 1. 读取发布配置(源码零硬编码,全部来自 release.env)
	cfg, err := loadReleaseEnv(filepath.Join(root, "release.env"))
	if err != nil {
		return err
	}

	// 2. 版本号 = wails.json 的 info.productVersion(单一来源)
	version, err := readProductVersion(filepath.Join(root, "wails.json"))
	if err != nil {
		return err
	}
	fmt.Printf("==> 发布版本: %s\n", version)

	// 3. 构建安装包(ldflags 注入版本与清单地址)
	installer, err := buildInstaller(root, version, cfg.manifestURL)
	if err != nil {
		return err
	}
	fmt.Printf("==> 构建完成: %s\n", installer)

	// 4. 计算安装包 SHA256
	sum, err := sha256File(installer)
	if err != nil {
		return fmt.Errorf("计算安装包 SHA256 失败: %w", err)
	}
	fmt.Printf("==> SHA256: %s\n", sum)

	// 5. 上传安装包 + 更新清单
	client, err := oss.New(cfg.ossEndpoint, cfg.ossAccessKeyID, cfg.ossAccessSecret)
	if err != nil {
		return fmt.Errorf("连接 OSS 失败: %w", err)
	}
	bucket, err := client.Bucket(cfg.ossBucket)
	if err != nil {
		return fmt.Errorf("获取 bucket 失败: %w", err)
	}

	objPath := installerObjectPath(version)
	if err := bucket.PutObjectFromFile(objPath, installer); err != nil {
		return fmt.Errorf("上传安装包失败: %w", err)
	}
	fmt.Printf("==> 已上传: %s\n", objPath)

	// 清单里安装包 URL 由公开清单地址推导(同 bucket 公共读域名)
	installerURL := deriveInstallerURL(cfg.manifestURL, objPath)
	payload := updater.Manifest{
		Version: version,
		Platforms: map[string]*updater.PlatformEntry{
			platformKey: {URL: installerURL, SHA256: sum},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("生成清单失败: %w", err)
	}
	if err := bucket.PutObject(manifestObjectPath, strings.NewReader(string(data)+"\n")); err != nil {
		return fmt.Errorf("上传清单失败: %w", err)
	}

	fmt.Printf("==> 已更新清单: %s\n", manifestObjectPath)
	fmt.Printf("==> 发布完成: %s (清单: %s)\n", version, cfg.manifestURL)
	return nil
}

// projectRoot 返回项目根目录(本文件位于 cmd/release,向上两级)。
func projectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	// 允许在任意子目录执行:向上找 go.mod
	dir := wd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("未找到项目根目录(缺 go.mod),请在项目内执行")
}

// loadReleaseEnv 解析 release.env(KEY=VALUE 逐行,# 开头为注释)。
func loadReleaseEnv(path string) (*releaseConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("缺少 release.env(%w),请复制 release.env.example 并填写", err)
	}
	defer f.Close()

	cfg := &releaseConfig{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "UPDATE_MANIFEST_URL":
			cfg.manifestURL = v
		case "OSS_ACCESS_KEY_ID":
			cfg.ossAccessKeyID = v
		case "OSS_ACCESS_KEY_SECRET":
			cfg.ossAccessSecret = v
		case "OSS_ENDPOINT":
			cfg.ossEndpoint = v
		case "OSS_BUCKET":
			cfg.ossBucket = v
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取 release.env 失败: %w", err)
	}
	if cfg.manifestURL == "" || cfg.ossAccessKeyID == "" || cfg.ossAccessSecret == "" ||
		cfg.ossEndpoint == "" || cfg.ossBucket == "" {
		return nil, fmt.Errorf("release.env 配置不完整,请核对 release.env.example 各字段")
	}
	if !strings.HasPrefix(cfg.manifestURL, "https://") {
		return nil, fmt.Errorf("UPDATE_MANIFEST_URL 必须为 https:// 地址")
	}
	return cfg, nil
}

// readProductVersion 从 wails.json 读取 info.productVersion。
func readProductVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 wails.json 失败: %w", err)
	}
	var parsed struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("解析 wails.json 失败: %w", err)
	}
	v := strings.TrimSpace(parsed.Info.ProductVersion)
	if v == "" {
		return "", fmt.Errorf("wails.json 缺少 info.productVersion")
	}
	return v, nil
}

// buildInstaller 执行 wails build 生成 NSIS 安装包,返回产物路径。
func buildInstaller(root, version, manifestURL string) (string, error) {
	ldflags := fmt.Sprintf("-X main.Version=%s -X quota-viewer/internal/updater.ManifestURL=%s",
		version, manifestURL)
	cmd := exec.Command("wails", "build", "-platform", "windows/amd64", "-nsis", "-ldflags", ldflags)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("==> 构建: %s\n", cmd.String())
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("wails build 失败: %w", err)
	}
	out := filepath.Join(root, "build", "bin", "quota-viewer-amd64-installer.exe")
	if _, err := os.Stat(out); err != nil {
		return "", fmt.Errorf("未找到构建产物 %s: %w", out, err)
	}
	return out, nil
}

// sha256File 计算文件 SHA256 十六进制摘要。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var h hash.Hash = sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// deriveInstallerURL 由公开清单地址推导安装包下载地址:
// 清单地址 = https://<bucket>.<endpoint>/<prefix>/version.json,
// 安装包地址 = https://<bucket>.<endpoint>/<objectPath>。
func deriveInstallerURL(manifestURL, objectPath string) string {
	base := manifestURL
	// 去掉清单自身路径,保留 scheme://host(/路径前缀)
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[:i]
	}
	return base + "/" + objectPath
}
