package main

import (
	"testing"

	"quota-viewer/internal/config"
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
