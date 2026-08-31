# 阿里云资源包用量查询 API 开发参考

> 适用范围：查询 **表格存储 OTS 资源包**、**VPC 共享流量包（通用流量包）**、**CDT（云数据传输）流量包** 的实例余量与用量明细。
> 结论均已通过真实账号 + AccessKey 实测验证（2026-08），测试脚本见同目录 `query.js`。

---

## 1. 结论速览（TL;DR）

**只依赖费用中心 BssOpenApi 一套接口即可覆盖全部需求，无需调用 OTS / VPC / CDT 各产品的专用 API。**

| 需求 | 推荐接口 | 版本 | 关键过滤参数 |
|---|---|---|---|
| 资源包实例列表 + 余量 | `QueryResourcePackageInstances` | 2017-12-14 | `ProductCode` |
| 资源包实例列表 + 余量（含历史包） | `DescribeFrInstances` | 2023-09-30 | `ProductCode` / `CommodityCode` / `Status` |
| 抵扣/用量明细（流水） | `DescribeDeductLogs` | 2023-09-30 | `CommodityCode` + 毫秒时间戳区间 |

- 两个版本 API 的 endpoint 相同：`https://business.aliyuncs.com`，均为 RPC 风格。
- **余量查询用旧版**（新版对 OTS 自然月周期型包不返回剩余量，见 §6）。
- **明细查询用新版 `DescribeDeductLogs`**（旧版 `QueryDPUtilizationDetail` 实测返回 `NotAuthorized`，且官方文档注明仅覆盖 RI/SCU）。
- 明细接口返回的是**抵扣流水**（约每 5 分钟～1 小时一批），如需"日用量"需自行按天聚合。

---

## 2. 调用基础

### 2.1 协议与签名

- Endpoint：`business.aliyuncs.com`，HTTPS GET，RPC 风格。
- 签名方式一（旧式 RPC，本文示例采用）：`SignatureMethod=HMAC-SHA1`，把所有参数排序拼接后做 HMAC-SHA1，签名放在 query string。
- 签名方式二（ACS3-HMAC-SHA256，Header 签名）：官方 SDK 默认使用，效果等价。
- 生产环境建议直接用官方 SDK（OpenAPI 门户 `api.aliyun.com` 搜索 `BssOpenApi` 可生成各语言代码）或 `aliyun` CLI，不必手写签名。

**公共参数（HMAC-SHA1 方式）：**

| 参数 | 说明 |
|---|---|
| `Action` / `Version` / `Format=JSON` | 接口名 / 版本号（2017-12-14 或 2023-09-30）/ 返回格式 |
| `AccessKeyId` | AK |
| `SignatureMethod=HMAC-SHA1`、`SignatureVersion=1.0` | 固定值 |
| `SignatureNonce` | 随机串（UUID），防重放 |
| `Timestamp` | **UTC** 时间，格式 `yyyy-MM-ddTHH:mm:ssZ`，与服务端偏差过大会报错 |

签名算法：参数按 key 的 ASCII 升序拼接为 `k1=v1&k2=v2&...`（值做 RFC3986 百分号编码）→
`stringToSign = "GET&%2F&" + urlencode(查询串)` → `signature = base64(HMAC-SHA1(AccessKeySecret + "&", stringToSign))`。

### 2.2 RAM 权限

实测可用权限（最小化只读）：

| 接口 | 授权 Action |
|---|---|
| `QueryResourcePackageInstances` | `bss:DescribeInstances` |
| `DescribeFrInstances` | `bss:DescribeFrInstance` |
| `DescribeDeductLogs` | 本次测试 AK 实测可调用（建议直接授予系统策略 `AliyunBSSReadOnlyAccess`） |
| `QueryDPUtilizationDetail`（旧版明细） | `bss:FrDeductLogQueryRequest`（实测该 AK 无此权限，返回 `NotAuthorized: Auth user relation failed`） |

> 简化建议：给查询类业务直接挂 `AliyunBSSReadOnlyAccess`，一次覆盖上表全部接口。

---

## 3. 三类资源包的产品码速查表

`ProductCode`（产品码，用于按产品过滤实例）与 `CommodityCode`（商品码，实测取值）：

| 资源类型 | ProductCode | CommodityCode（实测） | 包类型模板 PackageType（实测） | 说明 |
|---|---|---|---|---|
| OTS 资源包 | `ots` | `otsbag` | `FPT_otsbag_periodMonthlyAcc_OutOfReservationReadCU_china_common` | 高性能读套餐(全球通用)，自然月周期型，按月重置额度 |
| VPC 共享流量包 | `flowbag` | `flowbag` | `FPT_generalnetwork_deadlineAcc_asia_idle` / `_asia` | 亚太通用（闲时/全时），总量递减型，抵扣按流量计费的 ECS/EIP/SLB/共享带宽(cbwp)/IPv6 带宽/CDT 公网流量等 |
| CDT 流量包 | —（旧版接口按 `CommodityCode` 识别） | `cdt_Resource_dp_cn` | `cdt_Resource_dp_cn_20250529211731_05440` | CDT资源包(公网)_BGP(多线)，2025-08 上线的新产品，总量递减型，仅抵扣 `cdt_DataTransfer_public_cn` / `cdt_internet_public_cn` |

> 完整官方产品码对照表：[codes-of-resource-plans](https://help.aliyun.com/zh/user-center/developer-reference/codes-of-resource-plans)（注意该表未收录 CDT 资源包，CDT 取值以上面实测为准）。

---

## 4. 接口详解

### 4.1 QueryResourcePackageInstances（余量查询，推荐）

查询用户资源包实例列表。**只返回有效（Available）的资源包**，不含已失效历史包。

```
GET https://business.aliyuncs.com/?Action=QueryResourcePackageInstances&Version=2017-12-14&...
```

**请求参数（均可选）：**

| 参数 | 类型 | 说明 |
|---|---|---|
| `ProductCode` | string | 产品码过滤，如 `ots`、`flowbag` |
| `ExpiryTimeStart` / `ExpiryTimeEnd` | string | 按失效时间过滤，UTC ISO8601：`yyyy-MM-ddTHH:mm:ssZ` |
| `PageNum` / `PageSize` | int | 分页，默认 1 / 20，**PageSize 最大 300** |
| `IncludePartner` | boolean | 是否包含合作伙伴 |

**返回关键字段（`Data.Instances.Instance[]`）：**

| 字段 | 说明 | 实测示例 |
|---|---|---|
| `InstanceId` | 资源包实例 ID | `PK-cn-0gl4xpcbv01`（OTS）/ `flowpack-cn-xxxx`（流量包）/ `cdt_Resource_dp_cn-xxxx` |
| `TotalAmount` / `TotalAmountUnit` | 总量 + 单位 | `100000000` + `CU`；`1000` + `GB` |
| `RemainingAmount` / `RemainingAmountUnit` | 剩余量 + 单位 | `39448283` + `CU` |
| `Status` | `Available` / `Expired` | `Available` |
| `PackageType` / `CommodityCode` / `Remark` | 包类型模板 / 商品码 / 商品名称 | 见 §3 |
| `DeductType` | 扣费类型 | `Absolute`（总量恒定）、`DeadlineAcc`（总量递减）、`PeriodMonthlyAcc`（自然月周期） |
| `Region` | 适用地域 | `china-common` / `chinese-mainland` / `*` |
| `EffectiveTime` / `ExpiryTime` | 生效/失效时间，UTC ISO8601 | `2026-08-29T05:00:00Z` |
| `ApplicableProducts.Product[]` | 可抵扣的产品码列表 | 流量包：`ecs/eip/slb/cbwp/cdt_DataTransfer_public_cn/...` |

> **没有"已用量"字段**：`已用量 = TotalAmount − RemainingAmount`，需自行计算。

**实测示例（OTS 读 CU 包，某自然月周期包当月剩余约 39.4%）：**

```json
{
  "Status": "Available",
  "InstanceId": "PK-cn-0gl4xpcbv01",
  "TotalAmount": "100000000", "TotalAmountUnit": "CU",
  "RemainingAmount": "39448283", "RemainingAmountUnit": "CU",
  "PackageType": "FPT_otsbag_periodMonthlyAcc_OutOfReservationReadCU_china_common",
  "DeductType": "PeriodMonthlyAcc",
  "CommodityCode": "otsbag", "Region": "china-common",
  "Remark": "高性能读套餐(全球通用)",
  "EffectiveTime": "2026-08-29T05:00:00Z", "ExpiryTime": "2026-09-29T05:00:00Z",
  "ApplicableProducts": { "Product": ["ots"] }
}
```

**实测账号数据规模**：有效资源包 23 个（OTS 读CU包 ×3、共享流量包 ×6、CDT 流量包 ×9、OSS/PolarDB/NAS/FC 各若干），单次 `PageSize=300` 即可全部拉回。

### 4.2 DescribeFrInstances（新版实例查询，信息更全）

2023-09-30 版费用 API。**包含历史包**，状态覆盖 `valid / invalid / exhaust`，实测返回 102 条（旧版仅 23 条有效包）。

```
GET https://business.aliyuncs.com/?Action=DescribeFrInstances&Version=2023-09-30&...
```

**主要请求参数（均可选）：**

| 参数 | 说明 |
|---|---|
| `Group` | 资源维度：`fr`（普通资源包）、`cu`（SCU）、`ecsRi`（预留实例券）、`oss_rc`/`oss_arc`（OSS 预留空间）、`polardb` |
| `ProductCode` / `CommodityCode` / `TemplateCode` | 按产品 / 商品 / 模板过滤 |
| `Status` | `valid` / `invalid` / `exhaust` |
| `CapacityType` | `absolute`（总量恒定）、`deadlineAcc`（总量递减）、`periodMonthlyAcc`（自然月周期）、`periodMonthlyShift`（动态月周期） |
| `StartTime` / `EndTime` | 生效时间范围，**毫秒时间戳** |
| `PageNum` / `PageSize`、`SortField`（startTime/endTime）、`SortRule` | 分页排序 |

**返回关键字段（`Data[]`，注意响应无 `Success/Code` 包裹层）：**

| 字段 | 说明 |
|---|---|
| `InstanceId` | 实例 ID |
| `Product` / `Commodity` / `Template` | `{Code, Name}` 结构，产品/商品/模板（中文名） |
| `InitCapacityViewValue` / `InitCapacityViewUnit` | 初始总量（显示值），如 `1.000000亿CU`、`1000.000000GB` |
| `CurrCapacityViewValue` / `CurrCapacityViewUnit` | 当前剩余量 |
| `StatusCode` | `valid` / `invalid` / `exhaust` |
| `CapacityType` / `CycleType` | `{Code, Name}`，容量类型/承诺周期 |
| `StartTime` / `EndTime` / `PurchaseTime` | **毫秒时间戳** |
| `DeductRegions` | 可抵扣地域列表 |
| `EnableRenew` / `EnableUpgrade` / `EnableExchange` | 可否续费/升级/换购 |

> ⚠️ **已知坑**：OTS 自然月周期型包（`periodMonthlyAcc`）的 `CurrCapacityViewValue` 返回 `"-"`（无剩余量数值），流量包/CDT 包正常。**余量请以旧版 4.1 接口为准**，新版适合查历史包和展示模板中文名。

### 4.3 DescribeDeductLogs（抵扣/用量明细，推荐）

查询资源包对按量实例的逐笔抵扣记录。**实测是查询三类资源包用量的唯一可用明细接口。**

```
GET https://business.aliyuncs.com/?Action=DescribeDeductLogs&Version=2023-09-30&...
```

**请求参数：**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `BillingStartTime` / `BillingEndTime` | long | **是** | 计费时间窗，**毫秒时间戳** |
| `CommodityCode` | string | 否 | **资源包商品码**，实测支持 `otsbag` / `flowbag` / `cdt_Resource_dp_cn` |
| `BillingCommodityCode` | string | 否 | 被抵扣商品码，如 `ots`、`cdt_DataTransfer_public_cn` |
| `InstanceId` | string | 否 | 资源包实例 ID |
| `BillInstanceId` | string | 否 | 被抵扣的实例 ID |
| `Group` | string | 否 | 资源维度，同 4.2 |
| `PageNum` / `PageSize` | int | 否 | 分页（实测 7 天流水 5860 条，必须循环翻页） |

**返回关键字段（`Data[]`，按 `DeductTime` 倒序，外层含 `TotalCount/CurrentPage/PageSize`）：**

| 字段 | 说明 | 实测示例 |
|---|---|---|
| `InstanceId` | 资源包实例 ID | `PK-cn-0gl4xpcbv01` |
| `Commodity` | 资源包商品 `{Code,Name}` | `otsbag` / `flowbag` / `cdt_Resource_dp_cn` |
| `DeductTime` | 抵扣时间，毫秒时间戳 | `1788072509000` |
| `CapacityBeforeDeductViewValue/Unit` | 抵扣前余量 | `4.181841千万CU` |
| **`CapacityDeductedViewValue/Unit`** | **本次抵扣量** | `2.537500十万CU` / `190.531536MB` |
| `CapacityAfterDeductViewValue/Unit` | 抵扣后余量 | `4.156466千万CU` |
| `BillingCommodity` | 被抵扣商品 `{Code,Name}` | `ots` / `cdt_DataTransfer_public_cn` |
| `BillingInstanceId` | 被抵扣实例标识（分号分隔，见下） | `ssd;ljp-03;cn-hangzhou` |
| `MeasureDeductedViewValue/Unit` | 原始计量值 | 与抵扣量通常一致 |
| `BillingPriceField` | 被抵扣计费项 `{Code,Name}` | `OutOfReservationReadCU` / `internet_traffic` |
| `AccountId` / `RelationAccountId` | 账号 / 被抵扣账号（多账号合并账单场景） | — |

**三类资源包的 `BillingInstanceId`（被抵扣实例）格式：**

| 类型 | 格式 | 实测示例 |
|---|---|---|
| OTS | `存储规格;实例ID[;地域]`，多元索引为 `实例ID#search_index` | `ssd;ljp-03;cn-hangzhou`、`ssd;ljp-03#search_index;cn-hangzhou` |
| 流量包 / CDT | `实例ID;地域;线路;产品类型` | `eip-bp1xxx;cn-hangzhou;BGP;eip`、`lb-bp1xxx;cn-hangzhou;BGP;slb`、`i-xxx;cn-shanghai;BGP;ecs`、`fc-public;cn-hangzhou;BGP;fc`、`ipv6-xxx;cn-wulanchabu;BGP;ipv6gateway` |

**抵扣粒度（实测）**：OTS 约每小时一批；流量包/CDT 约每 5 分钟～1 小时一批。7 天流水条数参考：`otsbag` 485 条、`flowbag` 1509 条、`cdt_Resource_dp_cn` 2548 条。

**日用量聚合口径建议**：

```
某资源包当日用量 = Σ CapacityDeductedViewValue（按 DeductTime 的自然日、单位归一后求和）
某实例当日用量   = Σ CapacityDeductedViewValue（按 BillingInstanceId 分组）
最新余量        = 按 DeductTime 倒序第一条的 CapacityAfterDeductViewValue（可与 4.1 的 RemainingAmount 交叉校验）
```

> ⚠️ 注意：中文数量单位（`千万CU`、`十万CU`、`万CU`、`亿CU`）需解析换算（千万=1e7、亿=1e8）；`MB/GB` 按 1024 进制处理需与显示口径核对（接口显示值本身即换算后的数值+单位，求和前先统一换算成 Byte/CU 基础单位）。

### 4.4 QueryDPUtilizationDetail（旧版明细，不推荐用于本场景）

`BssOpenApi 2017-12-14`。官方文档注明仅覆盖 **RI（预留实例）与 SCU（存储容量单位包）**；实测该账号 AK 调用返回：

```json
{ "Code": "NotAuthorized", "Message": "Auth user relation failed, please check RAM permission or reseller permission." }
```

主要参数：`StartTime`/`EndTime`（`yyyy-MM-dd HH:mm:ss`，必填）、`IncludeShare`（必填 boolean）、`Limit`（≤300）、`LastToken`（游标分页）。**查询用量请改用 4.3 `DescribeDeductLogs`。**

---

## 5. 示例代码（Node.js，零依赖，可直接运行）

```js
// aliyun_bss_test/query.js — 用法: ALIYUN_AK_ID=xxx ALIYUN_AK_SECRET=xxx node query.js
const https = require('https');
const crypto = require('crypto');

const ENDPOINT = 'business.aliyuncs.com';

function enc(str) { // RFC3986 百分号编码
  return encodeURIComponent(String(str)).replace(/[!'()*]/g,
    (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase());
}

function callApi(version, action, params = {}) {
  return new Promise((resolve, reject) => {
    const all = {
      Action: action, Format: 'JSON', Version: version,
      AccessKeyId: process.env.ALIYUN_AK_ID,
      SignatureMethod: 'HMAC-SHA1', SignatureVersion: '1.0',
      SignatureNonce: crypto.randomUUID(),
      Timestamp: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'),
      ...params,
    };
    const qs = Object.keys(all).sort().map((k) => `${enc(k)}=${enc(all[k])}`).join('&');
    const stringToSign = `GET&${enc('/')}&${enc(qs)}`;
    const signature = crypto.createHmac('sha1', process.env.ALIYUN_AK_SECRET + '&')
      .update(stringToSign).digest('base64');

    https.get(`https://${ENDPOINT}/?${qs}&Signature=${enc(signature)}`, (res) => {
      let body = '';
      res.on('data', (c) => (body += c));
      res.on('end', () => resolve(JSON.parse(body)));
    }).on('error', reject);
  });
}

(async () => {
  // 1) OTS 资源包余量
  const ots = await callApi('2017-12-14', 'QueryResourcePackageInstances',
    { PageNum: 1, PageSize: 300, ProductCode: 'ots' });
  console.log(ots.Data.Instances.Instance.map((i) =>
    `${i.InstanceId} 剩余 ${i.RemainingAmount}${i.RemainingAmountUnit} / ${i.TotalAmount}${i.TotalAmountUnit}`));

  // 2) 共享流量包余量
  const flow = await callApi('2017-12-14', 'QueryResourcePackageInstances',
    { PageNum: 1, PageSize: 300, ProductCode: 'flowbag' });

  // 3) CDT 流量包抵扣明细（近 7 天，注意翻页）
  const cdtLogs = await callApi('2023-09-30', 'DescribeDeductLogs', {
    BillingStartTime: Date.now() - 7 * 86400e3,
    BillingEndTime: Date.now(),
    CommodityCode: 'cdt_Resource_dp_cn',
    PageNum: 1, PageSize: 300,
  });
  // cdtLogs.Data[] / cdtLogs.TotalCount
})();
```

> 2023-09-30 版接口的响应**没有** `Success/Code` 包裹层（直接是 `RequestId/TotalCount/Data` 等）；2017-12-14 版有 `Success` 与 `Code=Success`。做统一封装时需兼容两种信封。

---

## 6. 已知坑与注意事项汇总

1. **余量以旧版接口为准**：新版 `DescribeFrInstances` 对 OTS 自然月周期型包返回剩余量 `"-"`；旧版 `QueryResourcePackageInstances` 始终有数值。
2. **旧版接口只返回有效包**：查历史/已用尽的包需用新版 `DescribeFrInstances`（`Status=invalid/exhaust`）。
3. **单位显示不一致**：旧版接口部分流量包 `TotalAmountUnit` 会误报为 `Byte`（实际是 GB）；新版已用尽的包 `CurrCapacityViewUnit` 显示 `Byte`。建议解析时以 `PackageType`/`InitCapacity` 为准做兜底。
4. **没有"已用量"字段**：一律 `TotalAmount − RemainingAmount` 计算或从明细聚合。
5. **明细是流水不是聚合值**：日/小时用量需自行按时间桶求和；OTS 是小时级、流量类约 5 分钟～小时级一批。
6. **时间格式两套**：2017 版用 UTC ISO8601 字符串；2023 版用毫秒时间戳。
7. **中文数量单位**：明细接口的显示值带 `万/十万/千万/亿` 前缀，求和前需换算。
8. **分页上限**：旧版实例接口 `PageSize ≤ 300`；明细接口数据量大（本账号 7 天约 6000 条），务必循环翻页。
9. **CDT 有专用 API（`Cdt/2021-08-13` 服务，官方 SDK `cdt20210813`）**，但本次未实测；BSS 侧已能取到 CDT 包余量+明细，无强需求不必接入。
10. **安全**：AK/SK 建议通过环境变量/KMS 注入，禁止入库入仓；本仓库脚本不落盘凭证。对话中明文暴露过的 AK 用完请及时轮换。

---

## 7. 官方文档链接

- [QueryResourcePackageInstances](https://help.aliyun.com/zh/user-center/developer-reference/api-bssopenapi-2017-12-14-queryresourcepackageinstances)（2017-12-14）
- [DescribeFrInstances](https://help.aliyun.com/zh/user-center/developer-reference/api-bssopenapi-2023-09-30-describefrinstances)（2023-09-30）
- [DescribeDeductLogs](https://help.aliyun.com/zh/user-center/developer-reference/api-bssopenapi-2023-09-30-describedeductlogs)（2023-09-30）
- [QueryDPUtilizationDetail](https://help.aliyun.com/zh/user-center/developer-reference/api-bssopenapi-2017-12-14-querydputilizationdetail)（2017-12-14，RI/SCU）
- [资源包产品码对照表](https://help.aliyun.com/zh/user-center/developer-reference/codes-of-resource-plans)
- [BSS OpenAPI 资源和成本管理指南](https://help.aliyun.com/zh/user-center/developer-reference/call-api-operations-to-manage-resources-and-costs)
- [共享流量包计费与用量查询](https://help.aliyun.com/zh/dtp/product-overview/product-billing) ｜ [什么是共享流量包](https://help.aliyun.com/zh/dtp/product-overview/what-is-a-data-transfer-plan)
- [OTS 资源包类型与抵扣规则](https://help.aliyun.com/zh/tablestore/product-overview/resource-plan-overview/) ｜ [OTS 资源包选购指南](https://help.aliyun.com/zh/tablestore/product-overview/resource-plan-purchase-guide)
- [CDT 文档首页](https://help.aliyun.com/zh/cdt/) ｜ [CDT 资源包发布公告](https://help.aliyun.com/zh/cdt/product-overview/cdt-resource-plan-announcement)
- [OpenAPI 门户（在线调试）](https://api.aliyun.com/api/BssOpenApi)
