# 余额型渠道自动更新预算（充值检测）

## Summary

为余额/预算类渠道（Kind=balance：DeepSeek、阿里云、MiMo 兜底）增加自动更新预算能力：每次成功抓取后，将当前余额与该凭证组的上次记录余额比较；余额增加视为用户充值，将该渠道的预算更新为新余额。OpenRouter 等平台直接返回充值总金额的渠道为用量型（Kind=usage），不参与此逻辑（通过 Kind 判断自然排除）。首次抓取仅记录基线，不改预算。

## Current State Analysis

- `internal/fetcher/budget.go`：`ApplyBudget(r, budget)` 为余额型结果计算 `Total=budget`、`Used=budget-Balance`（负值钳 0）、`Percent`、`Remaining` 文案。默认预算 300。
- `internal/fetcher/types.go`：`QuotaResult.Kind` 区分 `usage`/`balance`；`Balance` 字段存原始余额。
- `internal/config/config.go`：`ProviderConfig{ID, Enabled, Creds, Keys, Budget}`；`Keys` 为多组凭证，`Budget` 为每渠道单值。
- `app.go fetchAll()`：并发抓取后在 goroutine 内调用 `fetcher.ApplyBudget(&r, j.budget)`；已有抓取后写回配置的先例（`UpdatedCreds` → `persistCredUpdates`）。
- `app.go SaveConfig()`：保存时经 `mergeKeys` 重建凭证组，组数可能变化。
- 前端无需改动：设置页读取 `budget` 展示输入框，球面/面板消费后端计算的 `percent/total`。

## Proposed Changes

### 1. `internal/config/config.go` — 增加上次余额基线字段

`ProviderConfig` 新增：

```go
LastBalances []float64 `json:"last_balances,omitempty"` // 各凭证组上次抓取到的余额(充值检测基线)
```

按凭证组下标对齐（多 key 支持），不暴露给前端。

### 2. `internal/fetcher/budget.go` — 新增充值判定纯函数

```go
// DetectRecharge 判定本次抓取是否发生充值:仅对成功且无错误的余额型结果生效
// (用量型如 OpenRouter 由平台返回真实总额,不参与)。存在上次余额记录且
// 当前余额严格大于上次记录时视为充值。首次抓取(hasLast=false)只记基线不算充值。
func DetectRecharge(r QuotaResult, lastBalance float64, hasLast bool) bool {
	if r.Kind != KindBalance || r.Error != "" || r.Balance < 0 {
		return false
	}
	return hasLast && r.Balance > lastBalance
}
```

### 3. `app.go` — 抓取流程接入自动预算

- `fetchAll()`：goroutine 内删除 `fetcher.ApplyBudget(&r, j.budget)`；`job` 结构去掉 `budget` 字段。`wg.Wait()` 后调用新方法 `a.applyAutoBudget(results)` 完成检测+持久化+按最新预算重算展示值。

新增方法：

```go
// applyAutoBudget 余额型渠道自动更新预算:
// 成功的余额型结果与本凭证组上次余额比较,余额增加视为充值,将新余额写入渠道预算;
// 同时刷新各组余额基线并落盘;最后用最新配置预算重算消耗百分比。
func (a *App) applyAutoBudget(results []fetcher.QuotaResult) {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := map[string]int{}
	for i := range a.cfg.Providers {
		idx[a.cfg.Providers[i].ID] = i
	}

	newBudget := map[string]float64{} // 渠道 id → 充值后的新预算(多 key 取最大)
	changed := false
	for i := range results {
		r := &results[i]
		if r.Kind != fetcher.KindBalance || r.Error != "" {
			continue
		}
		pi, ok := idx[r.ID]
		if !ok {
			continue
		}
		pc := &a.cfg.Providers[pi]
		hasLast := r.KeyIndex < len(pc.LastBalances)
		var last float64
		if hasLast {
			last = pc.LastBalances[r.KeyIndex]
		}
		if fetcher.DetectRecharge(*r, last, hasLast) {
			if b, seen := newBudget[r.ID]; !seen || r.Balance > b {
				newBudget[r.ID] = r.Balance
			}
		}
		for len(pc.LastBalances) <= r.KeyIndex {
			pc.LastBalances = append(pc.LastBalances, 0)
		}
		if pc.LastBalances[r.KeyIndex] != r.Balance {
			pc.LastBalances[r.KeyIndex] = r.Balance
			changed = true
		}
	}
	for id, b := range newBudget {
		if pc := &a.cfg.Providers[idx[id]]; pc.Budget != b {
			pc.Budget = b
			changed = true
		}
	}
	if changed {
		_ = config.Save(a.cfg)
	}

	// 用最新预算重算展示值(含未发生变化的渠道,统一走原逻辑)
	for i := range results {
		b := 0.0
		if pi, ok := idx[results[i].ID]; ok {
			b = a.cfg.Providers[pi].Budget
		}
		fetcher.ApplyBudget(&results[i], b)
	}
}
```

- `SaveConfig()`：合并凭证组后加对齐保护——组数变化时旧基线失配会导致误判充值：

```go
pc.Keys = mergeKeys(pc.CredKeys(), in.Keys, in.Creds, def.Fields)
pc.Creds = nil
// 凭证组数变化后旧余额基线不再对齐,重置待下次抓取重建
if len(pc.Keys) != len(pc.LastBalances) {
	pc.LastBalances = nil
}
```

### 4. 测试 — 新建 `internal/fetcher/budget_test.go`

覆盖 `DetectRecharge`：
- 首次抓取（hasLast=false）：即使余额 > 0 也不算充值
- 余额增加 → true；持平/减少 → false
- 错误结果 / Kind=usage / 负余额 → false
- 保留现有 `deepseek_test.go` 中 ApplyBudget 测试不动

## Assumptions & Decisions

1. **"比之前增加" = 与上次成功抓取的余额比较**（而非与当前预算比较）：可捕获未超过旧预算的部分充值（如 100→150），符合"将新的余额作为预算"的字面语义。需要持久化基线，故新增 `LastBalances` 字段。
2. **排除规则靠 Kind 实现**：OpenRouter 等平台返回真实总额的渠道是 `KindUsage`；MiMo 有套餐数据时也走 usage 路径，均被 `DetectRecharge` 的 Kind 检查排除。
3. **首次抓取只记基线不改预算**：避免老用户升级后被默认预算意外覆盖。
4. **多 key 同渠道多组同时充值取最大余额**作为该渠道新预算；预算保持每渠道单值，与现有结构一致。
5. **基线每次成功抓取都刷新**（含正常消耗导致的减少），保证下次比较基准正确；由此带来的配置落盘频率与现有 MiMo Cookie 回写一致（每次刷新至多一次），可接受。
6. 不做 app 级集成测试：`config.Save` 会写真实 `%APPDATA%` 目录，仓库现状也无 app 测试；纯逻辑已由 `DetectRecharge` 单测覆盖。

## Verification

1. `go build ./...` 与 `go vet ./...` 通过。
2. `go test ./internal/...` 全部通过（含新增 `budget_test.go`）。
3. 手工场景推演（依据单测）：
   - 预算 300、上次余额 100 → 本次 150：触发充值，`Budget=150`，展示 `Used=0, Percent=0`，绿球。
   - 上次余额 100 → 本次 90（消耗）：不改预算，仅刷新基线。
   - 首次运行：仅记基线，不动用户手设预算。
