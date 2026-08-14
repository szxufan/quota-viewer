package fetcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	mimoDefaultUsageURL   = "https://platform.xiaomimimo.com/api/v1/tokenPlan/usage"
	mimoDefaultBalanceURL = "https://platform.xiaomimimo.com/api/v1/balance"
)

// mimoGenLoginURL 生成小米登录地址(内含 sts 回调与 sign)的接口,是自动换取 Cookie 链路的第一步。
var mimoGenLoginURL = "https://platform.xiaomimimo.com/api/v1/genLoginUrl"

// mimoUserAgent 访问小米 passport 时的 User-Agent(与浏览器一致,降低被风控的概率)。
const mimoUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36 Edg/151.0.0.0"

// errMimoCookieExpired 表示 MiMo Cookie 失效(401/302),可尝试用小米账号 Cookie 自动换取。
var errMimoCookieExpired = errors.New("Cookie 已过期或不足(需包含 httponly cookie)")

// MiMoFetcher 通过 Cookie 调用小米 MiMo 额度 JSON API。
// 端点: GET /api/v1/tokenPlan/usage(套餐用量)
//
//	GET /api/v1/balance(按量余额;usage 无有效数据时兜底,像 DeepSeek 一样展示)
//
// 认证: Cookie 头(需包含 httponly cookie,前端 document.cookie 取不到)
// Referer: https://platform.xiaomimimo.com/console/plan-manage
// 可选配置小米账号 Cookie:MiMo Cookie 缺失或失效时自动走小米 STS 链路换取新 Cookie。
type MiMoFetcher struct {
	cookie       string            // MiMo 平台 Cookie(platform.xiaomimimo.com)
	xiaomiCookie string            // 小米账号 Cookie(account.xiaomi.com),可选
	apiURL       string
	balanceURL   string
	updatedCreds map[string]string // 换取产生的新凭证,供上层写回配置
}

func NewMiMoFetcher(cookie, xiaomiCookie, apiURL string) *MiMoFetcher {
	if apiURL == "" {
		apiURL = mimoDefaultUsageURL
	}
	return &MiMoFetcher{
		cookie:       cookie,
		xiaomiCookie: xiaomiCookie,
		apiURL:       apiURL,
		balanceURL:   mimoDefaultBalanceURL,
	}
}

// newNoRedirectClient 返回不跟随重定向的客户端:STS 换取链路需要逐步读取每步的 Location。
func newNoRedirectClient(timeout time.Duration) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Timeout: timeout,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// doGet 发送带 Cookie 的 GET 请求并返回响应体;401/302/非 200 返回带含义的错误。
func (m *MiMoFetcher) doGet(url string) ([]byte, error) {
	client := newNoRedirectClient(10 * time.Second)

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
		return nil, fmt.Errorf("%w,请更新", errMimoCookieExpired)
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

	if m.cookie == "" && m.xiaomiCookie == "" {
		result.Error = "未配置 MiMo Cookie"
		return result
	}

	// 仅配置了小米账号 Cookie:先自动换取 MiMo Cookie
	if m.cookie == "" {
		if err := m.refreshFromXiaomi(); err != nil {
			result.Error = err.Error()
			return result
		}
	}

	// 1) 优先查询套餐用量
	body, err := m.doGet(m.apiURL)
	if err != nil {
		// MiMo Cookie 失效且配置了小米账号 Cookie:自动换取后重试一次
		if errors.Is(err, errMimoCookieExpired) && m.xiaomiCookie != "" {
			if rerr := m.refreshFromXiaomi(); rerr == nil {
				body, err = m.doGet(m.apiURL)
			}
		}
		if err != nil {
			result.Error = err.Error()
			result.UpdatedCreds = m.updatedCreds
			return result
		}
	}
	if r, ok := m.parseUsage(body); ok {
		r.UpdatedCreds = m.updatedCreds
		return r
	}

	// 2) usage 无有效数据,回退查询按量余额
	balanceBody, err := m.doGet(m.balanceURL)
	if err != nil {
		result.Error = fmt.Sprintf("用量数据不可用,余额查询失败: %v", err)
		result.UpdatedCreds = m.updatedCreds
		return result
	}
	r := m.parseBalance(balanceBody)
	r.UpdatedCreds = m.updatedCreds
	return r
}

// refreshFromXiaomi 用小米账号 Cookie 换取新的 MiMo Cookie:
// 成功则更新 m.cookie,并记录 updatedCreds 供上层写回配置。
func (m *MiMoFetcher) refreshFromXiaomi() error {
	newCookie, err := exchangeMiMoCookie(m.xiaomiCookie)
	if err != nil {
		return err
	}
	m.cookie = newCookie
	m.updatedCreds = map[string]string{"cookie": newCookie}
	return nil
}

// exchangeMiMoCookie 用小米账号 Cookie 换取 MiMo 平台 Cookie(返回 Cookie 头格式字符串)。
// 链路(与浏览器登录一致,样例见 docs):
//  1. GET /api/v1/genLoginUrl -> 302 Location 为 account.xiaomi.com 的 serviceLogin 地址(含 sts 回调与 sign)
//  2. GET serviceLogin(带小米账号 Cookie)-> 302 Location 为 platform.xiaomimimo.com/sts?...
//  3. GET sts -> Set-Cookie 下发 api-platform_serviceToken 等 MiMo 凭证
func exchangeMiMoCookie(xiaomiCookie string) (string, error) {
	loginURL, err := fetchMiMoLoginURL()
	if err != nil {
		return "", fmt.Errorf("获取小米登录地址失败: %v", err)
	}
	stsURL, err := fetchMimoSTSURL(loginURL, xiaomiCookie)
	if err != nil {
		return "", err
	}
	return fetchMimoCookies(stsURL)
}

// fetchMiMoLoginURL 链路第 1 步:获取 account.xiaomi.com 的 serviceLogin 地址(内含 sts 回调与 sign)。
func fetchMiMoLoginURL() (string, error) {
	loginAPI := mimoGenLoginURL + "?" + url.Values{"currentPath": {"/console/balance"}}.Encode()
	req, err := http.NewRequest("GET", loginAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", "https://platform.xiaomimimo.com/")
	req.Header.Set("User-Agent", mimoUserAgent)

	resp, err := newNoRedirectClient(20 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("接口未返回跳转地址(HTTP %d)", resp.StatusCode)
	}
	return loc, nil
}

// fetchMimoSTSURL 链路第 2 步:携带小米账号 Cookie 访问 serviceLogin,拿到 sts 换取地址。
func fetchMimoSTSURL(loginURL, xiaomiCookie string) (string, error) {
	req, err := http.NewRequest("GET", loginURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", xiaomiCookie)
	req.Header.Set("Referer", "https://platform.xiaomimimo.com/")
	req.Header.Set("User-Agent", mimoUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6")

	resp, err := newNoRedirectClient(20 * time.Second).Do(req)
	if err != nil {
		return "", fmt.Errorf("访问小米登录服务失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 302 {
		// 账号 Cookie 失效时服务端返回登录页(200)而非跳转
		return "", fmt.Errorf("小米账号 Cookie 已失效或未登录(HTTP %d),请重新获取", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("小米登录服务未返回跳转地址")
	}
	return loc, nil
}

// fetchMimoCookies 链路第 3 步:访问 sts 换取地址,收集 Set-Cookie 得到 MiMo 平台 Cookie。
func fetchMimoCookies(stsURL string) (string, error) {
	req, err := http.NewRequest("GET", stsURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", "https://platform.xiaomimimo.com/")
	req.Header.Set("User-Agent", mimoUserAgent)

	resp, err := newNoRedirectClient(20 * time.Second).Do(req)
	if err != nil {
		return "", fmt.Errorf("访问 MiMo STS 换取接口失败: %v", err)
	}
	defer resp.Body.Close()

	var parts []string
	seen := make(map[string]bool, 4)
	for _, c := range resp.Cookies() {
		switch c.Name {
		case "api-platform_serviceToken", "userId", "api-platform_slh", "api-platform_ph":
			if c.Value == "" || seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	if !seen["api-platform_serviceToken"] {
		return "", fmt.Errorf("未获得 serviceToken(HTTP %d),换取失败", resp.StatusCode)
	}
	return strings.Join(parts, "; "), nil
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
