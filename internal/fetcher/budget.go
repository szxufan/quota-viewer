package fetcher

import "fmt"

// defaultBudget 是余额型 Provider 的默认预算(用户未设时使用)。
const defaultBudget = 300

// ApplyBudget 为余额型 Provider 应用预算,计算消耗百分比。
// 用户未设预算(budget <= 0)时使用 defaultBudget;计算逻辑:
// 已消耗 = 预算 - 当前余额;百分比 = 已消耗 / 预算 * 100。
// 余额超过预算时钳制已消耗为 0(用户充值超预算的情况)。
func ApplyBudget(r *QuotaResult, budget float64) {
	if r.Kind != KindBalance || r.Balance < 0 || r.Error != "" {
		return
	}
	if budget <= 0 {
		budget = defaultBudget
	}
	r.Total = budget
	r.Used = budget - r.Balance
	if r.Used < 0 {
		r.Used = 0
	}
	r.Percent = r.Used / budget * 100
	sym := currencySymbol(r.Currency)
	r.Remaining = fmt.Sprintf("%s%.2f / %s%.2f (预算)", sym, r.Balance, sym, budget)
}

// DetectRecharge 判定本次抓取是否发生充值:仅对成功且无错误的余额型结果生效
// (用量型如 OpenRouter 由平台返回真实总额,不参与)。存在上次余额记录且
// 当前余额严格大于上次记录时视为充值。首次抓取(hasLast=false)只记基线不算充值。
func DetectRecharge(r QuotaResult, lastBalance float64, hasLast bool) bool {
	if r.Kind != KindBalance || r.Error != "" || r.Balance < 0 {
		return false
	}
	return hasLast && r.Balance > lastBalance
}
