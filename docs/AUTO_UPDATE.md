# 自动升级与发布指南

本文描述 quota-viewer 的自动升级机制与发布流程。相关代码：

- `internal/updater/` — 客户端升级逻辑（检查/下载/校验/静默安装）
- `cmd/release/` — 发布工具（构建/上传 OSS/更新清单）
- `build/windows/installer/project.nsi` — 安装器（每用户安装，支持静默覆盖升级）

## 升级流程

```
应用启动 → 延迟 30 秒 → 拉取 version.json 清单
  ├─ ManifestURL 为空或 dev 构建 → 跳过(不检查)
  ├─ 网络失败/清单异常 → 记日志,静默等下轮
  └─ 清单版本 > 当前版本?
       ├─ 否 → 每 6 小时后再查
       └─ 是 → 下载安装包(实时 SHA256 校验)
            ├─ 校验失败 → 删除,等下轮
            └─ 通过 → Toast 提示 → 退出应用 →
                 安装器 /S 静默覆盖安装 → 自动重启新版
```

- 触发时机：启动后 30 秒首次检查，之后每 6 小时轮询。
- 安装方式：NSIS `/S` 静默安装到 `%LOCALAPPDATA%\Quota Viewer`（每用户，无 UAC）。
- 安装器先等待旧进程退出（至多 10 秒），完成后自动重启应用。
- 失败全部静默记日志，绝不影响正常使用。

## 版本清单格式（version.json）

托管在 OSS（bucket 公共读），固定路径 `quota-viewer/version.json`：

```json
{
  "version": "1.0.1",
  "notes": "可选更新说明",
  "platforms": {
    "windows/amd64": {
      "url": "https://<bucket>.<endpoint>/quota-viewer/1.0.1/quota-viewer-amd64-installer.exe",
      "sha256": "<安装包十六进制摘要>"
    }
  }
}
```

- 平台键为 `GOOS/GOARCH`，客户端按自身平台取条目；无对应条目则跳过（预留多平台扩展）。
- 版本比较为语义化数字比较（容忍 `v` 前缀、补零段），仅当清单版本**严格大于**当前版本才升级。

## 版本号管理

单一来源：`wails.json` 的 `info.productVersion`。

- `wails build` 用它填充 exe 文件属性与 NSIS 安装包版本；
- 发布时经 `-ldflags "-X main.Version=<v>"` 注入 Go 运行时常量；
- 前端设置面板页脚展示 `GetVersion()` 返回值。

发布新版本 = 改 `wails.json` 里的 `productVersion` → 执行发布命令。

## 发布流程

### 一次性准备

1. 阿里云 OSS 创建 bucket（升级产物用），对 `quota-viewer/*` 前缀开公共读。
2. 创建 RAM 子账号，仅授予该 bucket 写权限，拿到 AK/SK。
3. 复制 `release.env.example` 为 `release.env`，填写清单 URL 与 OSS 凭证：

```ini
UPDATE_MANIFEST_URL=https://<bucket>.<endpoint>/quota-viewer/version.json
OSS_ACCESS_KEY_ID=...
OSS_ACCESS_KEY_SECRET=...
OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
OSS_BUCKET=<bucket名>
```

### 发布新版本

```powershell
# 1. 修改 wails.json 的 info.productVersion(如 1.0.1)
# 2. 一键发布(构建 + 上传 + 更新清单)
go run ./cmd/release
```

脚本自动完成：读取 `release.env` → 读取版本号 → `wails build -nsis`（ldflags 注入版本与清单地址）→ 计算安装包 SHA256 → 上传安装包到 `quota-viewer/<版本>/` → 生成并上传 `version.json`。

已安装的旧版客户端最迟 6 小时内自动升级到新版。

## 安全模型

- **源码零硬编码**：清单地址与 OSS 凭证只在 `release.env`（已 gitignore），构建时经 ldflags 注入，源码库与 git 历史不含任何敏感值。
- **凭证隔离**：OSS 写凭证只存在于发布机；客户端 exe 只含公开的 HTTPS 清单地址（抓包可见，非秘密）。
- **完整性校验**：安装包下载时实时计算 SHA256 与清单比对，不匹配即丢弃；传输全程 HTTPS。
- 若未来需要防"OSS 凭证泄露导致恶意升级"，可升级为 ed25519 签名清单（客户端内置公钥），当前个人工具威胁模型下 HTTPS+SHA256 已足够。

## 测试与验证

```powershell
go test ./internal/updater/...   # 单元测试(版本比较/清单解析/下载校验)
go vet ./...
wails build -nsis                # 确认安装包可正常构建
```

端到端验证：装 1.0.0 → 改版本号重新发布 → 启动旧版，约 30 秒后应自动完成升级并重启为新版（无 UAC 弹窗）。

## 已知迁移说明

安装目录从 `Program Files` 改为 `%LOCALAPPDATA%\Quota Viewer`（每用户安装，自动升级必需——admin 安装的 UAC 弹窗无法静默）。旧版用户首次需手动安装新版一次，之后即可全自动升级。
