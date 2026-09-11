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
    Used        float64   `json:"used"`
    Total       float64   `json:"total"`       // 平台返回则填，否则 0
    Percent     float64   `json:"percent"`     // Used/Total*100; 无总量时由剩余百分比反推
    Remaining   string    `json:"remaining"`   // 原始剩余描述（如 "1,200/18,000 次"）
    ResetAt     string    `json:"reset_at"`    // 下次重置时间 ISO8601，空则未知
    LastUpdated time.Time `json:"last_updated"`
    Error       string    `json:"error"`       // 非空表示失败
}

type Fetcher interface {
    Fetch() QuotaResult
}
```

### Provider 注册表（registry.go）

```go
type CredentialField struct {
    Key   string `json:"key"`   // 凭证 key
    Label string `json:"label"` // 显示名
    Type  string `json:"type"`  // "password" | "text" | "textarea"
}

type ProviderDef struct {
    ...
    // Credentialless 免凭证 Provider(凭证存于外部工具,如百炼 CLI 全局登录态):
    // 启用即可用,fetchAll 为其创建空凭证组任务,前端徽标显示"免凭证"
    Credentialless bool
    ...
}

func GetAll() []ProviderDef            // 固定顺序 = 推荐展示顺序
func Get(id string) (ProviderDef, bool)
```

### 平台实现

| id | 展示名 | 缩写 | 凭证字段 | 端点/认证 |
|---|---|---|---|---|
| `kimi` | Kimi | K | api_key (password) | Kimi 开放平台额度 API, Bearer |
| `xfyun` | 讯飞星辰 | 讯 | cookie (textarea) | 讯飞星辰 MaaS API, Cookie 头 |
| `opencode-go` | OpenCode Go | Go | workspace_id (text) + session_token (password) | `https://opencode.ai/workspace/{wsID}/go`, auth Cookie |
| `mimo` | 小米 MiMo | M | cookie (textarea) | `https://platform.xiaomimimo.com/api/v1/tokenPlan/usage`, Cookie + Referer |
| `deepseek` | DeepSeek | D | api_key (password) | `https://api.deepseek.com/user/balance`, Bearer |
| `bailian` | 百炼 | BL | cli_path (text, 可选) | exec 调用 `bl usage token-plan --output json`,认证走 bl CLI 全局登录态 |

- 每个 fetcher 的 `baseURL`/`apiURL` 可重写（构造时传空用默认）——测试通过该参数注入 httptest server
- OpenCode Go 抓取的是 Dashboard 页面（SSR hydration + data-slot 双模式解析）。2026-09 真机确认：SSR hydration（`rollingUsage:$R[n]={status:"ok",resetInSec:3343,usagePercent:13.6,usage:..,limit:..}`）**仍在**，是主解析路径，但 `usagePercent` 已改为**小数**（如 13.6）；data-slot 备选路径中 label 随 `oc_locale` 变化（中文 `5 小时用量`/`每周用量`/`每月用量`，英文 `Rolling Usage` 等），百分比带 SolidStart 流式注释，重置时间为 `reset-time` 内本地化短语（中文 `重置于 55 分钟`/`2 天 8 小时`，英文 `Resets in ...`），由 `parseDurationToSec`（中英单位）估算秒数，失败则 resetInSec=0
- DeepSeek 是余额型：`Kind="balance"`，响应 `{"is_available":bool,"balance_infos":[{"currency","total_balance",...}]}`；取**第一条非零余额**的币种（如 USD $0.00 + CNY ¥247.51 → 显示 `余额 ¥247.51 (CNY)`）；`is_available=false` 或全部余额为 0 → Error
- `format.go` 的 `formatNum` 做千分位展示格式化（仅内部使用）

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
- 例外：`bailian_test.go` 用假 CLI 脚本（Windows `.cmd` / shell 脚本）注入 `f.execPath`，不起 httptest
- `registry_test.go` 校验注册表完整性（9 个、顺序、字段定义、Build 可执行）

---

## 关键文件

| 文件 | 职责 |
|---|---|
| `internal/fetcher/types.go` | QuotaResult + Fetcher 接口 + Kind 常量（契约核心） |
| `internal/fetcher/registry.go` | ProviderDef + 注册表（新增 Provider 的唯一入口） |
| `internal/fetcher/kimi.go` / `xfyun.go` / `opencode_go.go` / `mimo.go` / `deepseek.go` | 各平台实现 |
| `internal/fetcher/format.go` | 千分位格式化 |

---

## Must NOT Change

- `QuotaResult` JSON 字段名（前端渲染契约）
- Fetcher 接口签名 `Fetch() QuotaResult`
- Provider id 与注册表顺序（config 存储、TestConnection、前端绑定共用契约）
- fetchAll 的 results 顺序 = 启用 Provider 的注册表顺序（前端球格顺序绑定）
