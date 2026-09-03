package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeConfig 把 JSON 写入测试 APPDATA 下的配置文件。
func writeConfig(t *testing.T, jsonStr string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("APPDATA", tmpDir)

	dir := filepath.Join(tmpDir, "quota-viewer")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(jsonStr), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_FileNotExists_ReturnsDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("APPDATA", tmpDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.RefreshIntervalMin != 15 {
		t.Errorf("expected default RefreshIntervalMin=15, got %d", cfg.RefreshIntervalMin)
	}
	if cfg.BallX != -1 || cfg.BallY != -1 {
		t.Errorf("expected default BallX=-1, BallY=-1, got %d,%d", cfg.BallX, cfg.BallY)
	}
	if cfg.Opacity != 1.0 {
		t.Errorf("expected default Opacity=1.0, got %f", cfg.Opacity)
	}
	if len(cfg.Providers) != len(AllProviderIDs) {
		t.Fatalf("expected %d providers, got %d", len(AllProviderIDs), len(cfg.Providers))
	}
	// 默认启用前三个,其余关闭
	for _, p := range cfg.Providers {
		wantEnabled := false
		for _, d := range DefaultProviderIDs {
			if p.ID == d {
				wantEnabled = true
				break
			}
		}
		if p.Enabled != wantEnabled {
			t.Errorf("provider %s: expected enabled=%v, got %v", p.ID, wantEnabled, p.Enabled)
		}
	}
}

func TestSaveThenLoad_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("APPDATA", tmpDir)

	original := &Config{
		Providers: []ProviderConfig{
			{ID: "kimi", Enabled: true, Keys: []map[string]string{
				{"api_key": "k1"},
				{"api_key": "k2"},
			}},
			{ID: "xfyun", Enabled: false},
			{ID: "opencode-go", Enabled: true, Keys: []map[string]string{
				{"workspace_id": "w1", "session_token": "s1"},
			}},
			{ID: "mimo", Enabled: false},
			{ID: "deepseek", Enabled: true, Creds: map[string]string{"api_key": "d1"}, Budgets: []float64{500}},
		},
		RefreshIntervalMin: 30,
		BallX:              100,
		BallY:              200,
		Opacity:            0.65,
	}

	err := Save(original)
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// 验证文件确实创建在正确路径
	expectedPath := filepath.Join(tmpDir, "quota-viewer", "config.json")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("config file not created at %s", expectedPath)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(loaded.Providers) != len(original.Providers) {
		t.Fatalf("Providers length mismatch: got %d", len(loaded.Providers))
	}
	for i, p := range loaded.Providers {
		o := original.Providers[i]
		if p.ID != o.ID || p.Enabled != o.Enabled {
			t.Errorf("Providers[%d] mismatch: got %+v, want %+v", i, p, o)
		}
		gotKeys, wantKeys := p.CredKeys(), o.CredKeys()
		if len(gotKeys) != len(wantKeys) {
			t.Errorf("Providers[%d] Keys count mismatch: got %d, want %d", i, len(gotKeys), len(wantKeys))
		}
		for gi, gk := range gotKeys {
			for k, v := range wantKeys[gi] {
				if gk[k] != v {
					t.Errorf("Providers[%d].Keys[%d][%s] mismatch: got %s, want %s", i, gi, k, gk[k], v)
				}
			}
		}
		if len(p.Budgets) != len(o.Budgets) {
			t.Errorf("Providers[%d] Budgets count mismatch: got %d, want %d", i, len(p.Budgets), len(o.Budgets))
		}
		for bi := range o.Budgets {
			if bi < len(p.Budgets) && p.Budgets[bi] != o.Budgets[bi] {
				t.Errorf("Providers[%d].Budgets[%d] mismatch: got %f, want %f", i, bi, p.Budgets[bi], o.Budgets[bi])
			}
		}
	}
	if loaded.RefreshIntervalMin != 30 {
		t.Errorf("RefreshIntervalMin mismatch: got %d", loaded.RefreshIntervalMin)
	}
	if loaded.BallX != 100 || loaded.BallY != 200 {
		t.Errorf("Ball position mismatch: got %d,%d", loaded.BallX, loaded.BallY)
	}
	if loaded.Opacity != 0.65 {
		t.Errorf("Opacity mismatch: got %f, want 0.65", loaded.Opacity)
	}
}

func TestSave_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("APPDATA", tmpDir)

	// 确保目录不存在
	dir := filepath.Join(tmpDir, "quota-viewer")
	os.RemoveAll(dir)

	cfg := &Config{Providers: Default().Providers}
	err := Save(cfg)
	if err != nil {
		t.Fatalf("Save() should create directory, got error: %v", err)
	}
}

// 旧版扁平格式(含 mimo_cookie 与 opencode_go 字段)迁移到 providers 结构。
func TestLoad_MigrateLegacyJSON(t *testing.T) {
	writeConfig(t, `{
	  "kimi_api_key": "k",
	  "xfyun_cookie": "x",
	  "mimo_cookie": "session=abc",
	  "opencode_go_workspace_id": "ws1",
	  "opencode_go_session_token": "tok1",
	  "refresh_interval_min": 5,
	  "ball_x": 10,
	  "ball_y": 20
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	// 有凭证的 provider 都 enabled,凭证迁移正确
	byID := map[string]ProviderConfig{}
	for _, p := range cfg.Providers {
		byID[p.ID] = p
	}

	if !byID["kimi"].Enabled || byID["kimi"].CredKeys()[0]["api_key"] != "k" {
		t.Errorf("kimi migrate wrong: %+v", byID["kimi"])
	}
	if !byID["xfyun"].Enabled || byID["xfyun"].CredKeys()[0]["cookie"] != "x" {
		t.Errorf("xfyun migrate wrong: %+v", byID["xfyun"])
	}
	// mimo 有凭证:不再钳制,保持 enabled,凭证迁移为 Keys 单组
	if !byID["mimo"].Enabled {
		t.Errorf("mimo should stay enabled after migration: %+v", byID["mimo"])
	}
	if keys := byID["mimo"].CredKeys(); len(keys) != 1 || keys[0]["cookie"] != "session=abc" {
		t.Errorf("mimo creds should migrate to Keys: %+v", byID["mimo"])
	}
	oc := byID["opencode-go"]
	if !oc.Enabled || oc.CredKeys()[0]["workspace_id"] != "ws1" || oc.CredKeys()[0]["session_token"] != "tok1" {
		t.Errorf("opencode-go migrate wrong: %+v", oc)
	}
	// deepseek 无旧字段 → 不启用
	if byID["deepseek"].Enabled {
		t.Errorf("deepseek should not be enabled after migration: %+v", byID["deepseek"])
	}
	// 通用项保留
	if cfg.RefreshIntervalMin != 5 {
		t.Errorf("RefreshIntervalMin mismatch: got %d", cfg.RefreshIntervalMin)
	}
	if cfg.BallX != 10 || cfg.BallY != 20 {
		t.Errorf("Ball position mismatch: got %d,%d", cfg.BallX, cfg.BallY)
	}

	// 迁移后文件已回写为新格式
	tmpDir := os.Getenv("APPDATA")
	newPath := filepath.Join(tmpDir, "quota-viewer", "config.json")
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("migrated config not written back: %v", err)
	}
	var probe struct {
		Providers []ProviderConfig `json:"providers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("migrated config not valid JSON: %v", err)
	}
	if len(probe.Providers) == 0 {
		t.Error("migrated config missing providers key")
	}
}

// 旧格式全部字段为空 → 默认启用前三个。
func TestLoad_MigrateLegacyEmpty_ReturnsDefaults(t *testing.T) {
	writeConfig(t, `{
	  "kimi_api_key": "",
	  "xfyun_cookie": "",
	  "refresh_interval_min": 5,
	  "ball_x": -1,
	  "ball_y": -1
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.RefreshIntervalMin != 5 {
		t.Errorf("RefreshIntervalMin mismatch: got %d", cfg.RefreshIntervalMin)
	}
	enabledCount := 0
	for _, p := range cfg.Providers {
		if p.Enabled {
			enabledCount++
		}
	}
	if enabledCount != 3 {
		t.Errorf("expected 3 default enabled providers, got %d", enabledCount)
	}
}

// SyncConfig 保存/加载往返:发布端全字段、订阅端字段、排除开关均不丢失。
func TestSyncConfig_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("APPDATA", tmpDir)

	original := &Config{
		Providers: []ProviderConfig{
			{ID: "kimi", Enabled: true, Keys: []map[string]string{
				{"api_key": "k1"}, {"api_key": "k2"},
			}, SyncExcludes: []bool{false, true}},
		},
		RefreshIntervalMin: 15,
		Opacity:            1.0,
		Sync: SyncConfig{
			Mode:            SyncModePublish,
			Password:        "pw123",
			OSSEndpoint:     "https://oss-cn-hangzhou.aliyuncs.com",
			OSSBucket:       "my-bucket",
			OSSKey:          "quota/state.enc",
			OSSAccessID:     "akid",
			OSSAccessSecret: "aksecret",
		},
	}
	if err := Save(original); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Sync != original.Sync {
		t.Errorf("Sync mismatch: got %+v, want %+v", loaded.Sync, original.Sync)
	}
	got := loaded.Providers[0].SyncExcludes
	if len(got) != 2 || got[0] != false || got[1] != true {
		t.Errorf("SyncExcludes mismatch: got %v", got)
	}
}

// 旧配置文件无 sync / sync_excludes 字段 → 零值(模式关闭、全部组同步)。
func TestLoad_NoSyncFields_Defaults(t *testing.T) {
	writeConfig(t, `{"providers": [{"id": "kimi", "enabled": true, "keys": [{"api_key": "k1"}]}]}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Sync.Mode != SyncModeOff {
		t.Errorf("Sync.Mode = %q, 应为空(关闭)", cfg.Sync.Mode)
	}
	if len(cfg.Providers[0].SyncExcludes) != 0 {
		t.Errorf("SyncExcludes 应为空, got %v", cfg.Providers[0].SyncExcludes)
	}
}

// AlignSyncExcludes 对齐:截断 / 补 false / n ≤ 0 返回 nil。
func TestAlignSyncExcludes(t *testing.T) {
	got := AlignSyncExcludes([]bool{true, false, true}, 2)
	if len(got) != 2 || got[0] != true || got[1] != false {
		t.Errorf("截断错误: %v", got)
	}

	got = AlignSyncExcludes([]bool{true}, 3)
	if len(got) != 3 || got[0] != true || got[1] != false || got[2] != false {
		t.Errorf("补齐错误: %v", got)
	}

	if got := AlignSyncExcludes([]bool{true}, 0); got != nil {
		t.Errorf("n=0 应为 nil, got %v", got)
	}
}

// CredKeys 兼容性:Keys 优先;无 Keys 时 Creds 视为单组;均空返回 nil。
func TestCredKeys_Compat(t *testing.T) {
	// Keys 有值 → 返回 Keys
	p := ProviderConfig{Keys: []map[string]string{{"api_key": "a"}, {"api_key": "b"}}}
	if keys := p.CredKeys(); len(keys) != 2 || keys[1]["api_key"] != "b" {
		t.Errorf("expected Keys passthrough, got %+v", keys)
	}

	// 无 Keys、有 Creds → 视为单组
	p = ProviderConfig{Creds: map[string]string{"api_key": "x"}}
	if keys := p.CredKeys(); len(keys) != 1 || keys[0]["api_key"] != "x" {
		t.Errorf("expected single group from Creds, got %+v", keys)
	}

	// 均空 → nil
	p = ProviderConfig{}
	if keys := p.CredKeys(); keys != nil {
		t.Errorf("expected nil, got %+v", keys)
	}
}

// 旧渠道级预算(budget)迁移为逐凭证组预算(budgets):
// 单凭证 → budgets 单值;多凭证 → 每凭证组沿用原渠道预算。
func TestLoad_MigrateChannelBudgetToPerKey(t *testing.T) {
	writeConfig(t, `{
	  "providers": [
	    {"id": "deepseek", "enabled": true, "keys": [{"api_key": "d1"}], "budget": 100},
	    {"id": "aliyun", "enabled": true, "keys": [{"api_key": "a1"}, {"api_key": "a2"}], "budget": 500}
	  ]
	}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	byID := map[string]ProviderConfig{}
	for _, p := range cfg.Providers {
		byID[p.ID] = p
	}

	ds := byID["deepseek"]
	if ds.Budget != 0 {
		t.Errorf("deepseek: legacy Budget should be cleared, got %f", ds.Budget)
	}
	if len(ds.Budgets) != 1 || ds.Budgets[0] != 100 {
		t.Errorf("deepseek: expected Budgets=[100], got %v", ds.Budgets)
	}

	al := byID["aliyun"]
	if al.Budget != 0 {
		t.Errorf("aliyun: legacy Budget should be cleared, got %f", al.Budget)
	}
	if len(al.Budgets) != 2 || al.Budgets[0] != 500 || al.Budgets[1] != 500 {
		t.Errorf("aliyun: expected Budgets=[500 500], got %v", al.Budgets)
	}
}

func TestLoad_OpacityClamping(t *testing.T) {
	cases := []struct {
		name string
		json string
		want float64
	}{
		// 新格式缺 opacity 字段(旧版本配置)→ 回退 1.0
		{"missing field", `{"providers": [{"id": "kimi"}]}`, 1.0},
		{"zero value", `{"providers": [{"id": "kimi"}], "opacity": 0}`, 1.0},
		{"negative", `{"providers": [{"id": "kimi"}], "opacity": -1}`, 1.0},
		{"too large", `{"providers": [{"id": "kimi"}], "opacity": 2}`, 1.0},
		{"valid", `{"providers": [{"id": "kimi"}], "opacity": 0.4}`, 0.4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeConfig(t, tc.json)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if cfg.Opacity != tc.want {
				t.Errorf("Opacity mismatch: got %f, want %f", cfg.Opacity, tc.want)
			}
		})
	}
}
