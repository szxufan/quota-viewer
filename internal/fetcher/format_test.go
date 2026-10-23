package fetcher

import "testing"

func TestAbbrFromName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"工作号", "工作"},         // 中文取前两字
		{"AB 站", "AB"},        // 去空白后取前两 rune
		{"A", "A"},            // 单字符原样
		{"  Gateway  ", "Ga"}, // 首尾空白去除
		{"", ""},              // 空名返回空(调用方回退渠道默认缩写)
		{"   ", ""},           // 纯空白同上
	}
	for _, c := range cases {
		if got := AbbrFromName(c.in); got != c.want {
			t.Errorf("AbbrFromName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
