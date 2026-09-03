// Package syncstate 实现多机状态同步:发布端将配额状态加密上传到阿里云 OSS,
// 订阅端从公网地址下载解密后直接展示(见 .trae/documents/oss-state-sync.md)。
package syncstate

import (
	"time"

	"quota-viewer/internal/fetcher"
)

// payloadVersion 是当前载荷格式版本。
const payloadVersion = 1

// Payload 是发布端上传、订阅端下载的状态快照。
type Payload struct {
	Version       int                   `json:"version"`         // 固定 1
	PublishedAt   time.Time             `json:"published_at"`    // 发布时间
	NextRefreshAt time.Time             `json:"next_refresh_at"` // 预计下次刷新时间(订阅端据此 +60s 自动更新)
	Results       []fetcher.QuotaResult `json:"results"`
}

// NewPayload 构造当前时刻的载荷。
func NewPayload(results []fetcher.QuotaResult, nextRefreshAt time.Time) *Payload {
	return &Payload{
		Version:       payloadVersion,
		PublishedAt:   time.Now(),
		NextRefreshAt: nextRefreshAt,
		Results:       results,
	}
}
