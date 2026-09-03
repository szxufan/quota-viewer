# 多机状态同步（OSS + AES-256）

**When to read**: 修改状态同步（发布/订阅）、加密格式、OSS 上传/下载、凭证粒度同步开关时。

---

## 核心内容

设计文档：`.trae/documents/oss-state-sync.md`。

多台机器共享配额状态：**发布端**（已配置凭证的实例）每次刷新后将状态加密上传到阿里云 OSS；**订阅端**只配置状态文件公网 URL + 加密密码，下载解密后直接展示，完全禁用本地抓取。

### 加密格式

- 密钥派生：`SHA-256(密码 UTF-8 字节)` → 32 字节 AES-256 密钥
- 算法：AES-256-GCM，12 字节随机 nonce（`crypto/rand`）
- 文件内容：`base64( nonce ‖ ciphertext )`，单对象覆盖写
- 载荷（`syncstate.Payload`）：`{version:1, published_at, next_refresh_at, results:[QuotaResult]}`

### 发布端流程（`sync.go publishState`）

`Refresh()` 成功后异步执行：

1. `filterSyncExcluded` 按 `ProviderConfig.SyncExcludes`（与 Keys 对齐）剔除被排除凭证组的结果（`r.ID`+`r.KeyIndex` 匹配；越界/未配置视为同步）；
2. 全部排除 → 跳过上传；
3. `Payload.NextRefreshAt = now + RefreshIntervalMin`；
4. `Encrypt` → `UploadOSS`（官方 SDK `aliyun-oss-go-sdk` PutObject 覆盖写）；
5. 成功/失败均 `EventsEmit("sync:status", msg)` 推送到设置区块，不影响本地展示。

### 订阅端流程（`sync.go fetchRemoteState`）

- `Download(url)`（30s 超时，4MB 上限）→ `Decrypt` → 写 `a.cache` + `EventsEmit("quota:update")`
- `startAutoRefresh` 订阅分支：立即拉取一次，之后 sleep = `NextRefreshAt + 60s - now`（clamp [60s, 24h]），失败兜底 15 分钟
- `Refresh()` 订阅模式 = 重新下载（手动刷新按钮仍可用）；本地 provider 抓取完全不执行
- 前提：OSS bucket 开启**公共读**（订阅端匿名 HTTP GET，无需 OSS 凭证）

### 配置（`config.SyncConfig` / `Config.Sync`）

| 字段 | 模式 | 说明 |
|---|---|---|
| `mode` | 共有 | `""`(关闭) / `publish` / `subscribe` |
| `password` | 共有 | 加密密码（SHA-256 派生密钥；明文存储，下发前端掩码） |
| `oss_endpoint/bucket/key/access_id/access_secret` | 发布 | OSS 连接信息（secret 下发掩码） |
| `url` | 订阅 | 状态文件公网地址 |

凭证粒度开关：`ProviderConfig.SyncExcludes []bool`（`json:"sync_excludes"`），"排除"语义使零值 = 同步，旧配置无需迁移。前端仅发布模式渲染该开关；未渲染时不提交该字段，`SaveConfig` 收到 nil 保留已存值。

### 绑定方法（`sync.go`）

- `SaveSyncConfig(SyncConfig) error`：模式校验 + 密码/Secret 掩码还原 + 落盘
- `TestSync() string`：发布模式上传临时对象（`对象路径 + ".test"`）后删除；订阅模式试下载解密，返回条数/发布时间/耗时
- `GetConfig()` 返回新增 `sync`（掩码后）与各 provider 的 `sync_excludes`

### 前端（`main.js` + `settings-helpers.js`）

- 通用 Tab 顶部"状态同步"区块：模式 select + 按 `syncFieldsForMode(mode)` 动态渲染的字段 + 测试按钮 + 状态行
- 另一模式字段以隐藏 input 承载当前值随保存提交，切换模式不丢配置
- 订阅模式禁用"刷新间隔"输入
- 账号页每组凭证渲染"同步到 OSS"复选框（仅发布模式，默认勾选），`collectProviders` 输出 `sync_excludes`
- 监听 `sync:status` 事件显示最近上传/下载结果

---

## 关键文件

| 文件 | 职责 |
|---|---|
| `internal/syncstate/payload.go` | Payload 结构（版本固定 1） |
| `internal/syncstate/crypto.go` | DeriveKey / Encrypt / Decrypt（AES-256-GCM） |
| `internal/syncstate/oss.go` | UploadOSS / DeleteOSSObject（官方 SDK） |
| `internal/syncstate/download.go` | Download（公网匿名 GET） |
| `sync.go` | publishState / fetchRemoteState / filterSyncExcluded / SaveSyncConfig / TestSync |
| `app.go` | Refresh 模式分支、startAutoRefresh 订阅分支、GetConfig/SaveConfig 扩展 |

---

## Must NOT Change

- 密钥派生固定为 `SHA-256(password)`、密文格式固定为 `base64(nonce‖ciphertext)`——两端必须一致，改格式 = 破坏性变更（升级 `Payload.Version`）
- `SyncExcludes` 的"排除"语义（零值 = 同步），改为"包含"语义会破坏旧配置默认行为
- 订阅端不得执行任何本地抓取（`fetchAll` 在订阅模式下不可达）
- 同步失败只能推 `sync:status` 事件，不得影响本地展示与刷新主流程
