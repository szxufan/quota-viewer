package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"time"
)

const (
	mimoDefaultUsageURL   = "https://platform.xiaomimimo.com/api/v1/tokenPlan/usage"
	mimoDefaultBalanceURL = "https://platform.xiaomimimo.com/api/v1/balance"
)

// MiMoFetcher 通过 Cookie 调用小米 MiMo 额度 JSON API。
// 端点: GET /api/v1/tokenPlan/usage(套餐用量)
//
//	GET /api/v1/balance(按量余额;usage 无有效数据时兜底,像 DeepSeek 一样展示)
//
// 认证: Cookie 头(需包含 httponly cookie,前端 document.cookie 取不到)
// Referer: https://platform.xiaomimimo.com/console/plan-manage
type MiMoFetcher struct {
	cookie     string
	apiURL     string
	balanceURL string
}

func NewMiMoFetcher(cookie string, apiURL string) *MiMoFetcher {
	if apiURL == "" {
		apiURL = mimoDefaultUsageURL
	}
	return &MiMoFetcher{
		cookie:     cookie,
		apiURL:     apiURL,
		balanceURL: mimoDefaultBalanceURL,
	}
}

// doGet 发送带 Cookie 的 GET 请求并返回响应体;401/302/非 200 返回带含义的错误。
func (m *MiMoFetcher) doGet(url string) ([]byte, error) {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Timeout: 10 * time.Second,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	req.Header.Set("Cookie", m.cookie)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://platform.xiaomimimo.com/console/plan-manage")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 302 {
		return nil, fmt.Errorf("Cookie 已过期或不足(需包含 httponly cookie),请更新")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}
	return body, nil
}

func (m *MiMoFetcher) Fetch() QuotaResult {
	result := QuotaResult{
		Platform:    "小米MiMo",
		LastUpdated: time.Now(),
	}

	if m.cookie == "" {
		result.Error = "未配置 MiMo Cookie"
		return result
	}

	// 1) 优先查询套餐用量
	body, err := m.doGet(m.apiURL)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if r, ok := m.parseUsage(body); ok {
		return r
	}

	// 2) usage 无有效数据,回退查询按量余额
	balanceBody, err := m.doGet(m.balanceURL)
	if err != nil {
		result.Error = fmt.Sprintf("用量数据不可用,余额查询失败: %v", err)
		return result
	}
	return m.parseBalance(balanceBody)
}

// parseUsage 解析套餐用量响应;ok=false 表示响应中无有效用量数据(此时应回退余额查询)。
func (m *MiMoFetcher) parseUsage(body []byte) (QuotaResult, bool) {
	result := QuotaResult{
		Platform:    "小米MiMo",
		LastUpdated: time.Now(),
	}

	// 响应结构: {"code":0,"data":{"usage":{"percent":0.22,"items":[{"name":"plan_total_token","used":8331114938,"limit":38000000000,"percent":0.22}]}}}
	var raw struct {
		Code int `json:"code"`
		Data struct {
			Usage struct {
				Percent float64 `json:"percent"`
				Items   []struct {
					Name    string  `json:"name"`
					Used    float64 `json:"used"`
					Limit   float64 `json:"limit"`
					Percent float64 `json:"percent"`
				} `json:"items"`
			} `json:"usage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return result, false
	}

	// 主数据取第一个有 limit 的条目;所有条目一并显示
	var lines []string
	for _, item := range raw.Data.Usage.Items {
		if item.Limit > 0 {
			if result.Total == 0 {
				result.Used = item.Used
				result.Total = item.Limit
				result.Percent = item.Used / item.Limit * 100
			}
			lines = append(lines, fmt.Sprintf("%s / %s Credits", formatNum(item.Used), formatNum(item.Limit)))
		}
	}
	if len(lines) > 0 {
		result.Remaining = strings.Join(lines, "\n")
		return result, true
	}

	// 兜底:用顶层 percent
	if raw.Data.Usage.Percent > 0 {
		result.Percent = raw.Data.Usage.Percent * 100
		result.Remaining = fmt.Sprintf("%.1f%%", raw.Data.Usage.Percent*100)
		return result, true
	}

	return result, false
}

// flexibleFloat 兼容 JSON 中字符串或数字形式的浮点值(平台接口字段类型不稳定)。
type flexibleFloat float64

func (f *flexibleFloat) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		*f = flexibleFloat(v)
		return nil
	}
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = flexibleFloat(n)
	return nil
}

// parseBalance 解析按量余额响应,像 DeepSeek 一样以余额形式展示。
func (m *MiMoFetcher) parseBalance(body []byte) QuotaResult {
	result := QuotaResult{
		Platform:    "小米MiMo",
		Kind:        KindBalance,
		LastUpdated: time.Now(),
	}

	// 响应结构: {"code":0,"data":{"balance":"247.51","cashBalance":"200","giftBalance":"47.51","frozenBalance":"0","overdraftLimit":"0","currency":"CNY"}}
	var raw struct {
		Code int `json:"code"`
		Data struct {
			Balance  flexibleFloat `json:"balance"`
			Currency string        `json:"currency"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		result.Error = fmt.Sprintf("解析余额响应失败: %v", err)
		return result
	}
	if raw.Code != 0 {
		result.Error = "余额查询失败"
		return result
	}

	currency := raw.Data.Currency
	if currency == "" {
		currency = "CNY"
	}
	balance := float64(raw.Data.Balance)
	result.Balance = balance
	result.Currency = currency
	result.Remaining = fmt.Sprintf("余额 %s%s (%s)", currencySymbol(currency), strconv.FormatFloat(balance, 'f', -1, 64), currency)
	return result
}
