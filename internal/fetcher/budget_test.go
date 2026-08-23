package fetcher

import "testing"

func TestDetectRecharge_FirstFetch_OnlyBaseline(t *testing.T) {
	r := QuotaResult{Kind: KindBalance, Balance: 150, Currency: "CNY"}
	if DetectRecharge(r, 0, false) {
		t.Error("first fetch should never count as recharge")
	}
}

func TestDetectRecharge_BalanceIncreased(t *testing.T) {
	r := QuotaResult{Kind: KindBalance, Balance: 150, Currency: "CNY"}
	if !DetectRecharge(r, 100, true) {
		t.Error("balance increase should be detected as recharge")
	}
}

func TestDetectRecharge_BalanceEqualOrDecreased(t *testing.T) {
	equal := QuotaResult{Kind: KindBalance, Balance: 100}
	if DetectRecharge(equal, 100, true) {
		t.Error("equal balance should not be recharge")
	}
	decreased := QuotaResult{Kind: KindBalance, Balance: 90}
	if DetectRecharge(decreased, 100, true) {
		t.Error("decreased balance should not be recharge")
	}
}

func TestDetectRecharge_ErrorResult_NoOp(t *testing.T) {
	r := QuotaResult{Kind: KindBalance, Balance: 150, Error: "请求失败"}
	if DetectRecharge(r, 100, true) {
		t.Error("error result should not trigger recharge")
	}
}

func TestDetectRecharge_UsageKind_NoOp(t *testing.T) {
	r := QuotaResult{Kind: KindUsage, Total: 14, Used: 1}
	if DetectRecharge(r, 10, true) {
		t.Error("usage kind (e.g. OpenRouter) should not participate")
	}
}

func TestDetectRecharge_NegativeBalance_NoOp(t *testing.T) {
	r := QuotaResult{Kind: KindBalance, Balance: -1}
	if DetectRecharge(r, 100, true) {
		t.Error("negative balance should not trigger recharge")
	}
}
