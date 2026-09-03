package syncstate

import (
	"encoding/json"
	"testing"
	"time"

	"quota-viewer/internal/fetcher"
)

// TestPayloadJSONRoundTrip 载荷(含嵌套 QuotaResult)JSON 编解码往返一致。
func TestPayloadJSONRoundTrip(t *testing.T) {
	next := time.Now().Add(time.Hour).Truncate(time.Second)
	in := NewPayload([]fetcher.QuotaResult{
		{
			Platform: "Kimi", ID: "kimi", Abbr: "K", Kind: fetcher.KindUsage,
			KeyIndex: 2, KeyName: "主账号", Used: 100, Total: 200, Percent: 50,
			Remaining: "100/200 次", ResetAt: "2026-09-10T00:00:00+08:00",
			LastUpdated: time.Now().Truncate(time.Second),
		},
	}, next)
	in.PublishedAt = in.PublishedAt.Truncate(time.Second)

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal 出错: %v", err)
	}
	var out Payload
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal 出错: %v", err)
	}
	if out.Version != 1 || !out.PublishedAt.Equal(in.PublishedAt) || !out.NextRefreshAt.Equal(next) {
		t.Errorf("载荷标头往返不一致: %+v", out)
	}
	if len(out.Results) != 1 {
		t.Fatalf("Results 长度 = %d, 应为 1", len(out.Results))
	}
	got := out.Results[0]
	if got.ID != "kimi" || got.KeyIndex != 2 || got.KeyName != "主账号" || got.Percent != 50 {
		t.Errorf("QuotaResult 往返不一致: %+v", got)
	}
}

// TestNewPayload 构造的载荷版本固定为 1,PublishedAt 接近当前时刻。
func TestNewPayload(t *testing.T) {
	before := time.Now()
	p := NewPayload(nil, before.Add(time.Minute))
	if p.Version != 1 {
		t.Errorf("Version = %d, 应为 1", p.Version)
	}
	if p.PublishedAt.Before(before) {
		t.Errorf("PublishedAt %v 早于构造时刻 %v", p.PublishedAt, before)
	}
}
