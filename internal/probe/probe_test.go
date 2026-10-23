package probe

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"quota-viewer/internal/config"
	"quota-viewer/internal/fetcher"
)

// fixtureLookup 注入两家 Provider(一家用量型、一家凭证即站点),不碰网络:
// 凭证里带 "fail" 的组返回错误,其余返回固定成功结果。
type funcFetcher func() fetcher.QuotaResult

func (f funcFetcher) Fetch() fetcher.QuotaResult { return f() }

func fixtureLookup(id string) (fetcher.ProviderDef, bool) {
	switch id {
	case "kimi":
		return fetcher.ProviderDef{
			ID: "kimi", DisplayName: "Kimi", Abbr: "K", Kind: fetcher.KindUsage,
			Build: func(creds map[string]string) fetcher.Fetcher {
				return funcFetcher(func() fetcher.QuotaResult {
					if creds["api_key"] == "fail" {
						return fetcher.QuotaResult{Platform: "Kimi", Kind: fetcher.KindUsage, Error: "boom"}
					}
					return fetcher.QuotaResult{
						Platform: "Kimi", Kind: fetcher.KindUsage,
						Percent: 40, Remaining: "60% 剩余",
					}
				})
			},
		}, true
	case "new-api":
		return fetcher.ProviderDef{
			ID: "new-api", DisplayName: "New API", Abbr: "NA", Kind: fetcher.KindUsage, CredAsSite: true,
			Build: func(creds map[string]string) fetcher.Fetcher {
				return funcFetcher(func() fetcher.QuotaResult {
					return fetcher.QuotaResult{Platform: "New API", Kind: fetcher.KindUsage, Percent: 10}
				})
			},
		}, true
	}
	return fetcher.ProviderDef{}, false
}

func fixtureConfig() *config.Config {
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{
		{ID: "kimi", Enabled: true, Keys: []map[string]string{
			{"api_key": "ok"}, {"api_key": "fail"},
		}},
		{ID: "new-api", Enabled: true, Keys: []map[string]string{
			{"base_url": "https://a"}, {"base_url": "https://b"},
		}, KeyNames: []string{"站点甲", "站点乙"}},
		{ID: "disabled-one", Enabled: false},
	}
	return cfg
}

// happy path:对象粒度 = 凭证组;凭证即站点渠道以凭证名顶替平台名;
// 失败凭证组的数值字段为 null。
func TestBuildReport_ObjectGranularityAndIdentity(t *testing.T) {
	rep := BuildReport(time.Now(), fixtureConfig(), fixtureLookup)

	if rep.Schema != schemaVersion {
		t.Errorf("schema = %d, want %d", rep.Schema, schemaVersion)
	}
	if rep.Error != "" {
		t.Fatalf("unexpected error: %s", rep.Error)
	}
	if rep.EnabledCount != 4 || len(rep.Objects) != 4 {
		t.Fatalf("expected 4 objects, got %d", len(rep.Objects))
	}

	// kimi 两组:第一组成功,第二组失败(null 字段)
	k0 := rep.Objects[0]
	if k0.ID != "kimi" || k0.KeyIndex != 0 || k0.Platform != "Kimi" || k0.Kind != fetcher.KindUsage {
		t.Errorf("unexpected kimi[0]: %+v", k0)
	}
	if k0.Percent == nil || *k0.Percent != 40 || k0.RemainingPercent == nil || *k0.RemainingPercent != 60 {
		t.Errorf("unexpected kimi[0] percent fields: %+v", k0)
	}
	k1 := rep.Objects[1]
	if k1.Error != "boom" {
		t.Errorf("expected kimi[1] error, got %+v", k1)
	}
	if k1.Percent != nil || k1.RemainingPercent != nil || k1.ResetAt != nil {
		t.Errorf("failed object must have null numerics, got %+v", k1)
	}

	// new-api 两组:凭证即站点 → 平台名被凭证名顶替
	n0 := rep.Objects[2]
	if n0.Platform != "站点甲" || n0.KeyName != "站点甲" {
		t.Errorf("CredAsSite substitution missing: %+v", n0)
	}
	if rep.Objects[3].Platform != "站点乙" {
		t.Errorf("expected 站点乙, got %+v", rep.Objects[3])
	}

	// 未启用渠道不得出现
	for _, o := range rep.Objects {
		if o.ID == "disabled-one" {
			t.Errorf("disabled provider leaked into report: %+v", o)
		}
	}
}

// 报告序列化成一行合法 JSON(无换行)且可通过回读校验。
func TestEncodeLine_SingleLineJSON(t *testing.T) {
	rep := BuildReport(time.Now(), fixtureConfig(), fixtureLookup)
	data, err := EncodeLine(rep)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if bytes.ContainsAny(data, "\n\r") {
		t.Error("report must be a single line")
	}
	var back Report
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if back.EnabledCount != rep.EnabledCount {
		t.Errorf("round-trip count mismatch: %d vs %d", back.EnabledCount, rep.EnabledCount)
	}
	// 失败对象的可空字段回读后是 null(Go 侧保持 nil 指针)
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("raw parse: %v", err)
	}
	objects := raw["objects"].([]any)
	failed := objects[1].(map[string]any)
	if v, ok := failed["percent"]; !ok || v != nil {
		t.Errorf("failed object percent must be null, got %v", v)
	}
}

// Run 不带 --json:stderr 出 usage,stdout 不写一个字节,退出 2。
func TestRun_NoJSONFlag_UsageAndExit2(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := Run(nil, &out, &errBuf, fixtureLookup)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout must be empty, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "usage:") || !strings.Contains(errBuf.String(), "缺少 --json") {
		t.Errorf("unexpected usage: %q", errBuf.String())
	}
}

// Run 带未知参数(无 --json):usage + 退出 2。
func TestRun_UnknownArgWithoutJSON_Exit2(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := Run([]string{"--xml"}, &out, &errBuf, fixtureLookup)
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "未知参数") {
		t.Errorf("unexpected usage: %q", errBuf.String())
	}
}

// Run --json + 未知参数:失败信封(合法 JSON + error 字段)+ stderr 诊断,退出 0。
func TestRun_JSONWithUnknownArg_FailureEnvelope(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := Run([]string{"--json", "--wat"}, &out, &errBuf, fixtureLookup)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	if !strings.Contains(errBuf.String(), "quota-viewer-cli:") {
		t.Errorf("expected stderr diagnostic, got %q", errBuf.String())
	}
	var rep Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("envelope must be valid JSON: %v (stdout=%q)", err, out.String())
	}
	if rep.Error == "" || len(rep.Objects) != 0 {
		t.Errorf("unexpected envelope: %+v", rep)
	}
}

// Run --json 快乐路径(注入 lookup → 跳过真实配置,用 Default):
// 一行合法 JSON、schema 正确、无顶层错误,退出 0(对象数由 BuildReport 测试覆盖)。
func TestRun_JSON_HappyPath(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := Run([]string{"--json"}, &out, &errBuf, fixtureLookup)
	if code != 0 {
		t.Errorf("code = %d, want 0; stderr=%q", code, errBuf.String())
	}
	var rep Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if rep.Error != "" || rep.Schema != schemaVersion {
		t.Errorf("unexpected report: %+v", rep)
	}
	if rep.Objects == nil {
		t.Error("objects must serialize as [], not null")
	}
}

// APPDATA 缺失且未注入 lookup:失败信封 + 退出 0(不是静默空结果)。
func TestRun_APPDATA_Missing_FailureEnvelope(t *testing.T) {
	t.Setenv("APPDATA", "")
	var out, errBuf bytes.Buffer
	code := Run([]string{"--json"}, &out, &errBuf, nil)
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
	var rep Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !strings.Contains(rep.Error, "APPDATA") {
		t.Errorf("expected APPDATA error, got %q", rep.Error)
	}
}

// normalizeResetAt:空串/不可解析/过去时刻 → nil;合法未来 → UTC 秒精度;
// 无时区串按 +08:00 解释。
func TestNormalizeResetAt(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	noTZ := time.Now().Add(time.Hour).In(cstZone).Format(reportTimeLayout)

	if got := normalizeResetAt(""); got != nil {
		t.Errorf("empty → nil, got %v", *got)
	}
	if got := normalizeResetAt("not-a-time"); got != nil {
		t.Errorf("invalid → nil, got %v", *got)
	}
	if got := normalizeResetAt(time.Now().Add(-time.Hour).Format(time.RFC3339)); got != nil {
		t.Errorf("past → nil, got %v", *got)
	}
	got := normalizeResetAt(future)
	if got == nil {
		t.Fatal("future → non-nil")
	}
	if strings.Contains(*got, ".") || !strings.HasSuffix(*got, "Z") {
		t.Errorf("expected UTC second precision, got %q", *got)
	}
	// 无时区串("2026-01-02 03:04:05")按 +08:00 解释后仍是未来 → 归一成功
	if g := normalizeResetAt(noTZ); g == nil {
		t.Errorf("no-tz future string → non-nil, got nil (%q)", noTZ)
	}
}
