# 支持设置界面透明度

## 摘要

在设置面板新增"界面透明度"滑块（20%–100%），实时预览、立即持久化。透明度持久化到 config.json 新增字段 `opacity`，启动时自动应用。

## 现状分析

* 应用为 Wails v2.12.0 无边框透明窗口（`main.go` 中 `BackgroundColour: rgba(0,0,0,0)`），UI 由前端 WebView2 渲染，窗口自身背景完全透明。

* Wails v2.12.0 运行时**无** `WindowSetOpacity` API（已确认源码），Windows 原生 `SetLayeredWindowAttributes` 与当前透明窗口使用的 `WS_EX_NOREDIRECTIONBITMAP`（Win11 DWM 路径）存在兼容风险。

* 前端 `body` 背景透明，可见内容全部由 WebView2 渲染 —— 直接在 `body` 上设置 CSS `opacity` 即可实现窗口级透明效果，简单可靠、跨平台，且不影响现有窗口定位/尺寸逻辑（透明度不改变窗口几何）。

* 配置模型（`internal/config/config.go`）：`Config` 含 `Providers/RefreshIntervalMin/BallX/BallY`，`Load()` 有"确保默认值"段落；`app.go` 的 `GetConfig()` 向前端返回配置快照。

* 设置面板（`frontend/src/index.html` 的 `#settings`）已有刷新间隔表单组；`main.js` 的 `loadConfig()` 负责加载配置、滑块 UI 事件可仿照现有 input 模式。

## 变更方案

### 1. `internal/config/config.go` — 新增配置字段

* `Config` 增加字段：

  ```go
  Opacity float64 `json:"opacity"` // 界面透明度 0.2-1.0,1.0 = 不透明
  ```

* `Default()` 中设 `Opacity: 1.0`。

* `Load()` 的"确保默认值"段落追加钳制（旧配置文件无此字段 → 零值 0 → 回退 1.0；越界同样回退）：

  ```go
  if cfg.Opacity <= 0 || cfg.Opacity > 1 {
      cfg.Opacity = 1
  }
  ```

### 2. `app.go` — 配置读取 + 设置接口

* `GetConfig()` 返回值增加 `"opacity": a.cfg.Opacity`。

* 新增方法（前端滑块松开时调用，即时持久化）：

  ```go
  // SetOpacity 保存界面透明度(0.2-1.0),越界钳制。
  func (a *App) SetOpacity(opacity float64) error {
      a.mu.Lock()
      defer a.mu.Unlock()
      if opacity < 0.2 { opacity = 0.2 }
      if opacity > 1 { opacity = 1 }
      a.cfg.Opacity = opacity
      return config.Save(a.cfg)
  }
  ```

### 3. `frontend/src/index.html` — 设置面板加滑块

在"刷新间隔" form-group 之后插入：

```html
<div class="form-group">
    <label for="input-opacity">界面透明度</label>
    <div class="opacity-row">
        <input type="range" id="input-opacity" min="0.2" max="1" step="0.05" value="1">
        <span id="opacity-value" class="opacity-value">100%</span>
    </div>
</div>
```

### 4. `frontend/src/main.js` — 应用与交互

* 新增 `applyOpacity(v)`：`document.body.style.opacity = v` + 更新 `#opacity-value` 文本（`Math.round(v*100) + "%"`）。

* `loadConfig()` 中：`applyOpacity(cfg.opacity ?? 1)` 并同步滑块值（`#input-opacity.value`）。

* 滑块事件：

  * `input`：仅 `applyOpacity` 实时预览（不写磁盘）。

  * `change`（松开）：`applyOpacity` + `window.go.main.App.SetOpacity(v)`，失败时 `toast` 提示。

* 启动时应用已保存的透明度：文件底部启动逻辑处，调用一次 `GetConfig()`（只读、无副作用），取 `opacity` 执行 `applyOpacity`，使应用重启后立即生效。

### 5. `frontend/src/style.css` — 滑块样式

* `.opacity-row`：flex 布局，滑块占满剩余宽度，百分比文本固定宽度右对齐。

* `input[type="range"]` 简单适配现有暗色主题（accent-color: var(--accent)）。

### 6. `internal/config/config_test.go` — 测试

* `Default()` 返回 `Opacity == 1.0` 断言（并入现有默认值测试）。

* 新增用例：配置文件缺 `opacity` 字段（旧配置）→ 加载后为 1.0；写入越界值（如 2 / -1 / 0）→ 钳制为 1.0。

## 假设与决策

* **实现方式选 CSS opacity 而非 Windows API**：Wails v2.12.0 无透明度 API，原生 `SetLayeredWindowAttributes` 需给窗口追加 `WS_EX_LAYERED`，与现有 Win11 DWM 透明路径（`WS_EX_NOREDIRECTIONBITMAP`）组合存在渲染异常风险；CSS 方式对当前"透明背景 + WebView2 渲染"架构效果等同且零风险。

* **透明度全局生效**（球、面板、设置面板统一），不做"设置面板豁免"，保持简单一致；下限 20% 保证任何状态下滑块仍可读、可调回。

* **即时生效即时保存**，不并入现有"保存"按钮流程：滑块 `input` 实时预览，`change` 时持久化，符合滑块直觉。

* 旧配置无 `opacity` 字段时回退为 1.0（不透明），行为不变。

## 验证步骤

1. `go build ./...` 与 `go test ./...`（含新增 Opacity 测试）通过。
2. 前端 `npm run build` 无报错。
3. `wails dev` 运行验证：

   * 设置面板可见"界面透明度"滑块，默认 100%。

   * 拖动滑块实时看到整个窗口（含悬浮球）透明度变化，松开后关闭设置面板再打开，值保持。

   * 重启应用后透明度仍生效。

   * 手动编辑 config.json 删除 `opacity` 字段或填入越界值，重启后恢复为不透明。

