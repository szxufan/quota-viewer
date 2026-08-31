# 阿里云渠道增加云资源包用量查询与展示

## 摘要

在现有阿里云渠道（余额型）基础上，新增**云资源包用量**查询与展示：设置界面为阿里云凭证组提供资源包类型多选项（OTS 资源包 / VPC 共享流量包 / CDT 流量包），选中后详情界面按类型展示聚合用量（剩余/总量、进度条、月周期包的重置倒计时）。API 依据 `.trae/documents/阿里云资源包用量查询API开发参考.md` 的实测结论：余量用旧版 `QueryResourcePackageInstances`（2017-12-14）。

## 现状分析（实现前）

- 渠道为注册表驱动：`ProviderDef.Fields` 仅支持 `password`/`text`/`textarea`，**无选项型字段先例**。
- `Fetcher` 接口一次抓取只返回**一条** `QuotaResult`；`app.go fetchAll` 按 `结果数 = 渠道数 × 凭证组数` 定长分配。
- 阿里云渠道已手写 RPC V1 HMAC-SHA1 签名（`aliyun.go` 的 `aliyunPercentEncode`/`aliyunQueryString`/`aliyunSignature`/`aliyunNonce`），可直接复用。
- `GetConfig` 对全部凭证值做 `maskSecret` 掩码；前端输入框以 placeholder 显示掩码、提交时由 `mergeKeys` 还原。

## 关键设计决策

1. **扩展现有阿里云渠道**（而非新增渠道）：资源包与余额共用同一组 AK/SK，避免重复录入；符合"给阿里云渠道增加"的需求。
2. **新增可选接口 `MultiFetcher`**（`types.go`）：一次抓取返回多条结果。`AliyunFetcher.FetchMulti()` 返回 `余额结果 + 每选中类型一条用量结果`；未实现该接口的渠道由 `BuildAndFetch` 包装为单条，现有 8 个渠道零改动。
3. **资源包结果独立分组**：结果自设 `ID="aliyun-package"`、`Platform="阿里云资源包"`，详情面板与"阿里云"余额组上下分开成组展示（避免余额+多类型挤在同一行横向扩展过宽）。`fetchAll` 对 `ID/Abbr/KeyName` 改为**仅在抓取器未设置时回填**。
4. **选项型字段机制**：`CredentialField` 增加 `Options []FieldOption`、`Multiple`、`Plain`；`Type="select"` 前端渲染为复选框组，选中值逗号拼接存入凭证（如 `"ots,cdt"`）。`Plain=true` 表示非敏感字段：`GetConfig` 不掩码、前端以真实值初始化勾选状态（避免掩码占位导致"取消勾选被误还原"）。
5. **按类型聚合展示**（而非逐实例一行）：同类型多个实例（实测账号最多 9 个 CDT 包）求和为一条结果，适配现有"单行进度条"展示模型。**已用完（剩余量为 0）的实例不计入聚合**，避免失效包稀释进度条；但若该类型下全部实例都已用完，则仍然计入以展示用尽状态（进度条 100%）。
6. **客户端过滤**：`QueryResourcePackageInstances` 无 `CommodityCode` 过滤参数，一次分页拉全量实例（`PageSize=300` 循环翻页），按 `CommodityCode` 过滤：`otsbag` / `flowbag` / `cdt_Resource_dp_cn`。
7. **单位按类型固定**（不信任响应单位）：参考文档 §6.3，部分流量包 `TotalAmountUnit` 误报 `Byte`（实际 GB）。OTS→CU，流量包/CDT→GB。

## 变更清单

### Go 后端

| 文件 | 变更 |
|---|---|
| `internal/fetcher/types.go` | 新增 `MultiFetcher` 接口与 `BuildAndFetch(def, creds) []QuotaResult` 分发函数 |
| `internal/fetcher/registry.go` | `CredentialField` 增加 `Options/Multiple/Plain` 字段与 `FieldOption` 类型；阿里云条目追加 `package_types` select 字段（3 个选项），`Build` 中解析并注入 `f.packageTypes` |
| `internal/fetcher/aliyun.go` | 抽出公共请求方法 `callBssAPI(action, version, extra, out)`（签名/HTTP/解码，错误文案与原先一致）；`Fetch()` 改为调用它；结构体增加 `packageTypes` 字段 |
| `internal/fetcher/aliyun_package.go` | 新增：类型表 `aliyunPackageTypes`、`ParseAliyunPackageTypes`、`FetchMulti`、分页拉取 `fetchPackageInstances`、按类型聚合 `buildAliyunPackageResult`、中文单位格式化 `formatAliyunAmount`（亿/万）、`nextMonthStart`（自然月周期包重置时间） |
| `app.go` | `fetchAll` 改用 `BuildAndFetch` 并按 job 顺序展平多结果；`ID/Abbr/KeyName` 空才回填，凭证组名与子结果名以 ` · ` 组合；`GetConfig` 字段元数据透传 `options/multiple/plain`，plain 字段不掩码 |

### 前端

| 文件 | 变更 |
|---|---|
| `frontend/src/settings-helpers.js` | 新增纯函数 `parseOptionValues(value, options)`（去空白/去重/过滤未知值、按选项顺序返回） |
| `frontend/src/main.js` | `renderCredGroup` 增加 `select` 分支：复选框组 + 隐藏 input 承载逗号值（`collectProviders`/空组过滤/保存清空逻辑零改动复用） |
| `frontend/src/style.css` | `.field-options` / `.field-option` 复选框组样式（覆盖 `.form-group input` 的全宽/内边距） |

### 测试

| 文件 | 覆盖 |
|---|---|
| `internal/fetcher/aliyun_package_test.go` | 类型值解析；未选类型仅余额；凭证缺失不重复报错；多类型聚合（含忽略无关商品码、月周期包 `ResetAt`、空类型提示）；分页翻页（300+1 条）；业务错误透传；`formatAliyunAmount`/`nextMonthStart`（含跨年）；`BuildAndFetch` 单/多分发；注册表选项完整性 |
| `frontend/test/settings-helpers.test.mjs` | `parseOptionValues` 空值/解析/清洗/顺序归一 |

## 结果字段映射（每选中类型一条）

| QuotaResult 字段 | 取值 |
|---|---|
| `Platform` | `阿里云资源包` |
| `ID` | `aliyun-package`（详情面板独立成组） |
| `Abbr` | `OTS` / `流` / `CDT`（球格缩写） |
| `Kind` | `usage`（进度条 = 已用/总量） |
| `KeyName` | `OTS 资源包` / `共享流量包` / `CDT 流量包`（多账号时前缀凭证组名） |
| `Used/Total/Percent` | ΣTotalAmount − ΣRemainingAmount 推导 |
| `Remaining` | 形如 `3944.8万/1.5亿 CU·2包`；无实例时 `无生效中资源包` |
| `ResetAt` | 含 `PeriodMonthlyAcc` 包时 = 下月 1 日（本地时区，RFC3339）；总量递减型为空 |

## 已知边界

- 明细流水（`DescribeDeductLogs`）未接入：余量与用量用实例接口已满足，逐笔明细非本次需求。
- 旧版接口只返回有效包（`Available`），已失效历史包不在统计内（如需可后续接 `DescribeFrInstances`）。
- RAM 权限：建议 `AliyunBSSReadOnlyAccess`（`QueryResourcePackageInstances` 对应 `bss:DescribeInstances`）。

## 验证

```
go build ./... ; go vet ./... ; go test ./...
cd frontend ; npm test ; npm run build
```

手动验证：`wails dev` 运行 → 设置 → 阿里云 → 勾选资源包类型 → 保存 → 详情面板出现"阿里云资源包"分组（每选中类型一行，含进度条与剩余文案）；取消全部勾选保存后仅剩余额行。
