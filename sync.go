package main

import (
	"context"
	"fmt"
	"time"

	"quota-viewer/internal/config"
	"quota-viewer/internal/fetcher"
	"quota-viewer/internal/syncstate"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// 多机状态同步(见 .trae/documents/oss-state-sync.md):
// 发布端每次刷新后将状态加密上传 OSS;订阅端从公网地址下载解密展示,
// 按载荷中的预计下次刷新时间 +60 秒自动更新。

// filterSyncExcluded 按各 Provider 的 SyncExcludes 剔除不同步的凭证组结果。
// 切片越界或未配置视为 false(同步);"排除"语义保证旧配置默认全量同步。
func filterSyncExcluded(cfg *config.Config, results []fetcher.QuotaResult) []fetcher.QuotaResult {
	excluded := make(map[string][]bool, len(cfg.Providers))
	for _, p := range cfg.Providers {
		if len(p.SyncExcludes) > 0 {
			excluded[p.ID] = p.SyncExcludes
		}
	}
	if len(excluded) == 0 {
		return results
	}
	out := make([]fetcher.QuotaResult, 0, len(results))
	for _, r := range results {
		if ex, ok := excluded[r.ID]; ok && r.KeyIndex >= 0 && r.KeyIndex < len(ex) && ex[r.KeyIndex] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// publishState 发布端:过滤被排除的凭证组 → AES-256-GCM 加密 → 上传 OSS。
// 失败仅推送 sync:status 事件,不影响本地展示。
func (a *App) publishState(results []fetcher.QuotaResult) {
	a.mu.Lock()
	cfg := *a.cfg // 浅拷贝,Providers 切片只读使用
	a.mu.Unlock()

	s := cfg.Sync
	if s.Mode != config.SyncModePublish {
		return
	}
	results = filterSyncExcluded(&cfg, results)
	if len(results) == 0 {
		a.emitSyncStatus("已跳过上传: 全部凭证组均被排除同步")
		return
	}
	interval := cfg.RefreshIntervalMin
	if interval <= 0 {
		interval = 15
	}
	payload := syncstate.NewPayload(results, time.Now().Add(time.Duration(interval)*time.Minute))
	data, err := syncstate.Encrypt(payload, s.Password)
	if err != nil {
		a.emitSyncStatus(err.Error())
		return
	}
	if err := syncstate.UploadOSS(context.Background(), s.OSSEndpoint, s.OSSBucket, s.OSSKey, s.OSSAccessID, s.OSSAccessSecret, data); err != nil {
		a.emitSyncStatus(err.Error())
		return
	}
	a.emitSyncStatus("已上传 " + time.Now().Format("15:04:05"))
}

// fetchRemoteState 订阅端:下载 → 解密 → 更新缓存并推送前端。
// 返回下次自动更新的等待时长(预计下次刷新时间 +60 秒,clamp 到 [60s, 24h];
// 下载/解密失败兜底 15 分钟)。
func (a *App) fetchRemoteState() time.Duration {
	const fallback = 15 * time.Minute

	a.mu.Lock()
	s := a.cfg.Sync
	a.mu.Unlock()

	data, err := syncstate.Download(context.Background(), s.URL)
	if err != nil {
		a.emitSyncStatus(err.Error())
		return fallback
	}
	payload, err := syncstate.Decrypt(data, s.Password)
	if err != nil {
		a.emitSyncStatus(err.Error())
		return fallback
	}

	a.mu.Lock()
	a.cache = payload.Results
	a.mu.Unlock()
	wailsruntime.EventsEmit(a.ctx, "quota:update", payload.Results)
	a.emitSyncStatus("已更新 " + time.Now().Format("15:04:05"))

	wait := time.Until(payload.NextRefreshAt.Add(60 * time.Second))
	if wait < 60*time.Second {
		wait = 60 * time.Second
	}
	if wait > 24*time.Hour {
		wait = 24 * time.Hour
	}
	return wait
}

// emitSyncStatus 推送最近一次上传/下载结果到前端设置区块。
func (a *App) emitSyncStatus(msg string) {
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "sync:status", msg)
	}
}

// SaveSyncConfig 保存状态同步配置。密码与 AccessKey Secret 提交掩码值时保留旧值。
// 保存后重启自动刷新循环,模式切换立即生效(注意 startAutoRefresh 会取锁,
// 不能在持锁状态下调用)。
func (a *App) SaveSyncConfig(in config.SyncConfig) error {
	a.mu.Lock()

	switch in.Mode {
	case config.SyncModeOff, config.SyncModePublish, config.SyncModeSubscribe:
	default:
		a.mu.Unlock()
		return fmt.Errorf("未知同步模式: %s", in.Mode)
	}
	// 掩码还原:提交值与旧值掩码一致 → 保留旧值(空 = 清空,与凭证字段一致)
	old := a.cfg.Sync
	if in.Password != "" && in.Password == maskSecret(old.Password) {
		in.Password = old.Password
	}
	if in.OSSAccessSecret != "" && in.OSSAccessSecret == maskSecret(old.OSSAccessSecret) {
		in.OSSAccessSecret = old.OSSAccessSecret
	}
	a.cfg.Sync = in
	err := config.Save(a.cfg)
	a.mu.Unlock()

	if err != nil {
		return err
	}
	a.startAutoRefresh()
	return nil
}

// TestSync 测试同步配置:发布模式试上传临时对象后删除;订阅模式试下载并解密。
func (a *App) TestSync() string {
	a.mu.Lock()
	s := a.cfg.Sync
	a.mu.Unlock()

	switch s.Mode {
	case config.SyncModePublish:
		testKey := s.OSSKey + ".test"
		if err := syncstate.UploadOSS(context.Background(), s.OSSEndpoint, s.OSSBucket, testKey, s.OSSAccessID, s.OSSAccessSecret, []byte("quota-viewer sync test")); err != nil {
			return "失败: " + err.Error()
		}
		_ = syncstate.DeleteOSSObject(s.OSSEndpoint, s.OSSBucket, testKey, s.OSSAccessID, s.OSSAccessSecret)
		return "成功: OSS 上传正常"
	case config.SyncModeSubscribe:
		start := time.Now()
		data, err := syncstate.Download(context.Background(), s.URL)
		if err != nil {
			return "失败: " + err.Error()
		}
		p, err := syncstate.Decrypt(data, s.Password)
		if err != nil {
			return "失败: " + err.Error()
		}
		return fmt.Sprintf("成功: %d 条状态, 发布于 %s, 耗时 %dms",
			len(p.Results), p.PublishedAt.Format("01-02 15:04"), time.Since(start).Milliseconds())
	default:
		return "未启用状态同步"
	}
}
