package fetcher

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AliyunFetcher 通过阿里云 BssOpenApi QueryAccountBalance 接口查询账户余额。
// 端点: GET https://business.aliyuncs.com/?Action=QueryAccountBalance&...
// 认证: AccessKey ID + AccessKey Secret,RPC 风格 HMAC-SHA1 签名。
// 余额型 Provider: Kind=balance,Percent 无意义(恒 0),Remaining 展示余额。
type AliyunFetcher struct {
	accessKeyID     string
	accessKeySecret string
	apiURL          string // 可为空,默认为线上端点(便于测试覆盖)
}

func NewAliyunFetcher(accessKeyID, accessKeySecret string) *AliyunFetcher {
	return &AliyunFetcher{accessKeyID: accessKeyID, accessKeySecret: accessKeySecret}
}

type aliyunBalanceData struct {
	AvailableAmount string `json:"AvailableAmount"` // 可用额度(现金 + 信控 - 欠款)
	Currency        string `json:"Currency"`        // CNY / USD / JPY
}

type aliyunBalanceResponse struct {
	Code    string            `json:"Code"`
	Message string            `json:"Message"`
	Success bool              `json:"Success"`
	Data    aliyunBalanceData `json:"Data"`
}

func (f *AliyunFetcher) Fetch() QuotaResult {
	result := QuotaResult{
		Platform:    "阿里云",
		Kind:        KindBalance,
		LastUpdated: time.Now(),
	}

	if f.accessKeyID == "" || f.accessKeySecret == "" {
		result.Error = "未配置阿里云 AccessKey"
		return result
	}

	base := f.apiURL
	if base == "" {
		base = "https://business.aliyuncs.com"
	}

	nonce, err := aliyunNonce()
	if err != nil {
		result.Error = fmt.Sprintf("生成签名随机数失败: %v", err)
		return result
	}

	params := map[string]string{
		"Action":           "QueryAccountBalance",
		"Format":           "JSON",
		"Version":          "2017-12-14",
		"AccessKeyId":      f.accessKeyID,
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureVersion": "1.0",
		"SignatureNonce":   nonce,
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	params["Signature"] = aliyunSignature(params, f.accessKeySecret)

	reqURL := base + "?" + aliyunQueryString(params)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		result.Error = fmt.Sprintf("创建请求失败: %v", err)
		return result
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		result.Error = fmt.Sprintf("请求失败: %v", err)
		return result
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 {
		result.Error = "AccessKey 无效或无权限"
		return result
	}

	var body aliyunBalanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		if resp.StatusCode != 200 {
			result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
			return result
		}
		result.Error = fmt.Sprintf("解析响应失败: %v", err)
		return result
	}

	if resp.StatusCode != 200 || !body.Success {
		if body.Message != "" {
			result.Error = fmt.Sprintf("阿里云 API 错误: %s (%s)", body.Message, body.Code)
			return result
		}
		result.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return result
	}

	amountStr := strings.TrimSpace(body.Data.AvailableAmount)
	// 余额 >= 1000 时阿里云返回带千位分隔符的字符串(如 "1,391.95"),解析前先移除逗号。
	balance, err := strconv.ParseFloat(strings.ReplaceAll(amountStr, ",", ""), 64)
	if err != nil {
		result.Error = fmt.Sprintf("解析余额失败: %q", amountStr)
		return result
	}

	result.Balance = balance
	result.Currency = body.Data.Currency
	result.Remaining = fmt.Sprintf("余额 %s%s (%s)", currencySymbol(body.Data.Currency), amountStr, body.Data.Currency)
	return result
}

// aliyunPercentEncode 按阿里云 RPC 签名规则做 RFC3986 编码:
// 空格→%20、*→%2A、%7E→~(在 url.QueryEscape 基础上修正)。
func aliyunPercentEncode(s string) string {
	s = url.QueryEscape(s)
	s = strings.ReplaceAll(s, "+", "%20")
	s = strings.ReplaceAll(s, "*", "%2A")
	s = strings.ReplaceAll(s, "%7E", "~")
	return s
}

// aliyunQueryString 按参数名 ASCII 升序拼接编码后的 query string(即规范化查询串)。
func aliyunQueryString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, aliyunPercentEncode(k)+"="+aliyunPercentEncode(params[k]))
	}
	return strings.Join(pairs, "&")
}

// aliyunSignature 计算阿里云 RPC V1 签名:
// StringToSign = GET&percentEncode(/)&percentEncode(规范化查询串),
// Signature = Base64(HMAC-SHA1(StringToSign, AccessKeySecret+"&"))。
func aliyunSignature(params map[string]string, secret string) string {
	stringToSign := "GET&" + aliyunPercentEncode("/") + "&" + aliyunPercentEncode(aliyunQueryString(params))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// aliyunNonce 生成 16 字节随机 hex 作为 SignatureNonce。
func aliyunNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
