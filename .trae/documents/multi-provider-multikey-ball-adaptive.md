# 支持超过 3 个渠道、多 key、ball 自适应计划

## Summary

解除系统里"最多 3 个启用渠道"的硬限制，支持：

1. 任意数量渠道（panel 按内容高度自适应加高）；
2. 每个渠道多组凭证（多 key），panel 中同一渠道的多个 key 横向分割显示；
3. 悬浮球从"单行 3 格"改为网格自适应：1-3 个单行 60×60，4 个 2×2 60×60，5 个及以上按 `cols = ceil(sqrt(n))` 扩展边长（每格 ≥22px），球窗口尺寸由前端计算后通知 Go 侧调整。

用户已确认的设计决策：

* 多 key 输入：**凭证组模式**（每个渠道卡片内可"添加凭证/删除凭证"，每组是一套完整字段输入）；

* ball 布局：**网格**，超过 4 个渠道后按 3×3（及更多行）扩展。

## Current State Analysis

现状（全部限制在 3 个渠道以内，单凭证）：

| 位置            | 现状                            | 文件                                                                                                                                                  |
| ------------- | ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| 前端勾选          | 最多 3 个、最少 1 个                 | [main.js](file:///c:/Users/xufan/Trae/quota-viewer/frontend/src/main.js#L286-L299)                                                                  |
| 后端 SaveConfig | 0 个 → 全启用；>3 → 保留前 3          | [app.go](file:///c:/Users/xufan/Trae/quota-viewer/app.go#L169-L197)                                                                                 |
| 后端 fetchAll   | 只取前 3 个 enabled               | [app.go](file:///c:/Users/xufan/Trae/quota-viewer/app.go#L398-L438)                                                                                 |
| 旧配置迁移         | 钳制最多 3 个启用                    | [config.go](file:///c:/Users/xufan/Trae/quota-viewer/internal/config/config.go#L168-L177)                                                           |
| 凭证模型          | `Creds map[string]string`（单组） | [config.go](file:///c:/Users/xufan/Trae/quota-viewer/internal/config/config.go#L18-L23)                                                             |
| panel 尺寸      | 固定 340×310                    | [main.js](file:///c:/Users/xufan/Trae/quota-viewer/frontend/src/main.js#L5-L9)                                                                      |
| ball 尺寸       | 固定 60×60，单行 flex              | [app.go](file:///c:/Users/xufan/Trae/quota-viewer/app.go#L294)、[style.css](file:///c:/Users/xufan/Trae/quota-viewer/frontend/src/style.css#L50-L86) |
| 窗口最小尺寸        | 常量 ballSize=60，subclass 回调硬编码 | [workarea\_windows.go](file:///c:/Users/xufan/Trae/quota-viewer/workarea_windows.go#L61-L76)                                                        |
| 结果模型          | 一个平台一条记录，无 key 序号             | [types.go](file:///c:/Users/xufan/Trae/quota-viewer/internal/fetcher/types.go#L13-L28)                                                              |

## Proposed Changes

### 一、后端

#### 1. `internal/config/config.go`

* `ProviderConfig` 增加多凭证组字段：

  ```go
  Keys []map[string]string `json:"keys,omitempty"` // 多组凭证(每组一套字段值);空 = 未配置多 key
  ```

  `Creds` 保留（兼容旧配置读取）。

* 新增方法（统一取凭证组，兼容旧配置）：

  ```go
  // CredKeys 返回凭证组列表:优先 Keys;否则旧 Creds 视为单组;均空返回 nil
  func (p ProviderConfig) CredKeys() []map[string]string
  ```

* `migrateFromLegacy`：**删除**"钳制最多 3 个启用"的代码块（[config.go L168-177](file:///c:/Users/xufan/Trae/quota-viewer/internal/config/config.go#L168-L177)）。迁移逻辑可顺带把有值的 `Creds` 写入 `Keys`（非必须，CredKeys 已兼容；为保持文件整洁，迁移时 `Keys = [creds]` 并清空 `Creds` 更统一——采用此做法）。

* `Default()` 不变（默认启用前三个，用户可自行增加）。

#### 2. `internal/fetcher/types.go`

* `QuotaResult` 增加字段：

  ```go
  KeyIndex int `json:"key_index"` // 该结果属于渠道的第几个凭证组(0 起);单凭证恒为 0
  ```

#### 3. `app.go`

* `ProviderInput` 增加 `Keys []map[string]string json:"keys"`；`Creds` 保留（兼容旧前端/旧行为）。

* `GetConfig`：每个 provider 返回 `keys` 数组（每组的每个字段值经 `maskSecret` 掩码）；`creds` 仍返回（= keys\[0] 的掩码，或旧值，供兼容）。

* `SaveConfig`：

  * **删除** ">3 保留前 3" 钳制；保留"0 个启用 → 全部启用"。

  * 凭证合并（多 key 语义）：`in.Keys` 非空时——

    * 与旧 `pc.Keys` 长度相同 → 按索引逐字段合并（空串 = 保留旧值，与现有空串语义一致）；

    * 长度不同 → 全量替换（空字段即清空）；

    * 全空组跳过不保存。

  * `in.Keys` 为空（旧前端）→ 退化为现有 `Creds` 合并逻辑，并把结果写入 `Keys[0]`、清空 `Creds`（升级为新模型）。

  * 保存后统一 `pc.Creds = nil`（单一数据源 Keys）。

* `fetchAll`：

  * 遍历**所有** enabled provider（去掉 3 个上限）；

  * 每个 provider 用 `p.CredKeys()` 得到凭证组列表，每组并发 `def.Build(creds).Fetch()`；

  * 结果扁平数组，顺序 = 注册表顺序 × 组顺序；每组结果设置 `r.KeyIndex = i`；

  * 单组时 `KeyIndex = 0`，前端行为与现状一致。

* ball 动态尺寸：

  * `const ballSize = 60` 改为 `var ballSize = 60`（包级变量，\[workarea\_windows.go 的 subclass 回调需要读取]）；

  * `App` 结构体增加 `ballSize int`（初始 60，避免与包级变量重复时直接用包级变量即可——采用包级变量方案，`CollapseWindow`/`fitToScreen` 中的 `ballSize` 引用保持不变，运行时由 `SetBallSize` 更新）。

  * 新增方法：

    ```go
    // SetBallSize 前端渲染完 ball 网格后调用:更新最小/当前窗口尺寸并校正位置
    func (a *App) SetBallSize(size int)
    ```

    实现：`ballSize = size`；`WindowSetMinSize(ctx, size, size)`；`WindowSetSize(ctx, size, size)`；用 `fitToScreen` 重新钳制位置（`fitToScreen` 内部 `ballPhys := ballSize * dpi / 96` 已用包级变量，自动生效）。

  * `CollapseWindow` 无需改签名（内部引用包级 `ballSize`）。

#### 4. `internal/config/config_test.go`

* `TestLoad_MigrateLegacyJSON`：mimo 断言从"被钳制为 disabled"改为"保持 enabled"（因为 4 个 enabled 不再钳制）；迁移后 `Keys` 应包含原 `Creds`。

* `TestSaveThenLoad_RoundTrip`：增加多组 `Keys` 的 provider 用例。

* 新增 `TestCredKeys_Compat`：Keys 空 + Creds 有值 → 返回 \[Creds]；Keys 有值 → 返回 Keys。

### 二、前端

#### 5. `frontend/src/main.js`

* **panel 动态高度**：

  * 删除固定 `SIZES.panel = [340, 310]` 用法；`setView("panel")` 时先用缓存 `currentResults` 渲染（若已加载），再调用 `resizePanel()`。

  * 新增 `resizePanel()`：读取 `document.getElementById("panel").offsetHeight`，`ExpandWindow(340, max(310, h))`。

  * `renderResults` 末尾：若 `currentView === "panel"` 调用 `resizePanel()`（quota:update 事件到达时也生效）。

* **panel 多 key 横向分割**：

  * `renderResults` 按 `r.id` 分组；每个 provider 渲染一个 `quota-item`，其内每个 key 渲染一个 `quota-key-cell` 横向排列（`display:flex`）。

  * 单元格内容：序号标签（"Key 1"、"Key 2"，单 key 不显示）+ 剩余额度 + 进度条 + 倒计时（沿用现有 `getStatusColor`/`formatCountdown`）。

  * 单 key 时单元格占满整行（视觉与现状一致）。

* **ball 网格自适应**：

  * `updateBall`：格子改为网格排布；规则：

    * n ≤ 3：单行，边长 60（现状）；

    * n = 4：2×2，边长 60；

    * n ≥ 5：`cols = ceil(sqrt(n))`（5-9 → 3×3），边长 `= max(60, cols*22)`。

  * 渲染后调用 `window.go.main.App.SetBallSize(size)`。

  * 格子 CSS：JS 设置 ball 的 `--cols` 变量，样式见第 6 节。

  * tooltip（title）保持：每个 key 一行 `平台 Key n: 剩余额度`。

* **设置面板凭证组模式**：

  * `providerCards` 结构改为 `[{id, enabled, groups: [{fields: [{key, input}]}], budget}]`。

  * `renderProviderList`：每组凭证渲染一套字段输入（复用现有 `f.type` 渲染逻辑）+ 组尾"删除"按钮（组数 >1 时显示）；卡片底部"添加凭证"按钮（点击追加一组空字段）。

  * `collectProviders`：`{id, enabled, keys: groups.map(g => 字段值对象), budget}`（keys 每组为完整字段对象）。

  * 勾选逻辑：**删除"最多 3 个"限制**，保留"至少 1 个"。

  * 测试连接按钮：保存后测试（现有逻辑），后端测试用第一组凭证。

  * `SIZES.settings` 高度保持不变（内容区已可滚动）。

* `setView("ball")`：收起时 `CollapseWindow` 不变（Go 侧已用动态 ballSize）。

#### 6. `frontend/src/style.css`

* `.ball` 增加网格模式：

  ```css
  .ball.grid { flex-wrap: wrap; }
  .ball.grid .ball-cell { width: calc(100% / var(--cols, 3)); flex: none; height: calc(100% / var(--rows, 3)); }
  ```

  （JS 设置 `--cols`/`--rows`；cell 高度用 flex 拉伸 + aspect 保持。）

* `quota-key-cell` 样式：横向分割（`flex:1`、`border-left` 分隔线）、序号标签、进度条复用现有类。

* 其余现有样式（provider-card 等）复用，新增 `.provider-group` 分隔（凭证组间细分隔线）与"删除/添加凭证"按钮样式（复用 `.btn-sm`）。

#### 7. `frontend/src/index.html`

* 无需结构性修改（格子/凭证组均由 JS 动态生成）。

### 三、文档一致性（低成本）

#### 8. `docs/wiki/` 中"上限 3"表述同步更新（避免后续 agent 误判规则）

* [00-agent-rules.md L70](file:///c:/Users/xufan/Trae/quota-viewer/docs/wiki/00-agent-rules.md#L70)：改为"下限 1、无上限，凭证可多组"。

* [01-architecture-overview.md L11](file:///c:/Users/xufan/Trae/quota-viewer/docs/wiki/01-architecture-overview.md#L11)、[03-core-pipeline.md L22/L32/L49](file:///c:/Users/xufan/Trae/quota-viewer/docs/wiki/03-core-pipeline.md#L22-L49)、[07-config-model.md](file:///c:/Users/xufan/Trae/quota-viewer/docs/wiki/07-config-model.md#L58-L103)、[08-api-contract.md L20/L117](file:///c:/Users/xufan/Trae/quota-viewer/docs/wiki/08-api-contract.md#L20)：同步为"全部启用渠道、多凭证组（keys）"。

* [09-testing-strategies.md L22](file:///c:/Users/xufan/Trae/quota-viewer/docs/wiki/09-testing-strategies.md#L22)：更新测试说明。

* [04-window-positioning.md](file:///c:/Users/xufan/Trae/quota-viewer/docs/wiki/04-window-positioning.md)：补充动态 ball 尺寸（SetBallSize）说明。

## Assumptions & Decisions

1. **凭证组模式**（用户已确认）：每组 = 一套完整字段；单字段渠道（Kimi/DeepSeek）的多个 key 即多组 `api_key`；多字段渠道（OpenCode Go）即多组 `workspace_id+session_token`。
2. **ball 网格规则**（用户已确认）：1-3 单行 60×60；4 个 2×2；≥5 按 `ceil(sqrt(n))` 方形扩展，边长 = `max(60, cols*22)`（9 个 = 3×3=66，10-16 = 4×4=88）。
3. **panel 高度**：由 DOM 实际 `offsetHeight` 决定（header + 列表 + footer），最小 310，宽度保持 340。
4. **多 key 保存语义**：`in.Keys` 与旧 `Keys` 长度相同时按索引合并（空字段保留旧值）；长度不同时全量替换。前端删除凭证组 → 组数减少 → 触发全量替换，行为正确。
5. **兼容性**：旧配置（只有 `creds`）读取后经 `CredKeys()` 视为单组；用户保存一次即升级为 `keys` 模型。旧前端提交（无 keys）走 `Creds` 退化路径。
6. **并发**：所有渠道 × 凭证组并发 Fetch（每 key 一个 goroutine），数量级小，无需额外限流。
7. 勾选限制从"1-3"改为"≥1"；后端同样不再钳制上限。

## Verification

1. `go build ./...` 通过。
2. `go test ./...` 通过（config 包测试更新后）。
3. 前端语法检查：`npx vite build`（frontend 目录）通过。
4. 手动验证（`wails dev`）：

   * 设置面板勾选 5 个渠道无报错、可保存；

   * 给 Kimi 添加 2 组 api\_key，panel 中 Kimi 区块内横向显示 2 个 key 的额度与进度条；

   * 启用 5 个渠道后 panel 高度自动加高、完整显示不截断；

   * 收起为球：5 个渠道显示 3×3 网格中的 5 格（2×3 布局，边长 66），4 个渠道为 2×2 60×60，1-3 个渠道与现状一致；

   * 球窗口最小尺寸随球尺寸变化（拖拽不撑宽）；

   * 球展开/收起位置校正正常。

