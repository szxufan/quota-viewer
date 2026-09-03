# 窗口定位与 DPI

**When to read**: 修改悬浮球/面板定位、多显示器、DPI 缩放相关逻辑时。本项目最复杂的领域，改动前必读。

---

## 核心内容

### 三套坐标系（Windows 混乱之源）

| 坐标系 | 来源 | 用途 |
|---|---|---|
| 虚拟桌面绝对坐标（物理像素） | `WindowGetPosition` / `BallX/BallY` 配置 | 记录悬浮球位置 |
| 显示器工作区相对坐标 | `WindowSetPosition` 期望 | 移动窗口 |
| 逻辑像素 | `Screen.Size`（Wails） | 前端尺寸概念 |

**铁律**：`WindowGetPosition`（绝对）≠ `WindowSetPosition`（相对工作区）。多显示器 + 非零工作区原点（任务栏在左/上）时直接互传会错位——这就是 2026-07-25 fix/window-position 修的问题。

### 关键函数

```
fitToScreen(ctx, ballX, ballY, w, h) → (x, y, ok)   [app.go:242]
  ① Windows 精确路径: workAreaForPoint(ballX, ballY) → 物理像素工作区 + DPI
     逻辑尺寸 w/h × dpi/96 → 物理像素 → anchoredPos 定位 → 减工作区原点 (ax-wx, ay-wy)
  ② 回退路径（非 Windows/查询失败）: ScreenGetAll 逻辑像素近似（球心越界则放弃钳制）

anchoredPos(ballX, ballY, w, h, ballPhys, rx, ry, rw, rh, margin) → (x, y)  [app.go:289]
  默认从球位置向右下展开; 放不下则翻转（球右缘对齐面板右缘/上缘对齐）
  最后整体钳制在 (rx,ry,rw,rh) 内并保留 margin

workAreaForPoint(px, py) → (x, y, w, h, dpi, ok)   [workarea_windows.go:106]
  MonitorFromPoint + GetMonitorInfoW(rcWork) + GetDpiForMonitor(mdtEffectiveDPI)
```

### Win32 细节

- **POINT 打包**：64 位下 x/y 打包进一个 uintptr（低 32 位 x，高 32 位 y）传给 `MonitorFromPoint`
- **子类化**：`SetWindowSubclass` 覆盖 `WM_GETMINMAXINFO`，把 overlapped 窗口系统默认最小宽度（高 DPI 下约 262px 物理）压到 `ballSize×dpi/96`，否则 60px 球窗被撑宽
- **工具窗口**：`WS_EX_TOOLWINDOW` 去任务栏/Alt+Tab/缩略图（注意：曾因需求变化 add 又 Revert，见 git 历史 6fcdb28/a6b5e65）
- `subclassCB` 必须包在包级变量防止 GC 回收回调
- 样式切换后需重申 `AlwaysOnTop`

### 常量

| 常量 | 值 | 位置 |
|---|---|---|
| `ballSize` | 60（包级变量，运行时随渠道数变化） | app.go（与前端 SIZES.ball 一致） |
| `ballSizeMin` | 60 | app.go |
| `screenMargin` | 8 | app.go（面板与屏幕边缘间距） |
| `monitorDefaultToNearest` | 2 | workarea_windows.go |
| `mdtEffectiveDPI` | 0 | workarea_windows.go |

### 动态球尺寸

- 球窗口尺寸不再固定：前端渲染完球网格后调用 `App.SetBallSize(size)`（[app.go]），后端更新包级 `ballSize`、`WindowSetMinSize` + `WindowSetSize`，再 `fitToScreen` 校正位置
- `WM_GETMINMAXINFO` 子类回调读取同一包级 `ballSize`，最小拖动尺寸始终跟随球尺寸
- 网格规则（纯函数 `ballGridFor`，settings-helpers.js）：1-3 单行 60×60；4 个 2×2；≥5 按 `ceil(sqrt(n))` 方形扩展，边长 = `max(60, cols*22)`
- **尺寸时机**：数据可能在面板/设置展开期间到来（自动刷新/订阅更新），此时 `SetBallSize` 会破坏展开态——前端只在收起态调用它，展开期间记入 `pendingBallSize`，`setView("ball")` 收起时经 `syncBallSizeOnCollapse` 补调。遗漏该补调会导致"收起后球尺寸与数据不符"（2026-09-03 修复）
- 展开面板高度由前端按内容自适应：`resizePanel()` 读 DOM `offsetHeight` 后调 `ExpandWindow(340, h)`

### 已知坑

- `WindowSetMinSize` 设置的是逻辑像素；实际生效还要靠子类覆盖物理像素
- `Screen.GetAll` 在某些多屏/DPI 混合场景返回不可靠 → 必须优先 workAreaForPoint 物理像素路径
- 展开面板尺寸 (w,h) 由前端传入（逻辑像素），fitToScreen 内部自行换算物理像素

---

## 关键文件

| 文件 | 职责 |
|---|---|
| `app.go` | fitToScreen / anchoredPos / ExpandWindow / CollapseWindow / savedX-savedY 状态 |
| `workarea_windows.go` | Win32 查询与窗口样式/子类 |
| `workarea_other.go` | 非 Windows 桩（无实际实现） |

---

## Must NOT Change

- `ballSize`（包级变量）与前端 `SIZES.ball` 必须一致;`SetBallSize` 更新时必须同步 `WindowSetMinSize`
- fitToScreen 的返回值语义：**工作区相对坐标**（调用方直接传 WindowSetPosition）
- 窗口状态机：`expanded` 标志 + `savedX/savedY`（展开前球位）——收起必须还原球位
