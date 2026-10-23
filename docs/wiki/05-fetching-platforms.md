# 平台抓取器与注册表

**When to read**: 修改或新增某个平台的额度抓取逻辑时。

---

## 核心内容

### 统一接口

```go
const (
    KindUsage   = "usage"   // 用量型(默认):Percent = 已用百分比
    KindBalance = "balance" // 余额型(DeepSeek):Percent 无意义,Remaining 展示余额
)

type QuotaResult struct {            // internal/fetcher/types.go
    Platform    string    `json:"platform"`    // 展示名("Kimi" / "讯飞星辰" / ...)
    ID          string    `json:"id"`          // provider id(注册表)
    Abbr        string    `json:"abbr"`        // 球格缩写
    Kind        string    `json:"kind"`        // "usage"(默认) / "balance"
    KeyIndex    int       `json:"key_index"`   // 该结果属于渠道的第几个凭证组(0 起)
    KeyName     string    `json:"key_name"`    // 凭证组显示名/多结果的子结果名
    CredAsSite  bool      `json:"cred_as_site,omitempty"` // 凭证即站点渠道标记(见下)
    Used        float64   `json:"used"`
    Total       float64   `json:"total"`       // 平台返回则填，否则 0
    Percent     float64   `json:"percent"`     // Used/Total*100; 无总量时由剩余百分比反推
    Remaining   string    `json:"remaining"`   // 原始剩余描述（如 "1,200/18,000 次"）
    ResetAt     string    `json:"reset_at"`    // 下次重置时间 ISO8601，空则未知
    // 多周期渠道(Kimi/GLM/百炼/OpenCode Go/Ollama/MiniMax 等)各周期刷新时间;
    // 空 = 单周期渠道,前端详情页仅按 ResetAt 展示一行倒计时
    PeriodResets []PeriodReset `json:"period_resets,omitempty"` // [{label, reset_at}]
    LastUpdated time.Time `json:"last_updated"`
    Error       string    `json:"error"`       // 非空表示失败
}

type Fetcher interface {
    Fetch() QuotaResult
}
type MultiFetcher interface {          // 可选:一次抓取返回多条结果
    FetchMulti() []QuotaResult         // (阿里云: 余额+各资源包; MiniMax: 各套餐卡)
}
func BuildAndFetch(def ProviderDef, creds map[string]string) []QuotaResult
```

### Provider 注册表（registry.go）

```go
type CredentialField struct {
    Key   string `json:"key"`   // 凭证 key
    Label string `json:"label"` // 显示名
    Type  string `json:"type"`  // "password" | "text" | "textarea" | "select"
}

type ProviderDef struct {
    ...
    // Credentialless 免凭证 Provider(凭证存于外部工具,如百炼 CLI 全局登录态):
    // 启用即可用,fetchAll 为其创建空凭证组任务,前端徽标显示"免凭证"
    Credentialless bool
    // CredAsSite 凭证即站点渠道(如 New API 中转站):每个凭证组是一个独立站点,
    // 有凭证名时以凭证名/缩写代替平台名/球格缩写展示,详情页按凭证逐条成组
    CredAsSite bool
    ...
}

func GetAll() []ProviderDef            // 固定顺序 = 推荐展示顺序
func Get(id string) (ProviderDef, bool)
```

### 展示身份归一（SettleResult,format.go）

`fetcher.SettleResult(r, def, keyIdx, keyName, keyAbbr)` 是抓取结果的统一身份归一,
app.go fetchAll 与 internal/probe 探针(--json)共用同一口径:

- ID/Abbr/Kind 仅在抓取器未自行设置时回填(多结果抓取器可为每条结果指定独立分组与缩写)
- KeyIndex/KeyName 记录凭证组归属;抓取器自带的 KeyName 作子结果名拼接("凭证名 · 子结果名")
- CredAsSite 渠道:平台名/缩写被凭证名/缩写顶替并置 `r.CredAsSite`,前端据此独立成组
  (不再回退比较 key_name 与 platform 的字符串相等)

### 平台实现

| id | 展示名 | 缩写 | 凭证字段 | 端点/认证 |
|---|---|---|---|---|
| `kimi` | Kimi | K | api_key (password) | Kimi 开放平台额度 API, Bearer |
| `xfyun` | 讯飞星辰 | 讯 | cookie (textarea) | 讯飞星辰 MaaS API, Cookie 头 |
| `opencode-go` | OpenCode Go | Go | api_key (password) | `GET https://opencode.ai/zen/go/v1/usage`, Bearer;留空自动读 `~/.local/share/opencode/auth.json` |
| `mimo` | 小米 MiMo | M | cookie (textarea) | `https://platform.xiaomimimo.com/api/v1/tokenPlan/usage`, Cookie + Referer |
| `deepseek` | DeepSeek | D | api_key (password) | `https://api.deepseek.com/user/balance`, Bearer |
| `glm` | GLM | GL | token + organization + project | GLM 开放平台 coding plan 用量 API, Bearer |
| `openrouter` | OpenRouter | OR | api_key (password) | `https://openrouter.ai/api/v1/credits`, Bearer |
| `bailian` | 百炼 | BL | cli_path (text, 可选) | exec 调用 `bl usage token-plan --output json`,认证走 bl CLI 全局登录态 |
| `new-api` | New API | NA | base_url + channel_id + user + authorization | GET `{BaseUrl}/api/channel/{channel_id}`, `New-Api-User` + 原始 `Authorization` |
| `aliyun` | 阿里云 | AL | access_key_id + access_key_secret + package_types (select) | BssOpenApi 余额 + 资源包用量,RPC HMAC-SHA1 签名 |
| `ollama` | Ollama | O | cookie (textarea) | `GET https://ollama.com/settings` 解析 SSR HTML, Cookie;超时 30s + 传输错误重试 1 次 |
| `command-code` | Command Code | C | api_key (password) | `api.commandcode.ai/alpha/*`(官方 CLI 同款私有路由), Bearer;留空自动读 `~/.commandcode/auth.json` |
| `factory-droid` | Factory Droid | F | api_key (password) | `api.factory.ai/api/billing/limits`(官方 web 同款私有路由), Bearer;留空自动读 `~/.factory/.env`;旧账单模型兜底 |
| `minimax` | MiniMax | MX | api_key (password) | `{域名链}/v1/api/openplatform/coding_plan/remains`, Bearer(sk-cp- 订阅 Key) |
| `open-design` | Open Design | OD | cookie (textarea) | `amr-api.open-design.ai/api/v1/workspaces/...`(两步:发现 workspaceId → 查窗口), Cookie;302/401 统统按 Cookie 失效 |

- 每个 fetcher 的 `baseURL`/`apiURL` 可重写（构造时传空用默认）——测试通过该参数注入 httptest server
- OpenCode Go 走官方 JSON API `GET /zen/go/v1/usage`(2026-10 改版,旧 Dashboard 页面抓取/Workspace+Session Cookie 通道废弃):响应 `usage.{rolling,weekly,monthly}` 各窗口直接给 `percent`(0-100 已用)+ `resetsAt`(RFC3339);Percent 取最紧张窗口(同值 rolling > weekly > monthly),Remaining 展示全部窗口,PeriodResets 逐窗口倒计时;403 + `error.type=EntitlementError` = 未订阅;API Key 留空自动读 OpenCode CLI 的 `~/.local/share/opencode/auth.json`(`opencode-go.key`)
- DeepSeek 是余额型：`Kind="balance"`，响应 `{"is_available":bool,"balance_infos":[{"currency","total_balance",...}]}`；取**第一条非零余额**的币种（如 USD $0.00 + CNY ¥247.51 → 显示 `余额 ¥247.51 (CNY)`）；`is_available=false` 或全部余额为 0 → Error
- New API 是带余额的用量型：GET `{BaseUrl}/api/channel/{channel_id}`，请求头为 `New-Api-User: <User>` 和原始 `Authorization`；响应 `data.balance` 直接作为余额，`data.used_quota / 500000` 作为已用金额；有重置时间的渠道额外返回 `balance_reset_next_time`（Unix 秒），填入 `ResetAt` 供前端显示倒计时（部分部署平铺在顶层而非 `data` 内，fetcher 两级兜底）；`data` 缺失 → Error
- Ollama 无公开 quota API,只能抓 `ollama.com/settings` 的 SSR HTML:按 "Session usage"/"Hourly usage"/"Weekly usage" 标题切块,正则提取 `X% used`(兼容 `width:X%` 进度条)与 `data-time` 重置时间;Percent 取 5h 与周窗口较紧张者;ollama.com 偶发 >10s 慢响应 → 超时放宽 30s + 传输层错误重试 1 次(幂等 GET);302/登录页特征 → Cookie 失效提示
- Command Code 走官方 CLI(npm 包 command-code)同款私有路由 `/alpha/whoami`(团队用户取 orgId)+ `/alpha/billing/credits`(月度剩余 credits + 5h/周窗口 Used/Cap/ResetAt);主展示 = 5h 窗口绝对值,周窗口按紧张度并入 Percent;Key 留空自动读 `~/.commandcode/auth.json` 的 `apiKey`
- Factory Droid 优先新账单模型 `/api/billing/limits`(仅百分比,Total=100 惯例同 Ollama):Standard/Core 双池 × 5h/周/月窗口,Percent 取全部窗口最紧张者;**Core 接管**:Standard 顶格(≥100%)且 `overagePreference=droidCore` 时展示主体整体切 Core 池(否则 100% 钉死球面看不出第二管血);重置时间一律由 `secondsRemaining` 推算(windowEnd 真机可能过期);`usesTokenRateLimitsBilling=false` 时回退旧账单模型 `/api/organization/subscription/usage`(Standard/Premium,比率/绝对值双口径,容忍 usedRatio 恒 0 的脏数据);Key 留空自动读 `~/.factory/.env` 的 FACTORY_API_KEY
- MiniMax 是多结果抓取器(FetchMulti):`/v1/api/openplatform/coding_plan/remains` 返回套餐卡数组 `model_remains[]`(主套餐在前,视频/音乐赠送卡在后),每卡一条结果——单卡沿用平台默认展示,多卡以卡名派生 KeyName/缩写(AbbrFromName);字段是 `current_*_remaining_percent`(**剩余**%,必须反转为已用%,有回归测试守护);周总额=0 且无周剩余百分比 = 周无限制;三个区域域名组成回退链逐个尝试;业务码 1004/2049 = Key 类型/区域不匹配
- Open Design 两步走:先 `GET /api/v1/workspaces/current` 取真实 workspaceId(直接拿 current 查会 400),再查 `.../billing/coding-plan-usage`;窗口按 `durationSeconds` 识别(18000=5h,604800=7d),credits 是十进制字符串按 ParseFloat 算百分比;API key(sk-amr-)读不到额度只能用 Cookie;**禁止自动重定向**——Cookie 失效时控制面回 302 跳登录页,跟随了就会把登录页 HTML 报成解析失败,必须并入 Cookie 失效分支
- `format.go` 的 `formatNum` 做千分位展示格式化(仅内部使用);`clampPercent`(factorydroid.go)钳制 0-100,Ollama/MiniMax/OpenDesign 共用

### CLI 型抓取器（bailian，首个非 HTTP 实现）

- `bl usage token-plan --output json` 返回 `per5HourPercentage` / `per1WeekPercentage`（∈[0,1] **已用比例**，均可能缺失）+ `per*ResetTime`（毫秒时间戳）；无绝对 Credits 数，不填 Used/Total
- 认证为 bl CLI 全局登录态（`bl auth login --console` 或 `--config token-plan --api-key sk-sp-xxx`），命令不支持按次注入 Key → 凭证字段只有可选 `cli_path`，注册表标记 `Credentialless: true`：
  - `fetchAll`：无凭证组时也创建一个空组任务（否则启用后被跳过，球格不显示）
  - 前端徽标：空字段显示"免凭证"而非"未配置"；`collectProviders` 保留空组提交（后端 CredKeys 保持非空）
- Windows：npm 全局安装的 `bl` 是 `bl.cmd` shim，`os/exec` 不能直接执行，需 `cmd /c` 包装（见 `bailian.go` 的 `run`）；`.exe` 与非 Windows 直接执行
- 容错：stdout 从首个 `{` 起 JSON 解码（容忍前导告警/尾部更新提示）；退出码非 0 且 stderr 含 "unknown command" → 提示升级 CLI
- 测试用假 CLI 脚本注入 `f.execPath`（Windows 写 `.cmd`、其余写 shell 脚本），不起 httptest（见 `bailian_test.go`）

### 失败语义

- 任何平台出错（网络/鉴权/解析）→ `QuotaResult.Error` 非空，其余字段尽力填充
- 调用方（app.go）不 panic：`fetchAll` 并发收集，前端按 Error 显示"失败"状态
- 未配置凭证 → 各 fetcher 自行返回带 Error 的结果（不 panic）

### 测试模式

全部用 `net/http/httptest` 起假服务，`baseURL` 指向假服务：
- `kimi_test.go` / `xfyun_test.go` / `opencode_go_test.go` / `mimo_test.go` / `deepseek_test.go` 覆盖成功/失败/异常 JSON 路径
- `new-api_test.go` 覆盖 URL/请求头、余额与已用金额换算、缺配置、401、非 200、异常 JSON 和缺失 data
- `ollama_test.go` / `commandcode_test.go` / `factorydroid_test.go` / `minimax_test.go` / `opendesign_test.go` 覆盖成功/失败/鉴权失效/异常结构与边界(含 Core 接管、剩余%反转、302 不跟随等回归)
- 例外：`bailian_test.go` 用假 CLI 脚本（Windows `.cmd` / shell 脚本）注入 `f.execPath`，不起 httptest
- `registry_test.go` 校验注册表完整性（15 个、顺序、字段定义、Build 可执行——空凭证 + 隔离用户目录,不打真实网络）

---

## 关键文件

| 文件 | 职责 |
|---|---|
| `internal/fetcher/types.go` | QuotaResult + Fetcher/MultiFetcher 接口 + Kind 常量（契约核心） |
| `internal/fetcher/registry.go` | ProviderDef + 注册表（新增 Provider 的唯一入口） |
| `internal/fetcher/kimi.go` / `xfyun.go` / `opencode_go.go` / `mimo.go` / `deepseek.go` / `glm.go` / `openrouter.go` / `bailian.go` / `new-api.go` / `aliyun.go` / `ollama.go` / `commandcode.go` / `factorydroid.go` / `minimax.go` / `opendesign.go` | 各平台实现 |
| `internal/fetcher/format.go` | 千分位格式化 + AbbrFromName 缩写派生 + SettleResult 身份归一 |

---

## Must NOT Change

- `QuotaResult` JSON 字段名（前端渲染契约）
- Fetcher 接口签名 `Fetch() QuotaResult`
- Provider id 与注册表顺序（config 存储、TestConnection、前端绑定共用契约）
- fetchAll 的 results 顺序 = 启用 Provider 的注册表顺序（前端球格顺序绑定）
