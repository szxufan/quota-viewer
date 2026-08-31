package fetcher

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseAliyunPackageTypes(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"ots", []string{"ots"}},
		{"cdt,ots", []string{"ots", "cdt"}},           // 归一为固定顺序
		{"ots,ots,flowbag", []string{"ots", "flowbag"}}, // 去重
		{" ots , flowbag ", []string{"ots", "flowbag"}}, // 去空白
		{"unknown,ots", []string{"ots"}},               // 过滤未知值
		{"unknown", nil},
	}
	for _, c := range cases {
		got := ParseAliyunPackageTypes(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ParseAliyunPackageTypes(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseAliyunPackageTypes(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

// aliyunMultiTestServer 按 Action 分发:余额查询返回固定成功响应,
// 实例查询返回给定的 JSON 字符串(可按页不同)。
func aliyunMultiTestServer(t *testing.T, instancePages map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("Action") {
		case "QueryAccountBalance":
			w.Write([]byte(`{"Code":"200","Success":true,"Data":{"AvailableAmount":"100.00","Currency":"CNY"}}`))
		case "QueryResourcePackageInstances":
			page := r.URL.Query().Get("PageNum")
			body, ok := instancePages[page]
			if !ok {
				body = `{"Code":"200","Success":true,"Data":{"TotalCount":0,"Instances":{"Instance":[]}}}`
			}
			w.Write([]byte(body))
		default:
			t.Errorf("unexpected Action %q", r.URL.Query().Get("Action"))
			w.Write([]byte(`{"Success":false}`))
		}
	}))
}

func TestAliyunFetcher_FetchMulti_NoTypes_OnlyBalance(t *testing.T) {
	server := aliyunMultiTestServer(t, nil)
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	results := f.FetchMulti()
	if len(results) != 1 {
		t.Fatalf("expected 1 result (balance only), got %d", len(results))
	}
	if results[0].Kind != KindBalance || results[0].Error != "" {
		t.Errorf("expected balance result without error, got %+v", results[0])
	}
}

func TestAliyunFetcher_FetchMulti_NoCreds_OnlyBalanceError(t *testing.T) {
	f := NewAliyunFetcher("", "")
	f.packageTypes = []string{"ots"}
	results := f.FetchMulti()
	// 凭证缺失时只返回余额错误结果,不重复产生资源包错误
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == "" {
		t.Error("expected error result for missing creds")
	}
}

func TestAliyunFetcher_FetchMulti_WithTypes_Aggregates(t *testing.T) {
	instances := `{
		"InstanceId": "PK-cn-1", "TotalAmount": "100000000", "RemainingAmount": "39448283",
		"CommodityCode": "otsbag", "DeductType": "PeriodMonthlyAcc"
	},{
		"InstanceId": "PK-cn-2", "TotalAmount": "50000000", "RemainingAmount": "50000000",
		"CommodityCode": "otsbag", "DeductType": "PeriodMonthlyAcc"
	},{
		"InstanceId": "PK-cn-3", "TotalAmount": "20000000", "RemainingAmount": "0",
		"CommodityCode": "otsbag", "DeductType": "PeriodMonthlyAcc"
	},{
		"InstanceId": "flowpack-cn-1", "TotalAmount": "1000", "RemainingAmount": "620.5",
		"CommodityCode": "flowbag", "DeductType": "DeadlineAcc"
	},{
		"InstanceId": "flowpack-cn-2", "TotalAmount": "200", "RemainingAmount": "0",
		"CommodityCode": "flowbag", "DeductType": "DeadlineAcc"
	},{
		"InstanceId": "ossbag-cn-1", "TotalAmount": "500", "RemainingAmount": "500",
		"CommodityCode": "ossbag", "DeductType": "DeadlineAcc"
	}`
	page := `{"Code":"200","Success":true,"Data":{"TotalCount":6,"Instances":{"Instance":[` + instances + `]}}}`
	server := aliyunMultiTestServer(t, map[string]string{"1": page})
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	f.packageTypes = ParseAliyunPackageTypes("ots,flowbag,cdt")
	results := f.FetchMulti()

	// 余额 + 3 个选中类型 = 4 条
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if results[0].Kind != KindBalance {
		t.Errorf("expected first result to be balance, got %+v", results[0])
	}

	ots := results[1]
	if ots.ID != "aliyun-package" || ots.Platform != "阿里云资源包" {
		t.Errorf("unexpected group fields: ID=%q Platform=%q", ots.ID, ots.Platform)
	}
	if ots.KeyName != "OTS 资源包" || ots.Abbr != "OTS" || ots.Kind != KindUsage {
		t.Errorf("unexpected ots meta: %+v", ots)
	}
	// 聚合:total = 1.5e8,remaining = 89448283,used = 60551717
	if ots.Total != 150000000 || ots.Used != 60551717 {
		t.Errorf("ots aggregation wrong: Total=%v Used=%v", ots.Total, ots.Used)
	}
	wantPct := 60551717.0 / 150000000 * 100
	if ots.Percent < wantPct-0.001 || ots.Percent > wantPct+0.001 {
		t.Errorf("ots percent = %v, want %v", ots.Percent, wantPct)
	}
	if !strings.Contains(ots.Remaining, "8944.8万/1.5亿 CU·2包") {
		t.Errorf("ots Remaining = %q, want contain '8944.8万/1.5亿 CU·2包'", ots.Remaining)
	}
	if ots.ResetAt == "" {
		t.Error("expected ResetAt for monthly package")
	} else if _, err := time.Parse(time.RFC3339, ots.ResetAt); err != nil {
		t.Errorf("ResetAt not RFC3339: %q", ots.ResetAt)
	}

	flow := results[2]
	if flow.KeyName != "共享流量包" {
		t.Errorf("flow KeyName = %q", flow.KeyName)
	}
	// ossbag 实例不计入流量包聚合
	if flow.Total != 1000 || flow.Used != 379.5 {
		t.Errorf("flow aggregation wrong: Total=%v Used=%v", flow.Total, flow.Used)
	}
	if !strings.Contains(flow.Remaining, "GB·1包") {
		t.Errorf("flow Remaining = %q, want contain 'GB·1包'", flow.Remaining)
	}
	if flow.ResetAt != "" {
		t.Errorf("DeadlineAcc should have no ResetAt, got %q", flow.ResetAt)
	}

	cdt := results[3]
	if cdt.Remaining != "无生效中资源包" || cdt.Total != 0 || cdt.Percent != 0 {
		t.Errorf("cdt empty case wrong: %+v", cdt)
	}
}

func TestAliyunFetcher_FetchMulti_Pagination(t *testing.T) {
	// 第一页 300 条(满页触发翻页),第二页 1 条
	var b strings.Builder
	for i := 0; i < aliyunPackagePageSize; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"InstanceId":"fp-` + strconv.Itoa(i) + `","TotalAmount":"100","RemainingAmount":"50","CommodityCode":"flowbag","DeductType":"DeadlineAcc"}`)
	}
	page1 := `{"Code":"200","Success":true,"Data":{"TotalCount":301,"Instances":{"Instance":[` + b.String() + `]}}}`
	page2 := `{"Code":"200","Success":true,"Data":{"TotalCount":301,"Instances":{"Instance":[
		{"InstanceId":"fp-300","TotalAmount":"100","RemainingAmount":"50","CommodityCode":"flowbag","DeductType":"DeadlineAcc"}
	]}}}`

	var gotPages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch q.Get("Action") {
		case "QueryAccountBalance":
			w.Write([]byte(`{"Code":"200","Success":true,"Data":{"AvailableAmount":"1.00","Currency":"CNY"}}`))
		case "QueryResourcePackageInstances":
			if q.Get("PageSize") != strconv.Itoa(aliyunPackagePageSize) {
				t.Errorf("expected PageSize=%d, got %q", aliyunPackagePageSize, q.Get("PageSize"))
			}
			gotPages = append(gotPages, q.Get("PageNum"))
			if q.Get("PageNum") == "1" {
				w.Write([]byte(page1))
			} else {
				w.Write([]byte(page2))
			}
		}
	}))
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	f.packageTypes = []string{"flowbag"}
	results := f.FetchMulti()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	flow := results[1]
	if flow.Error != "" {
		t.Fatalf("unexpected error: %s", flow.Error)
	}
	if !strings.Contains(flow.Remaining, "·301包") {
		t.Errorf("expected 301 packages aggregated, Remaining=%q", flow.Remaining)
	}
	if len(gotPages) != 2 || gotPages[0] != "1" || gotPages[1] != "2" {
		t.Errorf("expected pages [1 2], got %v", gotPages)
	}
}

func TestAliyunFetcher_FetchMulti_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("Action") {
		case "QueryAccountBalance":
			w.Write([]byte(`{"Code":"200","Success":true,"Data":{"AvailableAmount":"1.00","Currency":"CNY"}}`))
		default:
			w.Write([]byte(`{"Code":"NotAuthorized","Message":"no permission","Success":false}`))
		}
	}))
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	f.packageTypes = []string{"ots"}
	results := f.FetchMulti()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Error != "" {
		t.Errorf("balance should succeed, got %q", results[0].Error)
	}
	if !strings.Contains(results[1].Error, "NotAuthorized") || !strings.Contains(results[1].Error, "no permission") {
		t.Errorf("expected API error detail, got %q", results[1].Error)
	}
}

// TestBuildAliyunPackageResult_ExhaustedExcluded 已用完(剩余 0)的实例不计入聚合。
func TestBuildAliyunPackageResult_ExhaustedExcluded(t *testing.T) {
	instances := []aliyunPkgInstance{
		{CommodityCode: "flowbag", TotalAmount: "1000", RemainingAmount: "400", DeductType: "DeadlineAcc"},
		{CommodityCode: "flowbag", TotalAmount: "500", RemainingAmount: "0", DeductType: "DeadlineAcc"},
	}
	r := buildAliyunPackageResult(aliyunPackageTypes[1], instances)
	if r.Total != 1000 || r.Used != 600 {
		t.Errorf("exhausted instance should be excluded: Total=%v Used=%v", r.Total, r.Used)
	}
	if !strings.Contains(r.Remaining, "·1包") {
		t.Errorf("Remaining = %q, want contain '·1包'", r.Remaining)
	}
}

// TestBuildAliyunPackageResult_AllExhausted_StillCounted 全部实例用完时仍然计入,展示用尽状态。
func TestBuildAliyunPackageResult_AllExhausted_StillCounted(t *testing.T) {
	instances := []aliyunPkgInstance{
		{CommodityCode: "cdt_Resource_dp_cn", TotalAmount: "500", RemainingAmount: "0", DeductType: "DeadlineAcc"},
		{CommodityCode: "cdt_Resource_dp_cn", TotalAmount: "300", RemainingAmount: "0", DeductType: "DeadlineAcc"},
	}
	r := buildAliyunPackageResult(aliyunPackageTypes[2], instances)
	if r.Total != 800 || r.Used != 800 || r.Percent != 100 {
		t.Errorf("all-exhausted should be counted: Total=%v Used=%v Percent=%v", r.Total, r.Used, r.Percent)
	}
	if !strings.Contains(r.Remaining, "0/800 GB·2包") {
		t.Errorf("Remaining = %q, want contain '0/800 GB·2包'", r.Remaining)
	}
}

func TestFormatAliyunAmount(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{100000000, "1亿"},
		{150000000, "1.5亿"},
		{39448283, "3944.8万"},
		{10000, "1万"},
		{9999, "9,999"},
		{620.5, "621"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := formatAliyunAmount(c.in); got != c.want {
			t.Errorf("formatAliyunAmount(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNextMonthStart(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	cases := []struct{ in, want time.Time }{
		{time.Date(2026, 8, 31, 10, 30, 0, 0, loc), time.Date(2026, 9, 1, 0, 0, 0, 0, loc)},
		{time.Date(2026, 12, 15, 0, 0, 0, 0, loc), time.Date(2027, 1, 1, 0, 0, 0, 0, loc)}, // 跨年
		{time.Date(2026, 1, 1, 0, 0, 0, 0, loc), time.Date(2026, 2, 1, 0, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		if got := nextMonthStart(c.in); !got.Equal(c.want) {
			t.Errorf("nextMonthStart(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestBuildAndFetch_Dispatch 验证单结果/多结果 fetcher 的统一分发(不发真实请求)。
func TestBuildAndFetch_Dispatch(t *testing.T) {
	single := ProviderDef{
		ID: "single",
		Build: func(map[string]string) Fetcher {
			return stubFetcher{result: QuotaResult{Platform: "S"}}
		},
	}
	multi := ProviderDef{
		ID: "multi",
		Build: func(map[string]string) Fetcher {
			return stubMultiFetcher{results: []QuotaResult{{Platform: "M1"}, {Platform: "M2"}}}
		},
	}
	if rs := BuildAndFetch(single, nil); len(rs) != 1 || rs[0].Platform != "S" {
		t.Errorf("single dispatch wrong: %+v", rs)
	}
	if rs := BuildAndFetch(multi, nil); len(rs) != 2 || rs[1].Platform != "M2" {
		t.Errorf("multi dispatch wrong: %+v", rs)
	}
}

type stubFetcher struct{ result QuotaResult }

func (s stubFetcher) Fetch() QuotaResult { return s.result }

type stubMultiFetcher struct{ results []QuotaResult }

func (s stubMultiFetcher) Fetch() QuotaResult        { return s.results[0] }
func (s stubMultiFetcher) FetchMulti() []QuotaResult { return s.results }

// TestAliyunProvider_PackageTypesField 保证注册表中阿里云渠道的资源包选项完整。
func TestAliyunProvider_PackageTypesField(t *testing.T) {
	def, ok := Get("aliyun")
	if !ok {
		t.Fatal("aliyun provider not found")
	}
	var field *CredentialField
	for i := range def.Fields {
		if def.Fields[i].Key == "package_types" {
			field = &def.Fields[i]
			break
		}
	}
	if field == nil {
		t.Fatal("package_types field missing")
	}
	if field.Type != "select" || !field.Multiple || !field.Plain {
		t.Errorf("package_types field meta wrong: %+v", *field)
	}
	want := map[string]string{
		"ots":     "otsbag",
		"flowbag": "flowbag",
		"cdt":     "cdt_Resource_dp_cn",
	}
	if len(field.Options) != len(want) {
		t.Fatalf("expected %d options, got %d", len(want), len(field.Options))
	}
	for _, o := range field.Options {
		if _, ok := want[o.Value]; !ok {
			t.Errorf("unexpected option %q", o.Value)
		}
		if o.Label == "" {
			t.Errorf("option %q missing label", o.Value)
		}
	}
	// 选项值与类型表一一对应
	for _, pt := range aliyunPackageTypes {
		if pt.CommodityCode != want[pt.Value] {
			t.Errorf("type %s commodity = %s, want %s", pt.Value, pt.CommodityCode, want[pt.Value])
		}
		if fmt.Sprint(pt.Label) == "" || pt.Unit == "" || pt.Abbr == "" {
			t.Errorf("type %s incomplete: %+v", pt.Value, pt)
		}
	}
}
