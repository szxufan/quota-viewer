# 增加阿里云渠道（QueryAccountBalance 余额查询）

## 摘要

在 quota-viewer 中新增阿里云渠道，通过 BssOpenApi `QueryAccountBalance` 接口查询账户余额。项目采用注册表驱动架构，只需三步：实现 Fetcher（含阿里云 RPC 签名）、注册到 registry、编写 httptest 测试。前端自动适配，无需改动 UI。

## 现状分析

- **架构**：注册表驱动。新增渠道 = `internal/fetcher/` 新增实现 + [registry.go](file:///c:/Users/xufan/Trae/quota-viewer/internal/fetcher/registry.go) 追加条目 + `_test.go` 测试。前端通过 `GetConfig()` 返回的注册表元数据动态渲染，**无需前端改动**。
- **余额型先例**：[deepseek.go](file:///c:/Users/xufan/Trae/quota-viewer/internal/fetcher/deepseek.go) 是余额型（`KindBalance`）参考实现——填 `Balance`/`Currency`/`Remaining`，`Percent` 恒 0，支持 `ApplyBudget` 预算计算。
- **依赖约束**：fetcher 层零外部依赖（全部 `net/http` + 标准库），go.mod 无阿里云 SDK。**手工实现签名**，不引入新依赖。
- **config.go 无需改动**：`AllProviderIDs`（仅 5 个，已落后于注册表的 7 个）只用于首次默认配置生成；glm/openrouter 不在其中也正常工作，因为 `app.go` 的 `GetConfig`/`SaveConfig` 全部基于 `fetcher.GetAll()` 动态工作。新渠道照此模式即可。
- **registry_test.go 需同步**：硬编码了 provider 数量（7）和顺序列表，需更新为 8 并追加 `"aliyun"`。

## 阿里云 API 关键事实（已从官方文档核实）

| 项 | 值 |
|----|-----|
| 端点 | `https://business.aliyuncs.com`（GET，参数走 query string） |
| Action | `QueryAccountBalance` |
| Version | `2017-12-14` |
| 认证 | AccessKey ID + AccessKey Secret，RPC 风格 HMAC-SHA1 签名 |
| 响应 | `Success`(bool) / `Code` / `Message` / `Data.AvailableAmount`(可用余额) / `Data.Currency`(CNY/USD/JPY) / `Data.AvailableCashAmount` / `Data.CreditAmount` |
| 权限 | RAM 用户需 `AliyunBSSReadOnlyAccess` 策略 |

### 签名算法（RPC V1）

1. 公共参数：`Format=JSON`、`Version=2017-12-14`、`AccessKeyId`、`SignatureMethod=HMAC-SHA1`、`SignatureVersion=1.0`、`SignatureNonce`（随机数）、`Timestamp`（UTC `2006-01-02T15:04:05Z`）、`Action=QueryAccountBalance`
2. 全部参数（除 Signature）按参数名 ASCII 升序排序，key/value 做 RFC3986 percentEncode（空格→`%20`、`*`→`%2A`、`%7E`→`~`），以 `=` 连接、`&` 拼接得 CanonicalizedQueryString
3. `StringToSign = "GET" + "&" + percentEncode("/") + "&" + percentEncode(CanonicalizedQueryString)`
4. `Signature = Base64(HMAC-SHA1(StringToSign, AccessKeySecret + "&"))`
5. Signature 作为 query 参数随 GET 请求发送

## 提议变更

### 1. 新增 `internal/fetcher/aliyun.go`

`AliyunFetcher` 结构体，字段：`accessKeyID`、`accessKeySecret`、`apiURL`（可空，默认 `https://business.aliyuncs.com`，测试注入）。构造函数 `NewAliyunFetcher(accessKeyID, accessKeySecret string)`。

`Fetch() QuotaResult` 流程（对齐 [deepseek.go](file:///c:/Users/xufan/Trae/quota-viewer/internal/fetcher/deepseek.go) 风格）：

1. 初始化 `QuotaResult{Platform: "阿里云", Kind: KindBalance, LastUpdated: time.Now()}`
2. 凭证空检查：任一为空 → `Error = "未配置阿里云 AccessKey"`
3. 构建签名参数 map → 排序 → percentEncode → StringToSign → HMAC-SHA1（key = secret + "&"）→ Base64 得 Signature
4. 拼接最终 URL（所有参数 + Signature 经 `url.Values` 编码），GET 请求，10s 超时
5. 状态码处理：
   - 非 200：尝试解析响应体的 `Message`/`Code`，有则用 `fmt.Sprintf("阿里云 API 错误: %s (%s)")`，否则 `HTTP %d`；403 特判为 "AccessKey 无效或无权限"
   - 200 但 `Success=false`：`Error = fmt.Sprintf("阿里云 API 错误: %s (%s)", Message, Code)`
6. 解析 `Data.AvailableAmount`（`strconv.ParseFloat`，先 `strings.TrimSpace`）→ `Balance`；`Data.Currency` → `Currency`
7. `Remaining = fmt.Sprintf("余额 %s%s (%s)", currencySymbol(currency), availableAmount, currency)`（复用 deepseek.go 已有的 `currencySymbol`，CNY→¥ / USD→$ / 其他→""；JPY 走默认空符号直接显示数值）

辅助函数（同文件内，小写不导出）：
- `aliyunPercentEncode(s string) string`：`url.QueryEscape` 后替换 `+`→`%20`、`*`→`%2A`、`%7E`→`~`
- `aliyunSignature(params map[string]string, secret string) string`：排序 + 拼接 + HMAC-SHA1 + Base64
- SignatureNonce 用 `crypto/rand` 生成 16 字节 hex

### 2. 修改 `internal/fetcher/registry.go`

在 `registry` 数组末尾（openrouter 之后）追加：

```go
{
    ID:          "aliyun",
    DisplayName: "阿里云",
    Abbr:        "AL",
    Kind:        KindBalance,
    LoginURL:    "https://usercenter2.aliyun.com/home",
    Fields: []CredentialField{
        {Key: "access_key_id", Label: "AccessKey ID", Type: "text"},
        {Key: "access_key_secret", Label: "AccessKey Secret", Type: "password"},
    },
    Build: func(creds map[string]string) Fetcher {
        return NewAliyunFetcher(creds["access_key_id"], creds["access_key_secret"])
    },
},
```

### 3. 新增 `internal/fetcher/aliyun_test.go`

全部 `httptest`，不发真实请求。测试用例：

1. **空凭证** → Error 非空，Kind=balance
2. **正常响应** → 验证请求 query 含 `AccessKeyId`、`Signature`、`Action=QueryAccountBalance` 参数；解析 `AvailableAmount=10000.00`/`Currency=CNY` → `Balance=10000`、`Currency=CNY`、Remaining 含 `¥` 和 `10000.00`、Percent=0
3. **签名正确性** → 用固定参数（固定 nonce/timestamp 注入或独立单测 `aliyunSignature`）验证签名值与手工计算的期望一致（可用阿里云官方文档示例数据做对照，或独立验证 HMAC 逻辑：给定输入输出确定）
4. **Success=false** → Error 含 Message
5. **HTTP 403** → Error 提示无效/无权限
6. **异常 JSON** → Error 非空

### 4. 修改 `internal/fetcher/registry_test.go`

- `TestGetAll_ContainsSevenProviders_InStableOrder`：数量 7→8，`want` 列表追加 `"aliyun"`（函数名一并改为 `ContainsEightProviders` 保持语义）

### 不需要改动的文件

- `app.go`、`frontend/src/*`、`internal/config/config.go` —— 全部通过注册表动态工作（与 glm/openrouter 新增时一致）

## 假设与决策

1. **手工实现签名而非引入阿里云 SDK**：项目 fetcher 层零外部依赖，SDK 会引入大量传递依赖；RPC V1 签名算法简单（~40 行），手工实现更轻量且风格一致。
2. **端点用国内站 `https://business.aliyuncs.com`**：官方文档标准端点；国际站用户如有需求后续可扩展，本次不做。
3. **余额取 `AvailableAmount`（可用额度 = 现金 + 信控 - 欠款）**：这是账户实际可用余额的最完整口径，与文档示例一致。
4. **Kind=balance**：余额型，球格恒绿，支持预算设置（复用 `ApplyBudget`）。
5. **Abbr="AL"**：遵循注册表 1-2 字符约定（参照 GL/OR）。
6. **AccessKey ID 用 text 类型**：ID 本身非密钥，参照 opencode-go 的 workspace_id 先例；Secret 用 password。
7. **不修改 config.go 的 AllProviderIDs**：该列表已落后（缺 glm/openrouter），实际加载逻辑不依赖它，保持一致的动态行为。

## 验证步骤

```powershell
# 1. 全部测试通过（含 registry 完整性检查）
go test ./...

# 2. 构建验证
wails build
```

检查清单：
- [ ] `internal/fetcher/aliyun.go` — Fetcher + 签名实现 + Fetch()
- [ ] `internal/fetcher/registry.go` — 追加 aliyun 条目
- [ ] `internal/fetcher/aliyun_test.go` — httptest 测试（空凭证/正常/签名/Success=false/403/异常 JSON）
- [ ] `internal/fetcher/registry_test.go` — 更新数量与顺序断言
- [ ] `go test ./...` 全绿
- [ ] `wails build` 成功
