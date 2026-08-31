package fetcher

import "time"

// 结果类型常量。
const (
	// KindUsage 用量型(默认):Percent = 已用百分比。
	KindUsage = "usage"
	// KindBalance 余额型(如 DeepSeek):Percent 无意义,Remaining 展示余额。
	KindBalance = "balance"
)

// QuotaResult 是所有 fetcher 的统一返回结构。
type QuotaResult struct {
	Platform    string    `json:"platform"`  // 展示名("Kimi" / "讯飞星辰" / ...)
	ID          string    `json:"id"`        // provider id(注册表,前端对齐球格)
	Abbr        string    `json:"abbr"`      // 球格缩写("K" / "讯" / "Go" / ...)
	Kind        string    `json:"kind"`      // "usage"(默认) / "balance"
	KeyIndex    int       `json:"key_index"` // 该结果属于渠道的第几个凭证组(0 起);单凭证恒为 0
	KeyName     string    `json:"key_name"`  // 凭证组显示名(配置中设置;空 = 前端回退 "Key N")
	Used        float64   `json:"used"`      // 已用量
	Total       float64   `json:"total"`     // 总量(平台返回则填,否则 0)
	Percent     float64   `json:"percent"`   // Used/Total * 100;无总量时由剩余百分比反推
	Balance     float64   `json:"balance"`   // 余额型 Provider 的原始余额数值(Kind=balance 时有效)
	Currency    string    `json:"currency"`  // 余额货币代码(如 "CNY" / "USD")
	Remaining   string    `json:"remaining"` // 原始剩余描述(如 "1,200/18,000 次" 或 "无限制")
	ResetAt     string    `json:"reset_at"`  // 下次重置时间(ISO 8601,空则未知)
	LastUpdated time.Time `json:"last_updated"`
	Error       string    `json:"error"` // 非空表示失败

	// UpdatedCreds 本次抓取产生的、应写回配置的新凭证(字段 key → 新值);空 = 无更新。
	// 仅 Go 内部使用(如 MiMo Cookie 自动换取),不暴露给前端。
	UpdatedCreds map[string]string `json:"-"`
}

// Fetcher 是额度查询器的统一接口。
type Fetcher interface {
	Fetch() QuotaResult
}

// MultiFetcher 是可选接口:一次抓取可返回多条结果的 fetcher 实现它
// (如阿里云:账户余额 + 按所选资源包类型各一条用量结果)。
// 未实现该接口的 fetcher 由 BuildAndFetch 包装为单条结果。
type MultiFetcher interface {
	FetchMulti() []QuotaResult
}

// BuildAndFetch 用 Provider 定义构建 fetcher 并抓取,统一返回结果切片。
// 实现 MultiFetcher 的 fetcher 返回其全部结果;否则包装为单元素切片。
func BuildAndFetch(def ProviderDef, creds map[string]string) []QuotaResult {
	f := def.Build(creds)
	if mf, ok := f.(MultiFetcher); ok {
		return mf.FetchMulti()
	}
	return []QuotaResult{f.Fetch()}
}
