package syncstate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// downloadTimeout 公网下载超时。
const downloadTimeout = 30 * time.Second

// maxDownloadBytes 状态文件大小上限(正常载荷远小于此,防御异常响应)。
const maxDownloadBytes = 4 << 20

// Download 从公网 URL 下载加密状态文件(匿名 GET,bucket 需公共读)。
func Download(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("状态文件地址未配置")
	}
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("状态文件地址无效: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载状态文件失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载状态文件失败(HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		return nil, fmt.Errorf("读取状态文件失败: %w", err)
	}
	return data, nil
}
