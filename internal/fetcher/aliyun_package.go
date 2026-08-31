package fetcher

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 云资源包用量查询(见 .trae/documents/阿里云资源包用量查询API开发参考.md):
// 余量用旧版 QueryResourcePackageInstances(2017-12-14)接口,一次拉取全部有效实例
// (该接口无 CommodityCode 过滤参数),再按 CommodityCode 客户端过滤聚合。
// 已用量 = TotalAmount - RemainingAmount(接口无"已用量"字段)。

// aliyunPackageResultID 是资源包用量结果的分组 ID(详情面板独立于账户余额成组展示)。
const aliyunPackageResultID = "aliyun-package"

// aliyunPackagePageSize 是实例查询的单页条数(接口上限 300)。
const aliyunPackagePageSize = 300

// aliyunPackageType 描述一种可监控的云资源包类型(设置界面选项 → 实例过滤条件)。
// Unit 为固定展示单位:接口对部分流量包会误报单位为 Byte(实际 GB),
// 因此不信任响应单位,按类型取固定单位(参考文档 §6.3)。
type aliyunPackageType struct {
	Value         string // 配置中存储的值(逗号拼接的一项)
	Label         string // 详情页展示的凭证组名
	CommodityCode string // 实例 CommodityCode 过滤条件
	Unit          string // 展示单位
	Abbr          string // 球格缩写
}

// aliyunPackageTypes 是全部支持的资源包类型,顺序 = 详情展示顺序。
var aliyunPackageTypes = []aliyunPackageType{
	{Value: "ots", Label: "OTS 资源包", CommodityCode: "otsbag", Unit: "CU", Abbr: "OTS"},
	{Value: "flowbag", Label: "共享流量包", CommodityCode: "flowbag", Unit: "GB", Abbr: "流"},
	{Value: "cdt", Label: "CDT 流量包", CommodityCode: "cdt_Resource_dp_cn", Unit: "GB", Abbr: "CDT"},
}

// ParseAliyunPackageTypes 解析配置中逗号拼接的资源包类型值(如 "ots,cdt"):
// 去空白、去重、过滤未知值,并按 aliyunPackageTypes 的固定顺序返回。
func ParseAliyunPackageTypes(s string) []string {
	parts := strings.Split(s, ",")
	selected := make(map[string]bool, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			selected[v] = true
		}
	}
	if len(selected) == 0 {
		return nil
	}
	out := make([]string, 0, len(selected))
	for _, t := range aliyunPackageTypes {
		if selected[t.Value] {
			out = append(out, t.Value)
		}
	}
	return out
}

// aliyunPkgInstance 是 QueryResourcePackageInstances 返回的单个资源包实例(仅取用到的字段)。
// TotalAmount/RemainingAmount 为数字字符串(如 "100000000")。
type aliyunPkgInstance struct {
	InstanceId      string `json:"InstanceId"`
	TotalAmount     string `json:"TotalAmount"`
	RemainingAmount string `json:"RemainingAmount"`
	CommodityCode   string `json:"CommodityCode"`
	DeductType      string `json:"DeductType"` // Absolute / DeadlineAcc / PeriodMonthlyAcc / ...
}

// aliyunPkgResponse 是 QueryResourcePackageInstances 的响应(2017-12-14 版,含信封)。
type aliyunPkgResponse struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
	Success bool   `json:"Success"`
	Data    struct {
		TotalCount int `json:"TotalCount"`
		Instances  struct {
			Instance []aliyunPkgInstance `json:"Instance"`
		} `json:"Instances"`
	} `json:"Data"`
}

// FetchMulti 实现 MultiFetcher:返回 账户余额结果 + 每个选中资源包类型一条用量结果。
// 未选类型时等价于 Fetch(仅余额);凭证缺失时只返回余额的错误结果,不重复报错。
func (f *AliyunFetcher) FetchMulti() []QuotaResult {
	results := []QuotaResult{f.Fetch()}
	if len(f.packageTypes) == 0 || f.accessKeyID == "" || f.accessKeySecret == "" {
		return results
	}

	instances, err := f.fetchPackageInstances()
	if err != nil {
		results = append(results, QuotaResult{
			Platform:    "阿里云资源包",
			ID:          aliyunPackageResultID,
			Kind:        KindUsage,
			Error:       err.Error(),
			LastUpdated: time.Now(),
		})
		return results
	}

	for _, t := range aliyunPackageTypes {
		if !containsString(f.packageTypes, t.Value) {
			continue
		}
		results = append(results, buildAliyunPackageResult(t, instances))
	}
	return results
}

// fetchPackageInstances 分页拉取全部有效资源包实例(每页 300,循环至取完)。
func (f *AliyunFetcher) fetchPackageInstances() ([]aliyunPkgInstance, error) {
	var all []aliyunPkgInstance
	for pageNum := 1; ; pageNum++ {
		var body aliyunPkgResponse
		err := f.callBssAPI("QueryResourcePackageInstances", "2017-12-14", map[string]string{
			"PageNum":  strconv.Itoa(pageNum),
			"PageSize": strconv.Itoa(aliyunPackagePageSize),
		}, &body)
		if err != nil {
			return nil, err
		}
		if !body.Success {
			if body.Message != "" {
				return nil, fmt.Errorf("阿里云 API 错误: %s (%s)", body.Message, body.Code)
			}
			return nil, fmt.Errorf("阿里云 API 返回失败 (%s)", body.Code)
		}
		batch := body.Data.Instances.Instance
		all = append(all, batch...)
		// 末页判定:本页不满即结束;有 TotalCount 时取够即结束
		if len(batch) < aliyunPackagePageSize {
			break
		}
		if body.Data.TotalCount > 0 && len(all) >= body.Data.TotalCount {
			break
		}
		if pageNum >= 100 { // 防御:异常分页不至于死循环
			break
		}
	}
	return all, nil
}

// buildAliyunPackageResult 把某类型的全部实例聚合为一条用量结果:
// 总量/剩余求和,已用 = 总量 - 剩余;无实例时返回提示文案。
// 已用完(剩余量为 0)的实例不计入聚合,避免失效包稀释进度条;
// 但若该类型下全部实例都已用完,则仍然计入以展示用尽状态。
// 自然月周期型包(PeriodMonthlyAcc)按月重置额度,ResetAt 取下月 1 日。
func buildAliyunPackageResult(t aliyunPackageType, instances []aliyunPkgInstance) QuotaResult {
	r := QuotaResult{
		Platform:    "阿里云资源包",
		ID:          aliyunPackageResultID,
		Abbr:        t.Abbr,
		Kind:        KindUsage,
		KeyName:     t.Label,
		LastUpdated: time.Now(),
	}

	var total, remaining float64
	var count int
	var exhaustedTotal float64
	var exhaustedCount int
	monthly := false
	for _, ins := range instances {
		if ins.CommodityCode != t.CommodityCode {
			continue
		}
		tv, err1 := strconv.ParseFloat(ins.TotalAmount, 64)
		rv, err2 := strconv.ParseFloat(ins.RemainingAmount, 64)
		if err1 != nil || err2 != nil {
			continue // 异常数值实例跳过,不影响其余聚合
		}
		if rv <= 0 {
			// 已用完的实例先单独记账,仅当没有剩余实例时才计入
			exhaustedTotal += tv
			exhaustedCount++
			continue
		}
		total += tv
		remaining += rv
		count++
		if ins.DeductType == "PeriodMonthlyAcc" {
			monthly = true
		}
	}

	if count == 0 && exhaustedCount > 0 {
		// 全部已用完:仍然计入,展示用尽状态(进度条 100%)
		total = exhaustedTotal
		count = exhaustedCount
	}
	if count == 0 {
		r.Remaining = "无生效中资源包"
		return r
	}

	used := total - remaining
	if used < 0 {
		used = 0
	}
	r.Used = used
	r.Total = total
	if total > 0 {
		r.Percent = used / total * 100
	}
	r.Remaining = fmt.Sprintf("%s/%s %s·%d包",
		formatAliyunAmount(remaining), formatAliyunAmount(total), t.Unit, count)
	if monthly {
		r.ResetAt = nextMonthStart(time.Now()).Format(time.RFC3339)
	}
	return r
}

// formatAliyunAmount 按中文数量单位格式化大额用量(亿=1e8、万=1e4),
// 与阿里云控制台显示习惯一致;小于 1 万时用千分位整数。
func formatAliyunAmount(v float64) string {
	if v >= 1e8 {
		return trimFloat1(v/1e8) + "亿"
	}
	if v >= 1e4 {
		return trimFloat1(v/1e4) + "万"
	}
	return formatNum(v)
}

// trimFloat1 保留 1 位小数并去掉多余的 ".0"(如 1.0 → "1")。
func trimFloat1(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// nextMonthStart 返回 now 所在月份的下一个自然月 1 日 00:00(本地时区)。
// time.Date 自动处理 12 月 → 次年 1 月的进位。
func nextMonthStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
}

// containsString 判断切片是否包含字符串。
func containsString(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}
