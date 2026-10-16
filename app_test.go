package main

import (
	"testing"

	"quota-viewer/internal/config"
	"quota-viewer/internal/fetcher"
)

// TestSaveConfig_PackageTypesRoundTrip 验证阿里云资源包类型选择(save → GetConfig 回读):
// plain 字段原样返回不掩码、敏感字段保持掩码、掩码占位提交可还原旧值。
// (回归:前端复选框未设 value 时曾把 "on,on" 存入 package_types)
func TestSaveConfig_PackageTypesRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	a := &App{cfg: config.Default()}
	err := a.SaveConfig([]ProviderInput{{
		ID:      "aliyun",
		Enabled: true,
		Keys: []map[string]string{{
			"access_key_id":     "LTAI-test-ak",
			"access_key_secret": "secret-12345",
			"package_types":     "ots,flowbag",
		}},
		KeyNames: []string{""},
		Budgets:  []float64{0},
	}}, 0)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	getAliyunKeys := func() map[string]string {
		t.Helper()
		cfg := a.GetConfig()
		providers, _ := cfg["providers"].([]map[string]interface{})
		for _, p := range providers {
			if p["id"] != "aliyun" {
				continue
			}
			keys, _ := p["keys"].([]map[string]string)
			if len(keys) != 1 {
				t.Fatalf("expected 1 key group, got %d", len(keys))
			}
			return keys[0]
		}
		t.Fatal("aliyun provider missing in GetConfig")
		return nil
	}

	k := getAliyunKeys()
	if k["package_types"] != "ots,flowbag" {
		t.Errorf("package_types = %q, want %q(plain 字段不得掩码)", k["package_types"], "ots,flowbag")
	}
	if k["access_key_id"] == "LTAI-test-ak" || k["access_key_secret"] == "secret-12345" {
		t.Errorf("敏感字段必须掩码: ak=%q sk=%q", k["access_key_id"], k["access_key_secret"])
	}

	// 第二次保存:模拟前端原样提交掩码占位 + 更新类型选择
	err = a.SaveConfig([]ProviderInput{{
		ID:      "aliyun",
		Enabled: true,
		Keys: []map[string]string{{
			"access_key_id":     k["access_key_id"],
			"access_key_secret": k["access_key_secret"],
			"package_types":     "cdt",
		}},
		KeyNames: []string{""},
		Budgets:  []float64{0},
	}}, 0)
	if err != nil {
		t.Fatalf("second SaveConfig: %v", err)
	}

	k = getAliyunKeys()
	if k["package_types"] != "cdt" {
		t.Errorf("package_types 更新后 = %q, want %q", k["package_types"], "cdt")
	}
	// 掩码还原后真实 AK 应完好
	for _, pc := range a.cfg.Providers {
		if pc.ID != "aliyun" {
			continue
		}
		if got := pc.CredKeys()[0]["access_key_id"]; got != "LTAI-test-ak" {
			t.Errorf("掩码还原后 access_key_id = %q, want %q", got, "LTAI-test-ak")
		}
	}
}

// TestFilterSyncExcluded 发布端凭证粒度过滤:
// 命中 SyncExcludes 的组被剔除;切片越界/未配置视为同步;无排除时原样返回。
func TestFilterSyncExcluded(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{
		{ID: "kimi", SyncExcludes: []bool{false, true}}, // 组 1 排除
		{ID: "deepseek"},                                // 未配置 → 全部同步
	}}
	results := []fetcher.QuotaResult{
		{ID: "kimi", KeyIndex: 0},
		{ID: "kimi", KeyIndex: 1},
		{ID: "kimi", KeyIndex: 5}, // 越界 → 视为同步
		{ID: "deepseek", KeyIndex: 0},
		{ID: "aliyun-package", KeyIndex: 0}, // 无对应 provider → 同步
	}

	out := filterSyncExcluded(cfg, results)
	if len(out) != 4 {
		t.Fatalf("过滤后 %d 条, 应为 4: %+v", len(out), out)
	}
	for _, r := range out {
		if r.ID == "kimi" && r.KeyIndex == 1 {
			t.Fatalf("kimi 组 1 应被排除: %+v", out)
		}
	}

	// 全部排除 → 空切片
	cfg.Providers[0].SyncExcludes = []bool{true, true}
	out = filterSyncExcluded(cfg, results[:2])
	if len(out) != 0 {
		t.Fatalf("全部排除后应为空, got %+v", out)
	}

	// 无任何排除配置 → 原样返回
	cfgNoEx := &config.Config{Providers: []config.ProviderConfig{{ID: "kimi"}}}
	out = filterSyncExcluded(cfgNoEx, results)
	if len(out) != len(results) {
		t.Fatalf("未配置排除时应原样返回, got %d 条", len(out))
	}
}

// TestSaveSyncConfig 同步配置保存与掩码还原:
// 密码/AK Secret 提交掩码值保留旧值;非法模式报错;GetConfig 下发掩码。
func TestSaveSyncConfig(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	a := &App{cfg: config.Default()}
	err := a.SaveSyncConfig(config.SyncConfig{
		Mode:            config.SyncModePublish,
		Password:        "real-password",
		OSSEndpoint:     "https://oss-cn-hangzhou.aliyuncs.com",
		OSSBucket:       "b",
		OSSKey:          "quota/state.enc",
		OSSAccessID:     "akid",
		OSSAccessSecret: "real-secret",
	})
	if err != nil {
		t.Fatalf("SaveSyncConfig: %v", err)
	}

	// GetConfig 下发:密码/Secret 掩码,其余原样
	syncOut, _ := a.GetConfig()["sync"].(map[string]interface{})
	if syncOut["password"] == "real-password" || syncOut["oss_access_secret"] == "real-secret" {
		t.Errorf("密码与 Secret 必须掩码: %+v", syncOut)
	}
	if syncOut["oss_endpoint"] != "https://oss-cn-hangzhou.aliyuncs.com" || syncOut["url"] != "" {
		t.Errorf("非敏感字段应原样下发: %+v", syncOut)
	}

	// 第二次保存:提交掩码占位 → 真实值保留
	err = a.SaveSyncConfig(config.SyncConfig{
		Mode:            config.SyncModePublish,
		Password:        syncOut["password"].(string),
		OSSBucket:       "b",
		OSSKey:          "quota/state.enc",
		OSSAccessID:     "akid",
		OSSAccessSecret: syncOut["oss_access_secret"].(string),
	})
	if err != nil {
		t.Fatalf("second SaveSyncConfig: %v", err)
	}
	if a.cfg.Sync.Password != "real-password" || a.cfg.Sync.OSSAccessSecret != "real-secret" {
		t.Errorf("掩码还原失败: %+v", a.cfg.Sync)
	}

	// 非法模式报错
	if err := a.SaveSyncConfig(config.SyncConfig{Mode: "bogus"}); err == nil {
		t.Error("非法模式未报错")
	}
}

// TestSaveConfig_SyncExcludesRoundTrip 凭证组同步排除开关随 SaveConfig 保存并对齐。
func TestSaveConfig_SyncExcludesRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	a := &App{cfg: config.Default()}
	err := a.SaveConfig([]ProviderInput{{
		ID:      "kimi",
		Enabled: true,
		Keys: []map[string]string{
			{"api_key": "k1"},
			{"api_key": "k2"},
		},
		KeyNames:     []string{"", ""},
		Budgets:      []float64{0, 0},
		SyncExcludes: []bool{false, true},
	}}, 0)
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	for _, pc := range a.cfg.Providers {
		if pc.ID != "kimi" {
			continue
		}
		if len(pc.SyncExcludes) != 2 || pc.SyncExcludes[0] != false || pc.SyncExcludes[1] != true {
			t.Fatalf("SyncExcludes = %v, 应为 [false true]", pc.SyncExcludes)
		}
	}

	// GetConfig 下发 sync_excludes
	providers, _ := a.GetConfig()["providers"].([]map[string]interface{})
	for _, p := range providers {
		if p["id"] != "kimi" {
			continue
		}
		ex, _ := p["sync_excludes"].([]bool)
		if len(ex) != 2 || ex[1] != true {
			t.Errorf("下发 sync_excludes = %v, 应为 [false true]", ex)
		}
	}
}

// TestFetchAll_CredAsSite 凭证即站点渠道(New API)的展示顶替:
// 有凭证名时以凭证名/缩写代替平台名/球格缩写;缩写未配置取凭证名前两字;
// 未命名凭证与其它渠道保持 "New API"/"NA" 默认。
func TestFetchAll_CredAsSite(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	a := &App{cfg: config.Default()}
	// Default 只注册早期 5 渠道,new-api 需手动追加(与 SaveConfig 首次保存行为一致)
	a.cfg.Providers = append(a.cfg.Providers, config.ProviderConfig{
		ID:       "new-api",
		Enabled:  true,
		// 空 creds:Fetch 走参数校验快速返回错误,不发起网络请求
		Keys:     []map[string]string{{}, {}, {}},
		KeyNames: []string{"工作号", "AB 站", ""},
		KeyAbbrs: []string{"", "GW", ""},
	})

	results := a.fetchAll()
	byKey := map[int]fetcher.QuotaResult{}
	for _, r := range results {
		if r.ID == "new-api" {
			byKey[r.KeyIndex] = r
		}
	}
	if len(byKey) != 3 {
		t.Fatalf("期望 3 条 new-api 结果, got %d", len(byKey))
	}

	cases := []struct {
		idx      int
		platform string
		abbr     string
	}{
		{0, "工作号", "工作"}, // 缩写留空 → 取名称前两字
		{1, "AB 站", "GW"}, // 显式配置缩写优先
		{2, "New API", "NA"}, // 未命名凭证回退默认
	}
	for _, c := range cases {
		r, ok := byKey[c.idx]
		if !ok {
			t.Fatalf("缺少 key_index=%d 的结果", c.idx)
		}
		if r.Platform != c.platform || r.Abbr != c.abbr {
			t.Errorf("组 %d: platform=%q abbr=%q, want %q/%q", c.idx, r.Platform, r.Abbr, c.platform, c.abbr)
		}
	}

	// GetConfig 下发缩写与凭证即站点标记
	for _, p := range a.GetConfig()["providers"].([]map[string]interface{}) {
		if p["id"] != "new-api" {
			continue
		}
		if v, _ := p["cred_as_site"].(bool); !v {
			t.Errorf("下发 cred_as_site = %v, 应为 true", p["cred_as_site"])
		}
		abbrs, _ := p["key_abbrs"].([]string)
		if len(abbrs) != 3 || abbrs[1] != "GW" {
			t.Errorf("下发 key_abbrs = %v, 应为 3 个元素且 [1]=GW", p["key_abbrs"])
		}
	}
}

// TestStartAutoRefresh_Restart 自动刷新循环可重启:
// 再次调用时旧 stop 通道被关闭(旧 goroutine 退出)、生成新通道。
func TestStartAutoRefresh_Restart(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())

	a := &App{cfg: config.Default()}
	a.cfg.Sync = config.SyncConfig{Mode: config.SyncModeSubscribe, Password: "p"}
	a.startAutoRefresh()

	a.mu.Lock()
	first := a.stopAuto
	a.mu.Unlock()
	if first == nil {
		t.Fatal("未创建 stop 通道")
	}

	a.startAutoRefresh() // 模拟保存配置后的重启

	a.mu.Lock()
	second := a.stopAuto
	a.mu.Unlock()
	if second == nil || second == first {
		t.Fatal("重启后未更换 stop 通道")
	}
	select {
	case <-first:
	default:
		t.Fatal("旧 stop 通道未关闭,旧 goroutine 泄漏")
	}
}
