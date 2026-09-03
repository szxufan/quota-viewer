# Quota Viewer 自动升级方案

## 概述

为 quota-viewer（Wails v2 Windows 桌面应用）实现**全自动静默升级 + 本地一键自动发布**：

- 版本清单与安装包托管在**阿里云 OSS**（公共读 bucket，复用现有 OSS 渠道）
- 升级方式为**下载完整 NSIS 安装包 → 校验 SHA256 → `/S` 静默覆盖安装 → 自动重启**
- 触发时机：**启动后自动检查 + 每 6 小时轮询**，全程无需用户操作
- **仅实现 Windows**（与 README "Windows 10+" 及代码现状一致：非 Windows 仅有 `workarea_other.go` 桩）；NSIS 是 Windows 专用，macOS/Linux 如需支持要走"替换 .app/二进制 + 重启"的另一套机制，本期不做，但清单结构预留多平台扩展
- **源码零硬编码升级 URL**：清单地址、OSS 凭证等发布配置全部放在 `release.env`（gitignore，不入库），发布时经 ldflags 注入；客户端 exe 中仅含无害的公开 HTTPS 地址，OSS 写凭证只在发布机存在

## 现状分析

| 项 | 现状 | 影响 |
|---|---|---|
| 版本号 | 无应用版本常量、无 ldflags 注入；`build/windows/info.json` 为 `{{.Info.ProductVersion}}` 占位符，`wails.json` 未设置 `info.productVersion` | 需先建立版本机制，客户端才能比较新旧 |
| 更新检查 | 全库无 update/upgrade/release 相关代码 | 全新实现 |
| 安装器 | `project.nsi` 安装到 `$PROGRAMFILES64`，`REQUEST_EXECUTION_LEVEL` 默认 `admin`（wails_tools.nsh 第 31 行） | admin 安装会弹 UAC，**无法真正静默**；需改为每用户安装 |
| 可复用 | `internal/syncstate/download.go`（公网匿名 GET，但 4MB 上限不适合安装包）、`aliyun-oss-go-sdk` 依赖（发布工具上传用）、`OnStartup` 钩子（app.go 第 42 行） | updater 需自带大文件下载，不复用 Download |
| 前端 | 设置面板已有 Toast 体系；Wails 绑定自动生成 | 用于展示版本号与"正在升级"提示 |
| 发布 | 无 Makefile/CI/发布脚本；`wails.json` 无仓库 URL | 用本地一键脚本发布，后续可平滑迁移 CI |

## 关键决策

1. **改为每用户安装（per-user）**：`project.nsi` 中 `!define REQUEST_EXECUTION_LEVEL "user"` + `InstallDir "$LOCALAPPDATA\${INFO_PRODUCTNAME}"`。`wails_tools.nsh`（第 138-159 行）已内置支持：user 模式下自动 `SetShellVarContext current`、卸载注册表写入 HKCU。这样 `/S` 静默安装无 UAC，升级后 `Exec` 重启的进程也是普通用户权限。
   - 代价：旧版若已装到 Program Files，需手动重装一次；计划中标注为已知迁移成本。
2. **版本单一来源 = `wails.json` 的 `info.productVersion`**：发布工具读取它，同时通过 `-ldflags "-X main.Version=x.y.z"` 注入 Go 常量，保证 exe 文件属性、NSIS 安装包、运行时 `main.Version` 三者一致。
3. **发布配置全部外部化（安全要求）**：新建 `release.env`（模板 `release.env.example` 入库，真实文件 gitignore）。内容：`UPDATE_MANIFEST_URL`、`OSS_ACCESS_KEY_ID`、`OSS_ACCESS_KEY_SECRET`、`OSS_ENDPOINT`、`OSS_BUCKET`。源码、客户端 exe、git 历史均不含这些值；OSS 写凭证仅存在于发布机。
4. **安全强度 = HTTPS + SHA256**：清单与安装包均经 HTTPS 下载；安装包下载时实时校验 SHA256（清单内提供），不匹配即丢弃。OSS 写权限隔离在发布环境。满足个人工具威胁模型；如未来需要防 OSS 泄露伪造升级，可再升级为 ed25519 签名清单。
5. **不新增第三方依赖**：semver 比较、SHA256 校验、下载均用标准库实现；发布工具复用已有 `aliyun-oss-go-sdk`。
6. **全自动无开关**：按用户选择不做"关闭自动更新"配置项；检查/下载/安装失败一律静默记录，不打扰用户。
7. **dev 构建不检查更新**：`main.Version == "dev"` 时跳过，避免开发机被升级。
8. **仅 Windows 实现，清单预留多平台**：`version.json` 采用 `platforms` 映射（键为 `GOOS/GOARCH`），本期只发布 `windows/amd64` 条目；updater 按 `runtime.GOOS/GOARCH` 取条目，查不到（如 darwin 构建）则静默跳过。未来支持 macOS/Linux 时只需新增条目并实现对应平台的替换逻辑，客户端协议不变。

## 变更明细

### 1. `main.go` — 版本变量

新增：

```go
// Version 由发布构建通过 -ldflags "-X main.Version=x.y.z" 注入；开发构建为 "dev"
var Version = "dev"
```

### 2. `wails.json` — 产品版本

增加 `info` 节（Wails 构建时填入 `build/windows/info.json` 占位符）：

```json
"info": {
  "companyName": "joeatgp",
  "productName": "Quota Viewer",
  "productVersion": "1.0.0",
  "copyright": "Copyright © 2026"
}
```

### 3. 新包 `internal/updater` — 核心升级逻辑（新目录）

**`internal/updater/updater.go`**：

- `Manifest` 结构体：顶层 `{version, notes}` + `platforms map[string]PlatformEntry`，`PlatformEntry = {url, sha256}`，平台键为 `windows/amd64` 形式
- `ManifestURL` 为包级变量，**默认空字符串**（源码无 URL）；空值时 `StartAuto` 直接跳过——dev 构建、无发布配置的构建都不检查更新
- `CheckLatest(ctx, current string) (*PlatformEntry, version string, ok bool, err error)`：匿名 GET 清单（15s 超时、1MB 上限）→ JSON 解析 → 取 `runtime.GOOS/GOARCH` 对应条目（不存在则 ok=false 静默跳过）→ `Newer(latest, current)` semver 比较
- `DownloadInstaller(ctx, e *PlatformEntry, version string) (path string, err error)`：下载到 `%TEMP%\quota-viewer-update\<version>-installer.exe.tmp`（60s×N 超时、200MB 上限），边下边算 SHA256，校验通过才 rename 为正式名；校验失败删除临时文件
- `Apply(ctx, installerPath string)`：`exec.Command(installer, "/S")` 分离启动（`SysProcAttr` 不继承句柄）→ 返回后由调用方退出应用
- 辅助：`Newer(latest, current string) bool`（去 `v` 前缀、按 `.` 拆分数字比较、不足 3 段补 0）、`sha256OfFile`

**`internal/updater/auto.go`**：

- `StartAuto(ctx, current string, onApplying func(version string))`：`ManifestURL` 为空或 `current == "dev"` 直接返回；否则启动延迟 30 秒后首次检查，之后 `time.Ticker` 每 6 小时一次；发现新版本 → 下载校验 → `onApplying` 回调（前端提示）→ 短暂延迟 → `Apply` → 进程退出
- 全部错误仅 `log.Printf` 记录；防重入：`sync.Mutex` 保证同一时刻只有一次升级流程

### 4. `app.go` — 挂接

- `OnStartup` 末尾（`go a.startAutoRefresh()` 旁）新增：

```go
go updater.StartAuto(ctx, Version, func(version string) {
    wailsruntime.EventsEmit(ctx, "update:applying", version)
})
```

- 升级流程由 `StartAuto` 在 `Apply` 后直接退出（先经 `wailsruntime.Quit` 触发 `OnShutdown` 清理托盘，另起 3 秒兜底 `os.Exit` 定时器）
- 新增绑定方法 `GetVersion() string { return Version }` 供前端展示

### 5. `build/windows/installer/project.nsi` — 支持静默覆盖升级

- `!define REQUEST_EXECUTION_LEVEL "user"`（`!include "wails_tools.nsh"` 之前定义，wails_tools 将不再覆盖默认的 admin）
- `InstallDir "$LOCALAPPDATA\${INFO_PRODUCTNAME}"`（替换第 75 行）
- Section 开头（`SetOutPath $INSTDIR` 前）加"等待旧进程退出"循环：尝试 `Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"`，失败则 `Sleep 500` 重试至多 20 次（10 秒），仍失败则 `Abort`（防止覆盖写入被占用文件导致半升级状态）
- Section 末尾（`wails.writeUninstaller` 后）加：静默模式下自动重启

```nsis
IfSilent 0 +2
    Exec "$INSTDIR\${PRODUCT_EXECUTABLE}"
```

- 说明：user 模式下 `wails.webview2runtime` 走 wails_tools.nsh 第 159 行的每用户分支；WebView2 为系统级组件，首次安装已处理，升级路径不受影响

### 6. `cmd/release/main.go` — 自动发布工具（新目录）

本地一键自动发布，`go run ./cmd/release`，流程全自动：

1. 读取 `release.env`（缺失时报错退出并提示参照 `release.env.example`；解析 `UPDATE_MANIFEST_URL / OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET / OSS_ENDPOINT / OSS_BUCKET`）
2. 读取 `wails.json` 的 `info.productVersion` 作为版本号
3. 执行 `wails build -platform windows/amd64 -nsis -ldflags "-X main.Version=<v> -X quota-viewer/internal/updater.ManifestURL=<UPDATE_MANIFEST_URL>"`
4. 对产物 `build/bin/quota-viewer-amd64-installer.exe` 计算 SHA256
5. 用 OSS 凭证（aliyun-oss-go-sdk）上传安装包到 `quota-viewer/<版本>/` 前缀；生成 `version.json`（顶层 `version/notes` + `platforms["windows/amd64"] = {url, sha256}`，url 由 `UPDATE_MANIFEST_URL` 推导安装包对象地址）并上传
6. 控制台输出发布结果（版本、清单地址、SHA256）

### 7. 配置文件

- 新增 `release.env.example`（入库，字段名 + 注释占位）
- 新增 `.gitignore` 条目 `release.env`（现有 `.gitignore` 若无则新增文件）

### 8. 前端（小改）

- `frontend/src/main.js`：设置面板页脚展示 `GetVersion()` 返回值；监听 `update:applying` 事件 → Toast"正在升级到 vX.Y.Z，应用将自动重启"
- `frontend/src/index.html`：设置面板底部加版本号容器（极小改动，复用现有样式）

### 9. 测试（按项目规范同步编写）

- `internal/updater/updater_test.go`：
  - `Newer` 表驱动测试（相等/大/小/带 v 前缀/补零段数）
  - 清单解析：`httptest.Server` 模拟 200/404/坏 JSON/超大响应
  - 下载校验：httptest 提供已知内容，SHA256 匹配/不匹配两路径；`.tmp` 文件在失败时被清理
  - `ManifestURL` 为空时 `StartAuto` 直接返回
- `cmd/release` 不写测试（一次性脚本性质）

### 10. 文档（按项目规范同步编写）

- `docs/AUTO_UPDATE.md`：升级流程（文字版）、`version.json` 清单格式、发布步骤（配置 `release.env` → `go run ./cmd/release`）、OSS bucket 公共读要求、per-user 安装迁移说明、安全模型说明（HTTPS+SHA256+凭证隔离）
- `README.md` 开发小节补一行指向该文档

## OSS 侧一次性准备（手动，非代码）

1. bucket 开启公共读（或仅对 `quota-viewer/*` 前缀授权公共读）
2. 创建专用 RAM 子账号（仅该 bucket 写权限），AK/SK 填入 `release.env`

## 假设与待确认

- **假设**：本期仅发布 `windows/amd64`（与现状一致）；清单已按多平台结构设计，未来扩展 macOS/Linux 时另行实现对应替换机制（macOS：替换 .app；Linux：原地替换二进制，Unix 不锁运行中文件，实现更简单）
- **假设**：接受 per-user 安装迁移成本（旧 Program Files 安装需重装一次）
- **假设**：不做"禁用自动更新"开关（全自动静默的既定选择）
- **假设**：发布在本地开发机执行（后续可迁移 GitHub Actions/Gitee Go，`release.env` 的字段即 Secrets 清单）

## 验证步骤

1. `go test ./internal/updater/...` 全部通过
2. 配好 `release.env` 后 `go run ./cmd/release`：构建成功、OSS 出现 `quota-viewer/1.0.0/` 安装包与 `version.json`，浏览器匿名访问清单 URL 可下载
3. 端到端：装 `1.0.0` → wails.json 改 `1.0.1` 重新发布 → 启动旧版应用，观察 30 秒内自动完成升级并重启为 `1.0.1`（无 UAC 弹窗）
4. 失败路径：断网/坏 SHA256/清单 404/`release.env` 缺失（dev 构建）时应用正常运行不报错不崩溃
5. `git status` 确认 `release.env` 未被跟踪；`go vet ./...` 无告警；前端 `npm run build` 通过
