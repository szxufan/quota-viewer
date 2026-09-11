package fetcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const newAPIQuotaScale = 500000.0

// NewAPIFetcher 通过 New API 渠道详情接口查询余额和已用金额。
// 端点: GET {BaseUrl}/api/channel/{channel_id}
// 认证: New-Api-User + Authorization
type NewAPIFetcher struct {
	baseURL       string
	channelID     string
	user          string
	authorization string
}

// NewNewAPIFetcher 创建一个新的 New API fetcher。
func NewNewAPIFetcher(baseURL, channelID, user, authorization string) *NewAPIFetcher {
	return &NewAPIFetcher{
		baseURL:       baseURL,
		channelID:     channelID,
		user:          user,
		authorization: authorization,
	}
}

type newAPIChannelResponse struct {
	Data *newAPIChannelData `json:"data"`
}

type newAPIChannelData struct {
	Balance   float64 `json:"balance"`
	UsedQuota float64 `json:"used_quota"`
}

func (f *NewAPIFetcher) Fetch() QuotaResult {
	result := QuotaResult{
		Platform:    "New API",
		Kind:        KindUsage,
		LastUpdated: time.Now(),
	}

	baseURL := strings.TrimSpace(f.baseURL)
	channelID := strings.TrimSpace(f.channelID)
	user := strings.TrimSpace(f.user)
	authorization := strings.TrimSpace(f.authorization)
	if baseURL == "" {
		result.Error = "未配置 New API BaseUrl"
		return result
	}
	if channelID == "" {
		result.Error = "未配置 New API channel_id"
		return result
	}
	if user == "" {
		result.Error = "未配置 New API User"
		return result
	}
	if authorization == "" {
		result.Error = "未配置 New API Authorization"
		return result
	}

	endpoint, err := newAPIChannelURL(baseURL, channelID)
	if err != nil {
		result.Error = fmt.Sprintf("创建请求失败: %v", err)
		return result
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		result.Error = fmt.Sprintf("创建请求失败: %v", err)
		return result
	}
	req.Header.Set("New-Api-User", user)
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("请求失败: %v", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		result.Error = "New API 凭证无效或已过期"
		return result
	}
	if resp.StatusCode != http.StatusOK {
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	var body newAPIChannelResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		result.Error = fmt.Sprintf("解析响应失败: %v", err)
		return result
	}
	if body.Data == nil {
		result.Error = "响应中未找到渠道数据"
		return result
	}

	usedAmount := body.Data.UsedQuota / newAPIQuotaScale
	result.Balance = body.Data.Balance
	result.Used = usedAmount
	result.Remaining = fmt.Sprintf(
		"余额 %s；已用金额 %s",
		strconv.FormatFloat(body.Data.Balance, 'f', -1, 64),
		strconv.FormatFloat(usedAmount, 'f', -1, 64),
	)
	return result
}

func newAPIChannelURL(baseURL, channelID string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("BaseUrl 必须是完整 URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("BaseUrl 仅支持 http 或 https")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("BaseUrl 不能包含查询参数或片段")
	}
	return parsed.JoinPath("api", "channel", channelID).String(), nil
}
