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
	Sync               SyncConfig       `json:"sync,omitempty"`
}

// SyncMode 同步模式常量(见 .trae/documents/oss-state-sync.md)。
const (
	SyncModeOff       = ""          // 关闭(默认)
	SyncModePublish   = "publish"   // 发布端:加密上传到 OSS
	SyncModeSubscribe = "subscribe" // 订阅端:公网下载解密展示
)

// SyncConfig 是多机状态同步配置。密码与 AccessKey 与现有凭证一致明文存储,
// 下发前端时掩码(app.go maskSecret)。
type SyncConfig struct {
	Mode            string `json:"mode"` // "" | "publish" | "subscribe"
	Password        string `json:"password,omitempty"`          // 加密密码(SHA-256 派生 AES-256 密钥)
	OSSEndpoint     string `json:"oss_endpoint,omitempty"`      // 发布端:OSS Endpoint
	OSSBucket       string `json:"oss_bucket,omitempty"`        // 发布端:Bucket 名
	OSSKey          string `json:"oss_key,omitempty"`           // 发布端:对象路径(固定,覆盖写)
	OSSAccessID     string `json:"oss_access_id,omitempty"`     // 发布端:AccessKey ID
	OSSAccessSecret string `json:"oss_access_secret,omitempty"` // 发布端:AccessKey Secret
	URL             string `json:"url,omitempty"`               // 订阅端:状态文件公网地址
}

// ProviderConfig 描述单个 Provider 的启用状态与凭证。
type ProviderConfig struct {
	ID           string              `json:"id"`
	Enabled      bool                `json:"enabled"`
	Creds        map[string]string   `json:"creds,omitempty"`         // 旧版单凭证(兼容读取;保存后统一升级为 Keys)
	Keys         []map[string]string `json:"keys,omitempty"`          // 多组凭证(每组一套字段值);空 = 未配置多 key
	KeyNames     []string            `json:"key_names,omitempty"`     // 各凭证组的显示名(与 Keys 对齐;空 = 详情页回退 "Key N")
	Budget       float64             `json:"budget,omitempty"`        // 旧版渠道级预算(已废弃;Load 时迁移到 Budgets)
	Budgets      []float64           `json:"budgets,omitempty"`       // 各凭证组的预算(与 Keys 对齐;0 = 该组未设)
	LastBalances []float64           `json:"last_balances,omitempty"` // 各凭证组上次抓取到的余额(充值检测基线)
	SyncExcludes []bool              `json:"sync_excludes,omitempty"` // 各凭证组是否排除同步(与 Keys 对齐;"排除"语义使零值 = 同步,旧配置默认全量同步)
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

	// 预算从旧渠道级迁移为逐凭证组,并对齐到当前凭证组数
	if migrateBudgets(cfg) {
		_ = Save(cfg) // 迁移后立即回写,失败静默(下次 Load 会再尝试)
	}

	return cfg, nil
}

// migrateBudgets 把旧版渠道级预算(Budget)迁移为逐凭证组预算(Budgets),
// 并保证 Budgets 与凭证组数量对齐。旧值迁移到各组(每凭证组沿用原渠道预算)。
// 返回是否发生改动(需要回写)。
func migrateBudgets(cfg *Config) bool {
	changed := false
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		n := len(p.CredKeys())
		// 旧渠道级预算 → 逐组预算;仅当尚无逐组预算时迁移,避免覆盖已有值
		if p.Budget > 0 && len(p.Budgets) == 0 {
			if n == 0 {
				n = 1
			}
			p.Budgets = make([]float64, n)
			for k := range p.Budgets {
				p.Budgets[k] = p.Budget
			}
			p.Budget = 0
			changed = true
		} else if len(p.Budgets) > 0 && n != len(p.Budgets) {
			p.Budgets = AlignBudgets(p.Budgets, n)
			changed = true
		}
	}
	return changed
}

// AlignBudgets 把预算切片对齐到 n 个凭证组:
// 返回长度 = n,不足补 0(该组未设),超出截断。用于保存与迁移。
func AlignBudgets(b []float64, n int) []float64 {
	out := make([]float64, n)
	for i := 0; i < n && i < len(b); i++ {
		out[i] = b[i]
	}
	return out
}

// AlignSyncExcludes 把同步排除切片对齐到 n 个凭证组:
// 返回长度 = n,不足补 false(该组同步),超出截断;n ≤ 0 时返回 nil。
func AlignSyncExcludes(in []bool, n int) []bool {
	if n <= 0 {
		return nil
	}
	out := make([]bool, n)
	for i := 0; i < n && i < len(in); i++ {
		out[i] = in[i]
	}
	return out
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
