package fetcher

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAliyunFetcher_EmptyCreds_ReturnsError(t *testing.T) {
	cases := []struct{ id, secret string }{
		{"", ""},
		{"ak-test", ""},
		{"", "sk-test"},
	}
	for _, c := range cases {
		f := NewAliyunFetcher(c.id, c.secret)
		result := f.Fetch()
		if result.Error == "" {
			t.Errorf("expected error for creds (%q,%q)", c.id, c.secret)
		}
		if result.Kind != KindBalance {
			t.Errorf("expected Kind=balance, got '%s'", result.Kind)
		}
	}
}

func TestAliyunFetcher_OK_ParsesBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("AccessKeyId") != "ak-test" {
			t.Errorf("expected AccessKeyId=ak-test, got %q", q.Get("AccessKeyId"))
		}
		if q.Get("Action") != "QueryAccountBalance" {
			t.Errorf("expected Action=QueryAccountBalance, got %q", q.Get("Action"))
		}
		if q.Get("Version") != "2017-12-14" {
			t.Errorf("expected Version=2017-12-14, got %q", q.Get("Version"))
		}
		if q.Get("Signature") == "" {
			t.Error("expected non-empty Signature parameter")
		}
		if q.Get("SignatureNonce") == "" {
			t.Error("expected non-empty SignatureNonce parameter")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"Code": "200",
			"Message": "success",
			"RequestId": "16176743-6DC7-4CB3-BB25-A13982D8DFAD",
			"Success": true,
			"Data": {
				"AvailableAmount": "10000.00",
				"CreditAmount": "0.00",
				"MybankCreditAmount": "0.00",
				"Currency": "CNY",
				"AvailableCashAmount": "10000.00"
			}
		}`))
	}))
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	result := f.Fetch()
	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Balance != 10000.00 {
		t.Errorf("expected Balance=10000.00, got %f", result.Balance)
	}
	if result.Currency != "CNY" {
		t.Errorf("expected Currency=CNY, got %s", result.Currency)
	}
	if !strings.Contains(result.Remaining, "¥") || !strings.Contains(result.Remaining, "10000.00") {
		t.Errorf("expected ¥10000.00 in Remaining, got %q", result.Remaining)
	}
	if result.Percent != 0 {
		t.Errorf("expected Percent=0 for balance kind, got %f", result.Percent)
	}
}

// TestAliyunSignature_OfficialExample 用阿里云官方签名机制文档的示例数据验证签名实现。
// 参数与期望签名均来自官方文档(Request signatures / RPC 签名机制)。
func TestAliyunSignature_OfficialExample(t *testing.T) {
	params := map[string]string{
		"AccessKeyId":      "testid",
		"Action":           "DescribeRegions",
		"Format":           "XML",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   "3ee8c1b8-83d3-44af-a94f-4e0ad82fd6cf",
		"SignatureVersion": "1.0",
		"Timestamp":        "2016-02-23T12:46:24Z",
		"Version":          "2014-05-26",
	}
	got := aliyunSignature(params, "testsecret")
	want := "OLeaidS1JvxuMvnyHOwuJ+uX5qY="
	if got != want {
		t.Errorf("signature mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestAliyunPercentEncode_SpecialChars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a b", "a%20b"},
		{"a*b", "a%2Ab"},
		{"a~b", "a~b"},
		{"2016-02-23T12:46:24Z", "2016-02-23T12%3A46%3A24Z"},
	}
	for _, c := range cases {
		if got := aliyunPercentEncode(c.in); got != c.want {
			t.Errorf("aliyunPercentEncode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAliyunFetcher_NotSuccess_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"Code": "NotAuthorized",
			"Message": "This API is not authorized for caller.",
			"Success": false
		}`))
	}))
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	result := f.Fetch()
	if result.Error == "" {
		t.Fatal("expected error when Success=false")
	}
	if !strings.Contains(result.Error, "NotAuthorized") {
		t.Errorf("expected error to contain Code, got %q", result.Error)
	}
	if !strings.Contains(result.Error, "not authorized") {
		t.Errorf("expected error to contain Message, got %q", result.Error)
	}
}

func TestAliyunFetcher_403_ReturnsInvalidKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	result := f.Fetch()
	if !strings.Contains(result.Error, "无效") && !strings.Contains(result.Error, "无权限") {
		t.Errorf("expected invalid/no-permission error, got %q", result.Error)
	}
}

func TestAliyunFetcher_BadJSON_ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer server.Close()

	f := NewAliyunFetcher("ak-test", "sk-test")
	f.apiURL = server.URL
	result := f.Fetch()
	if result.Error == "" {
		t.Error("expected error for bad JSON")
	}
}
