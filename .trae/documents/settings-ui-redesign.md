# 设置界面改版（左右两栏 + 分 Tab + 固定底栏）

## 摘要

设置面板从"全部 Provider 卡片平铺、窗口随内容长高"改为：固定 720×620 窗口、"账号/通用"分 Tab、账号页左右两栏（左栏导航选择 Provider、右栏编辑凭证，多凭证横向标签切换）、保存按钮固定底栏。目标：首屏信息密度合理、无需长滚动，保存按钮始终可达。

> 迭代记录：v1 曾采用 360×520 + 手风琴卡片，因宽度不足、手风琴来回展开效率低，调整为当前的加宽两栏方案。

## 现状分析（改版前）

* 设置面板（`frontend/src/index.html` 的 `#settings`）一次性平铺全部 8 个 Provider 卡片，每张卡片含勾选框、名称、测试/打开登录页按钮、N 组凭证表单（显示名 + 1~3 个凭证字段 + 预算 + 删除按钮）与"添加凭证"按钮，内容总高度轻松超过 1500px。

* `main.js` 的 `resizeSettings()` 让窗口随内容长高（最小 340×480），超出屏幕工作区后靠 `settings-body` 内部滚动；保存按钮位于滚动内容末尾，需拉到底才能点击。

* 刷新间隔、界面透明度与账号配置混排在同一滚动区内，无层次。

## 变更方案

### 1. `frontend/src/index.html` — 布局重构

* `#settings` 内部改为四段式：头部（标题 + 关闭）→ 标签栏（账号/通用）→ 内容区 → 底栏（保存按钮）。

* 账号页 `#tab-accounts` 内为 `.accounts-layout` 两栏容器：`#provider-nav`（左栏导航）+ `#provider-detail`（右栏详情），均由 JS 动态生成。

* "刷新间隔"与"界面透明度"位于 `#tab-general` 页（限宽 440px，避免控件被拉宽）。

* 保存按钮 `#btn-save-config` 移出内容区，放入 `.settings-footer`。

### 2. `frontend/src/main.js` — 交互重构

* **固定窗口尺寸**：`SIZES.settings = [720, 620]`，删除 `resizeSettings()` 及其全部调用；`.settings` 用 flex 纵向布局，内容超出时两栏各自内部滚动；窗口被工作区钳制变矮时自动适应。

* **左右两栏（账号页）**：
  - 左栏导航项 = 勾选框 + 名称 + 状态徽标（`n 个凭证` / `未配置`）；点击选中，选中项高亮。
  - 右栏详情 = 标题行（名称 + 徽标）+ 凭证标签条 + 凭证表单页 + 测试/打开登录页按钮；仅显示选中 Provider 的面板。
  - `selectProvider(id)` 切换选中；重渲染（保存后重载）尽量保持上次选中项（`selectedProviderId`），无则选第一个。
  - 勾选框在左栏直接切换启用/停用（最少保留 1 个的限制不变），停用项降低不透明度。

* **多凭证横向标签**：详情区顶部标签条（`凭证 1` / `凭证 2` / `+`），点击切换凭证页；标签文案优先取显示名并实时同步；`+` 追加新凭证并切换到该页；删除按钮在各凭证页内（组数 >1 时显示），删除后激活相邻页。

* **状态徽标**：左栏与右栏标题行各一份，由 `updateProviderBadge()` 同步更新；口径与 `collectProviders()` 的"非空组"过滤一致（任一字段有输入值或掩码 placeholder）。

* **分 Tab**：`.settings-tab` 点击切换 `.settings-tab-pane` 的 `active` 类，默认"账号"页。

* **不变的部分**：`collectProviders()` 数据收集逻辑、保存/测试流程（测试仍先保存再测）、透明度实时预览逻辑。切换选中/分 Tab 仅显隐 DOM，不销毁输入状态。

### 3. `frontend/src/settings-helpers.js` — 纯函数模块

抽取无 DOM 依赖的展示逻辑，便于单测：

* `credTabLabel(name, index)`：凭证标签文案（显示名优先，回退 `凭证 n`）。
* `groupHasData(fields)`：凭证组是否非空（`{value, placeholder}` 任一非空）。
* `providerBadgeText(groups)`：Provider 徽标文案（`未配置` / `n 个凭证`）。

### 4. `frontend/src/style.css` — 样式

* `.settings` 宽 720、`height: 100vh`、flex 纵向；`.settings-body` 为 flex 容器（`overflow: hidden`），由各页/各栏自行滚动。
* 账号页：`#tab-accounts.active { display: flex }`；`.provider-nav` 固定宽 200、`.provider-detail` 占剩余宽度并以左边线分隔，两栏独立滚动（细滚动条样式）。
* 导航项状态：`.active`（accent 描边 + 浅 accent 底）、`.disabled`（降透明度）。
* 凭证标签 `.cred-tabs` / `.cred-tab(.active/.add)`、凭证页 `.cred-page(.active)`。
* 底栏 `.settings-footer`：保存按钮固定，不随内容滚动。

### 5. `frontend/test/settings-helpers.test.mjs` — 前端单测（零依赖）

* 使用 Node 内置 `node:test` + `node:assert/strict`（Node ≥18），不引入测试框架依赖。
* `package.json`：`"test": "node --test \"test/*.test.mjs\""`（Windows 下目录参数不可用，需用 glob）；`"type": "module"` 使 Node 以 ESM 解析 `src/*.js`（Vite 3 兼容，构建已验证）。
* 覆盖：标签文案回退规则、徽标文案、非空组判定（空组 / 有值 / 仅掩码 placeholder）。

### 6. 文档同步

* `docs/wiki/02-module-catalog.md`：前端条目补充 `settings-helpers.js` 与测试文件。
* `docs/wiki/09-testing-strategies.md`：前端由"无测试"更新为"纯函数模块 node:test 单测 + 构建 + 手动冒烟"。

## 假设与决策

* **两栏而非手风琴**：窗口加宽后，左栏总览 + 右栏编辑可避免反复展开/收起，跨 Provider 切换只需点一下；启用状态在左栏一目了然。

* **折叠/切换只显隐、不销毁 DOM**：`collectProviders()` 依赖遍历全部输入框收集状态，显隐方案零改动、无数据丢失风险。

* **固定 720×620**：宽度为原 360 的两倍，容纳两栏；高度较原 520 适当加高。小屏上 Go 侧 `ExpandWindow` 会按工作区钳制，flex 布局自动适应（两栏各自滚动）。

* **保存后重载保持选中项**：`selectedProviderId` 在重渲染后复用，减少操作中断感。

* **不引入 vitest/jsdom**：项目前端原本无测试框架，可测逻辑为少量纯函数，`node --test` 零依赖即可覆盖；DOM 级交互仍按项目惯例手工冒烟。

## 验证步骤

1. `cd frontend && npm test` 全部通过。
2. `cd frontend && npm run build` 无报错。
3. `go build ./...`、`go test ./...` 通过（无 Go 改动，回归确认）。
4. `wails build` 端到端通过。
5. 手动冒烟：
   - 打开设置：窗口 720×620，默认"账号"页，左栏列出全部 Provider（勾选框 + 名称 + 徽标），右栏显示选中项详情。
   - 左栏切换选中、勾选/取消勾选（最少 1 个限制）、停用项样式。
   - 凭证标签切换、`+` 添加、删除后相邻页激活、显示名实时同步标签文案。
   - 内容超出一屏时右栏内部滚动，保存按钮始终可见可点；保存/测试/透明度预览流程不变。
   - "通用"页刷新间隔与透明度正常、控件宽度受限不拉伸。
