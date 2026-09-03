# 多机状态同步（阿里云 OSS + AES-256 加密）

## 概述

支持多台机器运行 quota-viewer 而无需逐台配置凭证：

- **发布端**（已配置好凭证的实例）：配置 OSS 信息 + 加密密码，每次刷新后将状态与预计下次刷新时间用 AES-256-GCM 加密，通过官方 OSS SDK 上传到指定路径。
- **订阅端**（其他实例）：只配置公网 HTTP 地址 + 加密密码，下载解密后直接展示，按"预计下次刷新时间 + 60 秒"自动更新，完全禁用本地抓取。

## 现状分析（基于实际代码）

- `app.go`：`Refresh()`（L104）抓取后写 `a.cache` 并 `EventsEmit("quota:update")`；自动刷新为 `startAutoRefresh()`（L767-779）无限循环 goroutine，每轮开头重读 `cfg.RefreshIntervalMin`，无 ticker/无法停止。
- `internal/config/config.go`：`Config`（L10-16）+ `ProviderConfig`（L19-28），持久化到 `%APPDATA%\quota-viewer\config.json`，`Load()`（L100）/`Save()`（L244）。所有凭证明文存储，`GetConfig` 下发前端时用 `maskSecret`（app.go:781）掩码，`SaveConfig` 用 `mergeKeys`（app.go:330）还原掩码。
- 前端 `frontend/src/main.js`：设置界面按 provider `fields` 元数据动态渲染（`renderProviderList` L388），保存走 `SaveConfig(providers, interval)`（L665-681）。
- `go.mod`：直接依赖仅 systray + wails；**无 OSS SDK、无加密库**（加解密用标准库 `crypto/aes` + `crypto/cipher` GCM 即可，无需 x/crypto）。
- 项目无任何文件上传/下载代码；无 `GetState` 绑定方法。

## 决策

| 决策点 | 结论 |
|---|---|
| OSS 上传方式 | 官方 SDK `github.com/aliyun/aliyun-oss-go-sdk`（用户确认） |
| 订阅端本地抓取 | 完全禁用，仅展示远端状态（用户确认） |
| 凭证粒度同步开关 | 每个凭证组独立"同步到 OSS"开关（默认勾选）；`QuotaResult` 含 `ID`+`KeyIndex`（types.go:14-34），可按组过滤 |
| 加密算法 | AES-256-GCM（标准库），随机 12 字节 nonce |
| 密钥派生 | `SHA-256(密码 UTF-8 字节)` → 32 字节 AES-256 密钥 |
| 密文格式 | base64( nonce ‖ ciphertext )，单文件单对象 |
| 密码/AK 存储 | 与现状一致明文存 config.json，下发前端时掩码 |

## 变更明细

### 1. 新包 `internal/syncstate`

**`internal/syncstate/payload.go`** — 同步载荷定义：

```go
// Payload 是发布端上传、订阅端下载的状态快照。
type Payload struct {
    Version       int                   `json:"version"`        // 固定 1
    PublishedAt   time.Time             `json:"published_at"`
    NextRefreshAt time.Time             `json:"next_refresh_at"` // 预计下次刷新时间
    Results       []fetcher.QuotaResult `json:"results"`
}
```

**`internal/syncstate/crypto.go`** — 加解密：

- `DeriveKey(password string) []byte`：`sha256.Sum256([]byte(password))`
- `Encrypt(p *Payload, password string) ([]byte, error)`：JSON 序列化 → GCM 加密（`crypto/rand` 生成 nonce）→ 输出 `base64(nonce‖ciphertext)`
- `Decrypt(data []byte, password string) (*Payload, error)`：反向操作；base64/长度/Tag 校验失败均返回中文错误

**`internal/syncstate/oss.go`** — 上传（发布端）：

- `UploadOSS(ctx, endpoint, bucket, objectKey, akID, akSecret string, data []byte) error`：用 `oss.New(endpoint, akID, akSecret)` + `bucket.PutObject(objectKey, bytes.NewReader(data))`，包装中文错误

**`internal/syncstate/download.go`** — 下载（订阅端）：

- `Download(ctx, url string) ([]byte, error)`：`http.Client{Timeout: 30s}` GET，非 200 返回带状态码的错误

### 2. `internal/config/config.go`

`Config` 新增字段（Load/Save 无需改动，JSON 自动兼容旧配置文件）：

```go
type SyncConfig struct {
    Mode            string `json:"mode"`              // "" | "publish" | "subscribe"
    Password        string `json:"password,omitempty"`
    OSSEndpoint     string `json:"oss_endpoint,omitempty"`
    OSSBucket       string `json:"oss_bucket,omitempty"`
    OSSKey          string `json:"oss_key,omitempty"`
    OSSAccessID     string `json:"oss_access_id,omitempty"`
    OSSAccessSecret string `json:"oss_access_secret,omitempty"`
    URL             string `json:"url,omitempty"`
}
// Config 内：Sync SyncConfig `json:"sync,omitempty"`
```

### 3. `app.go`

- **发布路径**：`Refresh()`（L104）成功更新 cache 后，若 `cfg.Sync.Mode == "publish"`，异步 `go a.publishState(results)`：
  - **凭证粒度过滤**：先按 `ProviderConfig.SyncExcludes` 过滤 results（`r.ID` 匹配 provider、`SyncExcludes[r.KeyIndex] == true` 的结果剔除；切片越界视为 false 即同步），全部排除则跳过上传；
  - 构造 `Payload{NextRefreshAt: now + RefreshIntervalMin}` → `Encrypt` → `UploadOSS`；失败仅 `EventsEmit("sync:status", 错误信息)`，不影响本地展示。
- **配置保存**：`SaveConfig`（L250）的 `ProviderInput` 新增 `SyncExcludes []bool`（前端随 providers 一起提交），在第 2 步合并段（L284-297）写入 `pc.SyncExcludes = config.AlignSyncExcludes(in.SyncExcludes, len(pc.Keys))`；`GetConfig`（L129）下发时各 provider 带上 `sync_excludes`。
- **订阅路径**：
  - 新增 `fetchRemoteState()`：`Download(cfg.Sync.URL)` → `Decrypt` → 写 `a.cache` + `EventsEmit("quota:update")`，返回 `NextRefreshAt`。
  - `Refresh()`：订阅模式下改为调 `fetchRemoteState()` 并返回 cache，不做本地抓取；`fetchAll`（L586）在订阅模式下直接跳过。
  - `startAutoRefresh()`（L767）：每轮循环开头判断模式；订阅模式 sleep 时长 = `NextRefreshAt + 60s - now`（clamp 到 [60s, 24h]，异常兜底 15min），发布/关闭模式维持现状逻辑。
- **绑定方法**：
  - `SaveSyncConfig(s SyncConfig) error`：掩码还原（password/AK secret 收到掩码值时保留旧值）→ 写 `cfg.Sync` → `cfg.Save()`。
  - `TestSync() string`：发布模式试上传一个测试对象后删除（或仅 `PutObject` 到同 key 前缀临时对象）；订阅模式试下载+解密，返回耗时或中文错误（风格同 `TestConnection` L402）。
  - `GetConfig()`（L129）返回中新增 `sync`（password 与 OSSAccessSecret 掩码后下发）。

### 4. 前端

- **`frontend/src/index.html`**：设置页顶部新增"状态同步"区块（模式 select + 条件字段容器 + 测试按钮 + 状态行）。
- **`frontend/src/settings-helpers.js`**：新增纯函数 `syncFieldsForMode(mode)`，返回当前模式需展示的字段清单（publish：endpoint/bucket/key/akid/aksecret/password；subscribe：url/password），便于单测。
- **`frontend/src/main.js`**：
  - `loadConfig()`（L349）后渲染同步区块，按 `sync.mode` 切换字段可见性；密文字段用掩码 placeholder（沿用现有模式）。
  - **凭证粒度开关**：`renderCredGroup`（L505）在每组凭证页（与预算输入同区域）渲染"同步到 OSS"复选框，默认勾选；仅当同步模式为 publish 时显示。`collectProviders()`（L367）收集每组勾选状态为 `sync_excludes` 数组随 providers 提交。
  - 保存时收集同步配置调 `SaveSyncConfig`，再调原 `SaveConfig` 保存其余项（或合并顺序：先 sync 后 providers，互不影响）。
  - 订阅模式下隐藏/禁用刷新间隔输入与"立即刷新"中无意义的部分（刷新按钮仍可用，触发重新下载）。
  - 监听 `sync:status` 事件，在设置区块显示最近一次上传/下载结果。

### 5. 依赖

- `go get github.com/aliyun/aliyun-oss-go-sdk`（go.mod/go.sum 更新）。

### 6. 测试（用户规则：代码与测试同步）

- `internal/syncstate/crypto_test.go`：密钥派生确定性、加解密往返、错误密码/篡改密文解密失败。
- `internal/syncstate/payload_test.go`：Payload JSON 编解码（含 QuotaResult 嵌套）。
- `internal/syncstate/download_test.go`：`httptest.Server` 模拟 200/404。
- `internal/config/config_test.go`：新增 SyncConfig 保存/加载往返用例。
- `app_test.go`：`SaveSyncConfig` 掩码还原逻辑用例（沿用现有测试风格）。
- `frontend/test/settings-helpers.test.mjs`：`syncFieldsForMode` 三种模式用例。
- OSS 真实上传不测（需真实凭证），`TestSync` 手工验证。

### 7. 文档（用户规则：代码与文档同步）

- 新增 `docs/wiki/11-state-sync.md`：架构、加密格式、配置项、发布/订阅两端行为。
- 更新 `docs/wiki/02-module-catalog.md`（新包条目）与 `docs/wiki/07-config-model.md`（SyncConfig、ProviderConfig.SyncExcludes 字段）。

## 验证步骤

1. `go build ./...` 通过。
2. `go test ./...` 全绿。
3. `cd frontend && npm test`（vitest）全绿。
4. `wails dev` 手工验证：发布端配置 OSS + 密码，刷新后 OSS 控制台可见加密对象；订阅端配置对象公网 URL + 同密码，能展示状态并按 NextRefreshAt+60s 自动更新；错误密码显示解密失败。

## 假设

- OSS bucket 已设为**公共读**（订阅端走公网匿名 HTTP 下载，无需 OSS 凭证）；对象 key 固定，每次覆盖上传。
- 同步密码明文存本地 config.json，与现有凭证存储策略一致。
- 订阅端不校验发布时间新旧以外的内容（无签名，仅加密；公网地址本身即能力凭证）。
