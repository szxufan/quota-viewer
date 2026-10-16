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
