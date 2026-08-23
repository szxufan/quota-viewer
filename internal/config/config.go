package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 是应用配置:动态 Provider 列表(顺序 = 展示顺序)+ 通用项。
type Config struct {
	Providers          []ProviderConfig `json:"providers"`
	RefreshIntervalMin int              `json:"refresh_interval_min"`
	BallX              int              `json:"ball_x"`
	BallY              int              `json:"ball_y"`
	Opacity            float64          `json:"opacity"` // 界面透明度 0.2-1.0,1.0 = 不透明
}

// ProviderConfig 描述单个 Provider 的启用状态与凭证。
type ProviderConfig struct {
	ID      string              `json:"id"`
	Enabled bool                `json:"enabled"`
	Creds   map[string]string   `json:"creds,omitempty"` // 旧版单凭证(兼容读取;保存后统一升级为 Keys)
	Keys         []map[string]string `json:"keys,omitempty"`         // 多组凭证(每组一套字段值);空 = 未配置多 key
	Budget       float64             `json:"budget,omitempty"`       // 余额型 Provider 的预算总量(0 = 未设)
	LastBalances []float64           `json:"last_balances,omitempty"` // 各凭证组上次抓取到的余额(充值检测基线)
}

// CredKeys 返回凭证组列表:优先 Keys;否则旧 Creds 视为单组;均空返回 nil。
func (p ProviderConfig) CredKeys() []map[string]string {
	if len(p.Keys) > 0 {
		return p.Keys
	}
	if len(p.Creds) > 0 {
		return []map[string]string{p.Creds}
	}
	return nil
}

// AllProviderIDs 全部已知 Provider id(与 fetcher 注册表一致,顺序 = 展示顺序)。
var AllProviderIDs = []string{"kimi", "xfyun", "opencode-go", "mimo", "deepseek"}

// DefaultProviderIDs 默认启用的 Provider(与现状一致:Kimi/讯飞/OpenCode Go)。
var DefaultProviderIDs = []string{"kimi", "xfyun", "opencode-go"}

// legacyConfig 旧版扁平配置结构(仅用于 Load 时迁移)。
type legacyConfig struct {
	KimiAPIKey             string `json:"kimi_api_key"`
	XfyunCookie            string `json:"xfyun_cookie"`
	MimoCookie             string `json:"mimo_cookie"`
	OpenCodeGoWorkspaceID  string `json:"opencode_go_workspace_id"`
	OpenCodeGoSessionToken string `json:"opencode_go_session_token"`
	RefreshIntervalMin     int    `json:"refresh_interval_min"`
	BallX                  int    `json:"ball_x"`
	BallY                  int    `json:"ball_y"`
}

func configDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", os.ErrNotExist
	}
	dir := filepath.Join(appData, "quota-viewer")
	return dir, nil
}

func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Default 返回带默认值的配置:全部 Provider 注册,默认启用前三个。
func Default() *Config {
	cfg := &Config{
		Providers:          make([]ProviderConfig, 0, len(AllProviderIDs)),
		RefreshIntervalMin: 15,
		BallX:              -1,
		BallY:              -1,
		Opacity:            1.0,
	}
	for _, id := range AllProviderIDs {
		enabled := false
		for _, d := range DefaultProviderIDs {
			if d == id {
				enabled = true
				break
			}
		}
		cfg.Providers = append(cfg.Providers, ProviderConfig{ID: id, Enabled: enabled})
	}
	return cfg
}

// Load 读取配置文件。文件不存在时返回带默认值的空配置(不报错)。
// 旧版扁平字段格式(config v1)会自动迁移为 providers 结构并回写。
func Load() (*Config, error) {
	cfg := Default()

	path, err := configPath()
	if err != nil {
		return cfg, nil // APPDATA 不存在,返回默认配置
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // 文件不存在不算错误
		}
		return cfg, err
	}

	// 探测是新格式(providers 键)还是旧格式(扁平字段)
	var probe struct {
		Providers []ProviderConfig `json:"providers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return cfg, err
	}

	if len(probe.Providers) > 0 {
		// 新格式
		if err := json.Unmarshal(data, cfg); err != nil {
			return cfg, err
		}
	} else {
		// 旧格式:迁移
		var legacy legacyConfig
		if err := json.Unmarshal(data, &legacy); err != nil {
			return cfg, err
		}
		cfg = migrateFromLegacy(legacy)
	}

	// 确保默认值
	if cfg.RefreshIntervalMin <= 0 {
		cfg.RefreshIntervalMin = 15
	}
	// 确保 providers 非空(防御:损坏文件)
	if len(cfg.Providers) == 0 {
		cfg.Providers = Default().Providers
	}
	// 确保透明度在合法范围(旧配置无此字段 → 零值 0 → 回退不透明)
	if cfg.Opacity <= 0 || cfg.Opacity > 1 {
		cfg.Opacity = 1
	}

	return cfg, nil
}

// migrateFromLegacy 把旧版扁平配置迁移为 providers 结构。
// 有值的旧字段 → 对应 Provider enabled + 凭证迁移;全部为空 → 默认启用前三个。
// 迁移后立即回写新格式(失败静默,下次 Load 会再尝试)。
func migrateFromLegacy(l legacyConfig) *Config {
	cfg := Default()
	cfg.RefreshIntervalMin = l.RefreshIntervalMin
	cfg.BallX = l.BallX
	cfg.BallY = l.BallY

	set := func(id string, has bool, creds map[string]string) {
		if !has {
			return
		}
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == id {
				cfg.Providers[i].Enabled = true
				cfg.Providers[i].Creds = creds
				return
			}
		}
	}

	set("kimi", l.KimiAPIKey != "", map[string]string{"api_key": l.KimiAPIKey})
	set("xfyun", l.XfyunCookie != "", map[string]string{"cookie": l.XfyunCookie})
	set("mimo", l.MimoCookie != "", map[string]string{"cookie": l.MimoCookie})

	oc := map[string]string{}
	if l.OpenCodeGoWorkspaceID != "" {
		oc["workspace_id"] = l.OpenCodeGoWorkspaceID
	}
	if l.OpenCodeGoSessionToken != "" {
		oc["session_token"] = l.OpenCodeGoSessionToken
	}
	set("opencode-go", len(oc) > 0, oc)

	// 有凭证的 Provider 统一升级为 Keys 模型(单组),Creds 清空
	for i := range cfg.Providers {
		if len(cfg.Providers[i].Creds) > 0 {
			cfg.Providers[i].Keys = []map[string]string{cfg.Providers[i].Creds}
			cfg.Providers[i].Creds = nil
		}
	}

	_ = Save(cfg) // 回写新格式,失败静默
	return cfg
}

// Save 写入配置文件。目录不存在时自动创建。
func Save(cfg *Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	path, err := configPath()
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
