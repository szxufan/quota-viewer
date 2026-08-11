# 悬浮球文字按用量百分比双色渲染计划

## Summary

悬浮球每个格子的平台缩写文字,由现在的"整体单一状态色"改为"按用量百分比连续分割成两段颜色":

- **已用部分**(0% ~ `percent`):现有状态色(绿/黄/红,沿用 `getStatusColor` 阈值);
- **剩余部分**(`percent` ~ 100%):默认灰色文字(`--text-3`)。

实现方式:CSS `background-clip: text` + 带百分位断点的 `linear-gradient`(用户已确认:连续分割、状态色+灰色)。

## Current State Analysis

- 球格文字渲染位于 [main.js updateBall](file:///c:/Users/xufan/Trae/quota-viewer/frontend/src/main.js#L231-L273):每个 key 一个 `.ball-cell`,`cell.textContent = r.abbr`,class 取 `getStatusColor(r)` 的 green/yellow/red。
- 颜色样式位于 [style.css](file:///c:/Users/xufan/Trae/quota-viewer/frontend/src/style.css#L72-L86):`.ball-cell` 默认 `color: var(--text-3)`(未加载灰),`.ball-cell.green/yellow/red` 整字变色。
- 用量百分比已由后端提供:`QuotaResult.Percent` = 已用百分比([types.go](file:///c:/Users/xufan/Trae/quota-viewer/internal/fetcher/types.go#L22));错误时 `Error` 非空。**后端无需改动。**

## Proposed Changes

### 1. `frontend/src/style.css` — 球格文字改渐变裁剪

替换 [L72-L86](file:///c:/Users/xufan/Trae/quota-viewer/frontend/src/style.css#L72-L86) 的 `.ball-cell` 规则:

```css
.ball-cell {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    font-size: 13px;
    font-weight: 600;
    user-select: none;
    /* 双色文字:已用部分=状态色(--used-color),剩余部分=灰色;断点由 --used 决定 */
    color: transparent;
    background-image: linear-gradient(
        90deg,
        var(--used-color, var(--text-3)) 0%,
        var(--used-color, var(--text-3)) var(--used, 0%),
        var(--text-3) var(--used, 0%),
        var(--text-3) 100%
    );
    -webkit-background-clip: text;
    background-clip: text;
}
.ball-cell.green { --used-color: var(--green); }
.ball-cell.yellow { --used-color: var(--yellow); }
.ball-cell.red { --used-color: var(--red); }
```

说明:

- 删除原 `color: var(--text-3)`、`transition: color 0.3s ease`(渐变无法过渡,保留无意义)及三个 `.ball-cell.* { color: ... }`。
- 每个格子的 `--used`(百分比)和 `--used-color`(状态色)由 JS 内联/类名设置;未设时退化全灰。
- WebView2 基于 Chromium,`background-clip: text` 完全支持,无需降级。

### 2. `frontend/src/main.js` — updateBall 设置断点百分比

在 [updateBall](file:///c:/Users/xufan/Trae/quota-viewer/frontend/src/main.js#L231-L240) 的格子创建处修改:

```js
results.forEach((r) => {
    const cell = document.createElement("span");
    cell.className = "ball-cell " + getStatusColor(r);
    // 双色断点:错误时全红;否则按已用百分比切分(0 = 全灰)
    cell.style.setProperty("--used", (r.error ? 100 : r.percent || 0) + "%");
    cell.textContent = r.abbr || r.platform.slice(0, 1);
    ball.appendChild(cell);
});
```

其余(网格排布、tooltip、`SetBallSize`)不变。

## Assumptions & Decisions

1. 已用部分 = 现有状态色(阈值沿用 `getStatusColor`:≥90 红、≥75 黄、否则绿,余额型按预算消耗百分比);剩余部分 = `--text-3` 灰。
2. 连续分割,可切在字符中间(如单字 "K" 也能呈现左右两色)。
3. 边界:`percent = 0`(未设预算的余额型、无用量)→ 整字灰;`percent = 100` → 整字状态色;错误(Error 非空)→ 整字红。
4. 后端零改动:`percent` 已随 `quota:update` 事件下发。

## Verification

1. 前端语法检查:`npx vite build`(frontend 目录)通过。
2. `wails dev` 手动验证:

   - 单 key 球格:用量 30% → 左 30% 绿、右 70% 灰;
   - 用量 85% → 左 85% 黄;
   - 用量 ≥90% → 基本全红;
   - 余额型未设预算(percent=0)→ 整字灰;
   - 渠道失败(Error)→ 整字红;
   - 多 key 网格(>3 个):每个格子按各自百分比独立显示;
   - 悬停 tooltip、拖拽、展开面板均不受影响。
