package fetcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenRouterFetcher 通过 OpenRouter Credits 接口查询账户额度。
// 端点: GET https://openrouter.ai/api/v1/credits
// 认证: Authorization: Bearer sk-or-v1-xxx
//
// 响应: {"data":{"total_credits":14,"total_usage":0.004243457}}
// 总额度 = total_credits,已用 = total_usage,剩余 = total_credits - total_usage。
type OpenRouterFetcher struct {
	apiKey string
	apiURL string // 可为空,默认为线上端点(便于测试覆盖)
}

func NewOpenRouterFetcher(apiKey string) *OpenRouterFetcher {
	return &OpenRouterFetcher{apiKey: apiKey}
}

type openRouterCreditsData struct {
	TotalCredits float64 `json:"total_credits"`
	TotalUsage   float64 `json:"total_usage"`
}

type openRouterCreditsResponse struct {
	Data openRouterCreditsData `json:"data"`
}

func (f *OpenRouterFetcher) Fetch() QuotaResult {
	result := QuotaResult{
		Platform:    "OpenRouter",
		LastUpdated: time.Now(),
	}

	if f.apiKey == "" {
		result.Error = "未配置 OpenRouter API Key"
		return result
	}

	url := f.apiURL
	if url == "" {
		url = "https://openrouter.ai/api/v1/credits"
	}

	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		result.Error = fmt.Sprintf("创建请求失败: %v", err)
		return result
	}
	req.Header.Set("Authorization", "Bearer "+f.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("请求失败: %v", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		result.Error = "API Key 无效或已过期"
		return result
	}
	if resp.StatusCode != 200 {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	var body openRouterCreditsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		result.Error = fmt.Sprintf("解析响应失败: %v", err)
		return result
	}

	total := body.Data.TotalCredits
	used := body.Data.TotalUsage
	if used < 0 {
		used = 0
	}
	result.Used = used
	result.Total = total
	if total > 0 {
		result.Percent = used / total * 100
	}
	result.Remaining = fmt.Sprintf("$%s / $%s (USD)", formatNum(used), formatNum(total))
	return result
}
