# 计划：新增 百炼 Token Plan 用量检测 Provider

## 摘要

新增 `bailian` Provider（展示名"百炼"），通过官方百炼 CLI 的 `bl usage token-plan --output json` 命令获取 Token Plan 的 5小时/周 双窗口用量百分比与重置时间。这是项目**首个基于外部命令行（exec）而非 HTTP 的抓取器**。按 `docs/ADDING_A_PROVIDER.md` 的注册表驱动架构实现，前端配置面板与球格零改动自动适配。

## 现状分析

* **注册表驱动**：`internal/fetcher/registry.go` 的 `registry` 数组追加条目即可；`app.go` 的 `GetConfig/SaveConfig/fetchAll/TestConnection` 与前端全部动态适配。

* **现有 8 个 Provider**（kimi/xfyun/opencode-go/mimo/deepseek/glm/openrouter/aliyun），全部 HTTP 型，用 `httptest` 测试。

* **先例**：glm/openrouter/aliyun 加入时未更新 `config.AllProviderIDs`（仍为 5 项），靠 `SaveConfig` 动态 append 配置条目，已验证可行 → bailian 同样处理，`config.go` 零改动。

* **QuotaResult 契约**：`Percent`（已用百分比）驱动球色，`Remaining` 多行文本明细，`ResetAt`（RFC3339）驱动前端倒计时（`main.js` 已有 `data-reset-at` 逻辑）。

## 关键事实（已实测/源码确认）

1. **真实输出**（本机已配置 bl，实际执行）：

```json
{
  "per1WeekPercentage": 0.005016167,
  "per1WeekResetTime": 1789696860000
}
```

1. **CLI 源码**（modelstudioai/cli `packages/commands/src/commands/usage/token-plan.ts`）：

   * 字段：`per5HourPercentage` / `per5HourResetTime` / `per1WeekPercentage` / `per1WeekResetTime`，**均可选**（缺失 = 无数据或额度无限）

   * `Percentage` ∈ \[0,1] 为**已用比例**（文本模式展示为 "X% used"）；`ResetTime` 为毫秒时间戳
2. **认证**：命令 `auth: "console"` —— 凭证由 CLI 全局管理（`bl auth login --console` 或 `bl auth login --config token-plan --api-key sk-sp-xxx`），**不支持按次注入 API Key**。
3. **Windows 注意**：npm 全局安装的 `bl` 在 Windows 上是 `bl.cmd` shim，`os/exec` 无法直接执行，需 `cmd /c` 包装；原生二进制安装则为 `bl.exe`。
4. CLI 会输出自动更新提示（写 stderr/尾部），stdout 需容错解析 JSON。

## 设计决策

| 决策点        | 选择                                                      | 理由                                                |
| ---------- | ------------------------------------------------------- | ------------------------------------------------- |
| 凭证字段       | 单一 `cli_path`（text，Plain 可选字段，默认空 = PATH 查找）            | Key 无法注入用量命令（console 认证）；cli\_path 兼顾测试注入与自定义安装路径 |
| Kind       | `KindUsage`（默认）                                         | Percent 驱动球色                                      |
| 主窗口        | 百分比更高者优先；相等时 5小时窗口优先                                    | 与 Kimi/GLM 现有规则一致                                 |
| Used/Total | 不填（CLI 不提供绝对值），只填 Percent/ResetAt/Remaining             | 数据源限制                                             |
| ResetAt    | 主窗口 `ResetTime` 毫秒 → `time.UnixMilli().Format(RFC3339)` | 与 glm.go 一致，前端倒计时自动生效                             |
| 超时         | `exec.CommandContext` + 20s                             | HTTP 型是 10s；CLI 有 Node 启动开销留余量                    |
| JSON 解析    | 取首个 `{` 后用 `json.Decoder.Decode`                        | 容忍 stdout 前导告警与尾部更新提示                             |
| 多组凭证       | 不阻止（架构自动支持），字段 Label 提示勿配多组                             | 凭证是 CLI 全局态，多组会重复执行同一命令                           |

## 变更清单

### 1. 新建 `internal/fetcher/bailian.go`

```go
// BailianFetcher 通过百炼 CLI(bl) 的 usage token-plan 命令查询 Token Plan 用量。
// 端点: bl usage token-plan --output json
// 认证: CLI 全局登录态(bl auth login --console 或 --config token-plan --api-key sk-sp-xxx)
type BailianFetcher struct {
    execPath string // bl 可执行路径(空 = 从 PATH 查找)
}

func NewBailianFetcher(execPath string) *BailianFetcher

type bailianUsage struct { // 四个字段均为指针,缺失 = 无数据
    Per5HourPercentage *float64 `json:"per5HourPercentage"`
    Per5HourResetTime  *int64   `json:"per5HourResetTime"`
    Per1WeekPercentage *float64 `json:"per1WeekPercentage"`
    Per1WeekResetTime  *int64   `json:"per1WeekResetTime"`
}
```

`Fetch()` 流程：

1. 构造命令（Windows 兼容，见下）；`LookPath` 失败 → `Error = "未找到 bl 命令,请先安装百炼 CLI (npm install -g bailian-cli)"`
2. `exec.CommandContext`（20s 超时）运行，收集 stdout/stderr
3. 退出码非 0：stderr 含 "unknown command"（大小写不敏感）→ `"bl 版本过低,请升级: npm update -g bailian-cli"`；否则 `"bl 命令失败: " + stderr 截断(约200字符)`
4. 解析：`stdout[strings.Index("{")]` 起 `json.Decoder.Decode` → 失败 → `"解析 bl 输出失败: ..."`
5. 两个窗口百分比均缺失 → `"响应中未找到用量数据(可能未订阅 Token Plan 或额度无限)"`
6. 组装：

   * 主窗口 = 百分比高者，相等取 5 小时窗口；`result.Percent = ratio * 100`

   * `result.ResetAt` = 主窗口毫秒时间戳 → RFC3339

   * `result.Remaining` = 每窗口一行：`fmt.Sprintf("%.1f%% 已用 (%s)", p*100, label)`，label 为 `5小时窗口` / `周窗口`，多行 `\n` 连接

Windows 命令构造：

```go
// Windows: npm shim 是 bl.cmd,须 cmd /c 包装;原生安装为 bl.exe
if runtime.GOOS == "windows" {
    if execPath == "" {
        if _, err := exec.LookPath("bl"); err != nil { → 未安装错误 }
        return exec.Command("cmd", "/c", "bl", args...)
    }
    if 后缀 .cmd/.bat { return exec.Command("cmd", "/c", execPath, args...) }
}
return exec.Command(execPath, args...) // 非 Windows / .exe
```

### 2. 修改 `internal/fetcher/registry.go`

`registry` 数组末尾（aliyun 之后）追加：

```go
{
    ID:          "bailian",
    DisplayName: "百炼",
    Abbr:        "BL",
    Kind:        KindUsage,
    LoginURL:    "https://bailian.console.aliyun.com/cn-beijing?tab=plan#/efm/subscription/overview",
    Fields: []CredentialField{
        {Key: "cli_path", Label: "bl 命令路径(可选,默认 PATH 查找;凭证由 bl CLI 全局登录管理,请勿配置多组)", Type: "text", Plain: true},
    },
    Build: func(creds map[string]string) Fetcher {
        return NewBailianFetcher(creds["cli_path"])
    },
},
```

### 3. 修改 `internal/fetcher/registry_test.go`

* `TestGetAll_ContainsEightProviders_InStableOrder` → 改名/断言 9 个，`want` 数组末尾加 `"bailian"`

### 4. 新建 `internal/fetcher/bailian_test.go`

测试策略（exec 型标准做法）：临时目录写假 CLI 脚本（Windows 用 `.cmd`：`@echo {...}`，经 cmd /c 分支执行；Unix 用 `chmod +x` shell 脚本），注入 `f.execPath`。用例：

| 用例                           | 输入                                  | 断言                                         |
| ---------------------------- | ----------------------------------- | ------------------------------------------ |
| `TestBailian_CLINotFound`    | execPath 指向不存在路径                    | Error 含 "bl"                               |
| `TestBailian_OK_BothWindows` | 两窗口 JSON                            | Percent = 较高者×100；Remaining 两行；ResetAt 可解析 |
| `TestBailian_OK_OnlyWeek`    | 仅 `per1Week*`                       | 用周窗口数据                                     |
| `TestBailian_EmptyData`      | `{}`                                | Error 含 "未找到用量数据"                          |
| `TestBailian_CLIError`       | stderr 输出 + exit 1                  | Error 含 "bl 命令失败"                          |
| `TestBailian_UnknownCommand` | stderr 含 "Unknown command" + exit 1 | Error 含 "升级"                               |
| `TestBailian_BadJSON`        | stdout 非 JSON                       | Error 含 "解析"                               |

### 5. 修改 `docs/wiki/05-fetching-platforms.md`

* 平台表格加 `bailian` 行；补一段"CLI 型抓取器"说明（首个非 HTTP 实现、exec 超时/Windows shim 处理、假脚本测试法）

### 6.（执行时核查，可选）`README.zh-CN.md`

平台凭证表格若含完整 Provider 清单则补一行；仅列部分则不动。

### 不改动的文件

* `app.go` / `frontend/*` / `internal/config/config.go`（glm/openrouter/aliyun 同款零改动路径）

## 假设与决策记录

* `bl usage summary` 命令在当前 CLI 版本不存在/不含 Token Plan 窗口数据，按用户指示改用 `bl usage token-plan`（已实测拿到真实输出）

* Token Plan 用量 API 只返回百分比，无绝对 Credits 数 → 不填 Used/Total，Percent 为唯一主指标

* 默认不启用（不进 `DefaultProviderIDs`），用户需先安装并登录 bl CLI 后在设置中启用

* `cli_path` 为 Plain 字段：非敏感，设置界面原样回显（同 aliyun 的 `package_types`）

## 验证步骤

1. `go test ./internal/fetcher/ -run Bailian -v` — 新用例全绿
2. `go test ./...` — 全绿（含 registry 完整性 9 providers）
3. `cd frontend && npm run build` — 前端构建无碍
4. `wails build` — 整体构建成功
5. 真机验证：启动应用 → 设置启用"百炼" → 球格出现 BL 格，展开面板显示 `0.5% 已用 (周窗口)` 等行 + 周窗口重置倒计时（对照 `bl usage token-plan` 文本输出）

