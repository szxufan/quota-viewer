# 开机自启动功能实现方案

## 实现方式
Windows 上写入当前用户的注册表自启动键 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`（值名 `quota-viewer`，值为当前 exe 的带引号完整路径）。无需管理员权限。开关放在设置界面「通用」Tab，勾选后即时生效（不走底部「保存」按钮，与透明度滑块交互模式一致）。非 Windows 平台返回"不支持"错误，开关勾选失败会自动回滚并提示。

## 改动清单

### 1. 新建 `internal/autostart` 包（沿用项目 `//go:build windows` / `!windows` 分文件模式）
- `autostart_windows.go`：
  - `Enable() error` — 用已有的 `golang.org/x/sys/windows/registry` 依赖打开/创建 Run 键，`SET_VALUE` 写入 `"C:\...\quota-viewer.exe"`（路径来自 `os.Executable()`，带引号防含空格）
  - `Disable() error` — 删除该注册表值，值不存在视为成功
- `autostart_other.go`：同名函数桩实现，返回"当前平台不支持开机自启动"错误

### 2. `internal/config/config.go`
- `Config` 结构体新增 `AutoStart bool` 字段（`json:"auto_start"`），bool 零值安全，无需 Load 兜底

### 3. `app.go`
- 新增绑定方法 `SetAutoStart(enabled bool) error`：先执行注册表操作（失败则不动配置），成功后按现有 `SetOpacity` 模式加锁更新 `a.cfg` 并 `config.Save`
- `GetConfig()` 返回的 map 增加 `"auto_start"` 字段供前端回显
- `OnStartup` 增加自愈逻辑：若配置了自启动，启动时 best-effort 重新 `Enable()` 一次（幂等，顺带修正自动更新升级后 exe 路径变化的问题）

### 4. 前端
- `frontend/src/index.html` 「通用」Tab（刷新间隔、透明度之后）新增开关行，复用现有 `.field-option` checkbox 样式（label + checkbox + 灰色说明"登录 Windows 后自动启动"）
- `frontend/src/main.js`：
  - `loadConfig()` 中回显 `#input-autostart` 勾选状态
  - change 事件即时调用 `SetAutoStart`，成功 toast"已开启/已关闭开机自启动"，失败回滚勾选状态并 toast 错误

### 5. 依赖与构建
- `go mod tidy`（`golang.org/x/sys` 已在 go.sum，tidy 转为直接依赖，无需联网下载）
- `frontend/wailsjs/` 绑定由 `wails dev` / `wails build` 自动重新生成，无需手改

## 验证
- `go build ./...`、`go vet ./...` 通过
- 手工验证：勾选后 `reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v quota-viewer` 确认写入；取消勾选确认删除；重启应用后开关状态回显正确