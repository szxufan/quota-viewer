package fetcher

import (
	"strconv"
	"strings"
)

// AbbrFromName 从名称推导球格缩写:取前两个 rune(不足则全名),空白先去除。
// 用于凭证即站点渠道(CredAsSite)未配置缩写时的回退,如 "工作号" → "工作"。
func AbbrFromName(name string) string {
	r := []rune(strings.TrimSpace(name))
	if len(r) > 2 {
		r = r[:2]
	}
	return string(r)
}

// SettleResult 归一单条抓取结果的展示身份(app.go 编排层与 internal/probe 探针共用,
// 两处必须同口径,否则球面与 --json 输出对不上):
//   - ID/Abbr/Kind 仅在抓取器未自行设置时回填(多结果抓取器可为每条结果
//     指定独立分组与缩写,如资源包按类型成组、MiniMax 按套餐卡成组)
//   - KeyIndex/KeyName 记录凭证组归属;抓取器自带的 KeyName 作子结果名拼接
//   - 凭证即站点渠道(CredAsSite,如 New API 中转站):以凭证名/缩写代替
//     平台名/球格缩写并置 r.CredAsSite 标记,前端据此独立成组
func SettleResult(r *QuotaResult, def ProviderDef, keyIdx int, keyName, keyAbbr string) {
	if r.ID == "" {
		r.ID = def.ID
	}
	if r.Abbr == "" {
		r.Abbr = def.Abbr
	}
	r.KeyIndex = keyIdx
	if r.KeyName == "" {
		r.KeyName = keyName
	} else if keyName != "" {
		// 凭证组名 + 子结果名(多账号场景区分账号)
		r.KeyName = keyName + " · " + r.KeyName
	}
	if r.Kind == "" {
		r.Kind = KindUsage
	}
	if def.CredAsSite && keyName != "" {
		r.CredAsSite = true
		r.Platform = keyName
		if keyAbbr != "" {
			r.Abbr = keyAbbr
		} else if ab := AbbrFromName(keyName); ab != "" {
			r.Abbr = ab
		}
	}
}

// formatNum 以千分位分隔格式化数字,用于额度展示(如 8,335,815,690)。
func formatNum(v float64) string {
	n := v + 0.5 // 四舍五入到整数(与原 %.0f 展示语义一致)
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(int64(n), 10)
	out := make([]byte, 0, len(s)+len(s)/3+1)
	if neg {
		out = append(out, '-')
	}
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
