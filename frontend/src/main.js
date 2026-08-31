// === Wails 绑定 ===
// window.go.main.App 在运行时由 Wails 注入

import { credTabLabel, groupHasData, parseOptionValues, providerBadgeText } from "./settings-helpers.js";

// 各视图窗口尺寸,与 Go 侧 ballSize 常量保持一致
const SIZES = {
    ball: [60, 60],
    panel: [340, 310], // 宽度为基准值:多凭证时每多一列 +50% 加宽(见 resizePanel)
    settings: [720, 620], // 固定尺寸(账号页左右两栏),内容超出时内部滚动
};

let currentView = "ball"; // ball | panel | settings
let currentResults = [];
let panelMaxKeys = 1; // 当前结果中单渠道最大凭证数,用于计算面板宽度
// [{id, def, navItem, pane, enabled, badge, navBadge, tabs, pages,
//   groups: [{fields: [{key, input}], nameInput, budget, page}], active}]
let providerCards = [];
let selectedProviderId = null; // 账号页左栏当前选中项(重渲染后尽量保持)

// === 视图切换(统一入口,负责窗口尺寸与屏幕内定位) ===
function setView(view) {
    const prevView = currentView;
    currentView = view;
    document.getElementById("ball").classList.toggle("hidden", view !== "ball");
    document.getElementById("panel").classList.toggle("hidden", view !== "panel");
    document.getElementById("settings").classList.toggle("hidden", view !== "settings");

    // 设置界面模式:进入时临时不透明,离开时恢复透明度
    if (view === "settings" && prevView !== "settings") {
        window.go.main.App.SetSettingsMode(true).catch(() => {});
    } else if (view !== "settings" && prevView === "settings") {
        window.go.main.App.SetSettingsMode(false).catch(() => {});
    }

    if (view === "ball") {
        window.go.main.App.CollapseWindow();
    } else if (view === "settings") {
        const [w, h] = SIZES.settings;
        window.go.main.App.ExpandWindow(w, h);
    } else {
        // panel:用缓存数据重建 DOM,再按内容自适应高度
        if (currentResults.length) renderResults(currentResults);
        resizePanel();
    }
}

// === panel 按内容高度自适应 ===
async function resizePanel() {
    const panel = document.getElementById("panel");
    const list = document.getElementById("quota-list");
    // 宽度 = 基准宽度 × (1 + (凭证数-1) × 50%):每多一列凭证加宽半个基准
    const w = Math.round(SIZES.panel[0] * (1 + (panelMaxKeys - 1) * 0.5));
    // 先按目标宽度重排内容,再测量自然高度(避免窄窗口下量出偏高的值)
    panel.style.width = w + "px";
    // 先取消滚动限制,测量内容自然高度
    list.style.maxHeight = "";
    const natural = panel.offsetHeight;
    await window.go.main.App.ExpandWindow(w, Math.max(SIZES.panel[1], natural));
    // 等待窗口尺寸生效(Go 可能已按屏幕工作区钳制高度),
    // 再按实际窗口高度给内容区设滚动上限,超高时列表内部滚动
    setTimeout(() => {
        const winH = window.innerHeight || natural;
        const headerH = panel.querySelector(".panel-header").offsetHeight;
        const footerH = panel.querySelector(".panel-footer").offsetHeight;
        list.style.maxHeight = Math.max(0, winH - headerH - footerH) + "px";
    }, 30);
}

// === 事件监听 ===
window.runtime.EventsOn("quota:update", (results) => {
    currentResults = results;
    renderResults(results);
});

// === 球面点击:展开/收起 ===
document.getElementById("ball").addEventListener("click", () => {
    if (currentView !== "ball") return;
    setView("panel");
    refreshIfNeeded(); // 展开时若数据超过 3 分钟则刷新
});

// 收起按钮
document.getElementById("btn-collapse").addEventListener("click", () => {
    setView("ball");
});

// === 刷新 ===
document.getElementById("btn-refresh").addEventListener("click", () => {
    refreshQuota();
});

async function refreshQuota() {
    const btn = document.getElementById("btn-refresh");
    btn.disabled = true;
    btn.classList.add("spinning");
    document.getElementById("last-updated").textContent = "刷新中...";
    try {
        const results = await window.go.main.App.Refresh();
        currentResults = results;
        renderResults(results);
    } catch (e) {
        console.error("refresh error:", e);
        toast("刷新失败: " + e, "error");
    } finally {
        btn.disabled = false;
        btn.classList.remove("spinning");
    }
}

let lastRefreshTime = 0;
async function refreshIfNeeded() {
    if (Date.now() - lastRefreshTime > 3 * 60 * 1000) {
        await refreshQuota();
    }
}

// === 渲染结果 ===
function renderResults(results) {
    // 更新详情面板
    const list = document.getElementById("quota-list");
    list.innerHTML = "";

    // 按 provider id 分组,组内按 key_index 排序
    const groups = new Map();
    results.forEach((r) => {
        if (!groups.has(r.id)) groups.set(r.id, []);
        groups.get(r.id).push(r);
    });

    let idx = 0;
    panelMaxKeys = 1;
    groups.forEach((items) => {
        panelMaxKeys = Math.max(panelMaxKeys, items.length);
        items.sort((a, b) => a.key_index - b.key_index);
        const color = worstColor(items);
        const item = document.createElement("div");
        item.className = "quota-item" + (items[0].error ? " error" : "");
        item.style.animationDelay = idx++ * 45 + "ms";

        // 组头:平台名 + (多 key 时)数量
        const head = document.createElement("div");
        head.className = "quota-item-header";
        const platform = document.createElement("span");
        platform.className = "quota-platform";
        platform.innerHTML = `<i class="status-dot ${color}"></i>${items[0].platform}`;
        head.appendChild(platform);
        if (items.length > 1) {
            const count = document.createElement("span");
            count.className = "quota-key-count";
            count.textContent = items.length + " 个 Key";
            head.appendChild(count);
        }
        item.appendChild(head);

        // key 单元格:横向分割
        const cells = document.createElement("div");
        cells.className = "quota-key-cells";
        items.forEach((r, ki) => {
            const c = getStatusColor(r);
            const percent = r.error ? 0 : r.percent || 0;
            const resetHtml = (r.reset_at && !r.error)
                ? `<div class="quota-reset" data-reset-at="${r.reset_at}">${formatCountdown(r.reset_at)}</div>`
                : "";
            // 优先显示配置的凭证名,否则多 key 时回退 "Key N"(单 key 留空)
            const keyLabel = r.key_name || (items.length > 1 ? "Key " + (ki + 1) : "");
            const cell = document.createElement("div");
            cell.className = "quota-key-cell";
            cell.innerHTML = `
                <div class="quota-key-head">
                    <span class="quota-key-label">${keyLabel}</span>
                    <span class="quota-remaining ${r.error ? "error-text" : ""}">${r.error || r.remaining || "-"}</span>
                </div>
                <div class="progress-bar">
                    <div class="progress-fill ${c}" style="width: ${r.error ? 100 : percent}%"></div>
                </div>
                ${resetHtml}
            `;
            cells.appendChild(cell);
        });
        item.appendChild(cells);
        list.appendChild(item);
    });

    // 更新球面格子
    updateBall(results);

    // 更新时间
    const now = new Date();
    lastRefreshTime = now.getTime();
    document.getElementById("last-updated").textContent = "更新于 " + now.toLocaleTimeString("zh-CN");

    // 面板可见时按内容自适应高度
    if (currentView === "panel") resizePanel();
}

// 组状态色:取组内最差状态(red > yellow > green)
function worstColor(items) {
    let yellow = false;
    for (const r of items) {
        const c = getStatusColor(r);
        if (c === "red") return "red";
        if (c === "yellow") yellow = true;
    }
    return yellow ? "yellow" : "green";
}

// === 倒计时 ===
function formatCountdown(isoStr) {
    const target = new Date(isoStr);
    if (isNaN(target.getTime())) return "";
    const diff = target - Date.now();
    if (diff <= 0) return "已过期";
    const days = Math.floor(diff / 86400000);
    const hours = Math.floor((diff % 86400000) / 3600000);
    const mins = Math.floor((diff % 3600000) / 60000);
    if (days > 0) return `距下次刷新: ${days}天${hours}时${mins}分`;
    if (hours > 0) return `距下次刷新: ${hours}时${mins}分`;
    return `距下次刷新: ${mins}分`;
}

function updateCountdowns() {
    document.querySelectorAll(".quota-reset").forEach((el) => {
        const resetAt = el.getAttribute("data-reset-at");
        const text = formatCountdown(resetAt);
        if (text) el.textContent = text;
    });
}

setInterval(updateCountdowns, 30000);

function getStatusColor(r) {
    if (r.error) return "red";
    // 余额型(如 DeepSeek):设了预算后按消耗百分比走颜色,未设预算恒绿
    if (r.kind === "balance" && r.percent > 0) {
        if (r.percent >= 90) return "red";
        if (r.percent >= 75) return "yellow";
        return "green";
    }
    if (r.kind === "balance") return "green";
    if (r.percent >= 90) return "red";
    if (r.percent >= 75) return "yellow";
    return "green";
}

// 球面格子 = 每个 key 一个格子(颜色=状态);悬停 tooltip 显示各 key 明细。
// 网格规则:1-3 单行 60×60;4 个 2×2;≥5 按 ceil(sqrt(n)) 方形扩展(边长 = max(60, cols*22))。
function updateBall(results) {
    const ball = document.getElementById("ball");
    ball.querySelectorAll(".ball-cell").forEach((c) => c.remove());

    results.forEach((r) => {
        const cell = document.createElement("span");
        cell.className = "ball-cell " + getStatusColor(r);
        // 文字包一层 span:双色渐变作用于字形宽度,比例才准确
        const label = document.createElement("span");
        label.className = "ball-cell-text";
        // 双色断点:错误时全红;否则按已用百分比切分(0 = 全灰)
        label.style.setProperty("--used", (r.error ? 100 : r.percent || 0) + "%");
        label.textContent = r.abbr || r.platform.slice(0, 1);
        cell.appendChild(label);
        ball.appendChild(cell);
    });

    const n = results.length;
    let cols = n, size = 60;
    if (n > 4) {
        cols = Math.ceil(Math.sqrt(n));
        size = Math.max(60, cols * 22);
    } else if (n === 4) {
        cols = 2;
    }
    const rows = Math.ceil(n / cols);
    const grid = n > 3;
    ball.classList.toggle("grid", grid);
    ball.classList.toggle("single-cell", n === 1);
    ball.style.setProperty("--cols", cols);
    ball.style.setProperty("--rows", rows);

    // 网格模式标记首行/首列,去掉对应分隔线
    if (grid) {
        Array.from(ball.querySelectorAll(".ball-cell")).forEach((cell, ci) => {
            cell.classList.toggle("first-col", ci % cols === 0);
            cell.classList.toggle("first-row", ci < cols);
        });
    }

    ball.title = results
        .map((r) => r.platform + (r.key_name || (r.key_index > 0 ? " Key " + (r.key_index + 1) : "")) + ": " + (r.error || r.remaining || "未知"))
        .join("\n");

    // 仅在收起态调整窗口尺寸,避免覆盖展开面板
    if (currentView === "ball") {
        window.go.main.App.SetBallSize(size);
    }
}

// === 配置面板 ===
document.getElementById("btn-settings").addEventListener("click", () => {
    setView("settings");
    loadConfig();
});

document.getElementById("btn-close-settings").addEventListener("click", () => {
    setView("ball");
});

// === settings 分 Tab 切换(账号 / 通用) ===
function switchSettingsTab(tab) {
    document.querySelectorAll(".settings-tab").forEach((b) =>
        b.classList.toggle("active", b.dataset.tab === tab));
    document.querySelectorAll(".settings-tab-pane").forEach((p) =>
        p.classList.toggle("active", p.id === "tab-" + tab));
}
document.querySelectorAll(".settings-tab").forEach((btn) => {
    btn.addEventListener("click", () => switchSettingsTab(btn.dataset.tab));
});

// applyOpacity 同步透明度 UI 显示(窗口级透明度由 Go 侧 setWindowOpacity 控制,
// 前端不再用 CSS opacity,避免与窗口 alpha 双重叠加)
function applyOpacity(v) {
    document.getElementById("opacity-value").textContent = Math.round(v * 100) + "%";
}

// 透明度滑块:input 实时预览(Go 直接改窗口 alpha),change(松开)持久化
// 注意:预览时临时退出设置模式,让整个窗口预览透明度效果
document.getElementById("input-opacity").addEventListener("input", (e) => {
    const v = parseFloat(e.target.value);
    applyOpacity(v);
    // 临时退出设置模式,让整个窗口预览透明度效果
    window.go.main.App.SetSettingsMode(false).catch(() => {});
    window.go.main.App.SetOpacityPreview(v).catch((err) => {
        console.error("setOpacityPreview error:", err);
    });
});
document.getElementById("input-opacity").addEventListener("change", async (e) => {
    const v = parseFloat(e.target.value);
    applyOpacity(v);
    try {
        await window.go.main.App.SetOpacity(v);
        // 保存后重新进入设置模式,让设置界面保持不透明
        window.go.main.App.SetSettingsMode(true).catch(() => {});
    } catch (err) {
        console.error("setOpacity error:", err);
        toast("保存透明度失败: " + err, "error");
    }
});

async function loadConfig() {
    try {
        const cfg = await window.go.main.App.GetConfig();
        renderProviderList(cfg.providers || []);
        document.getElementById("input-interval").value = cfg.refresh_interval_min || 15;
        // 透明度:同步滑块显示(窗口 alpha 已由 Go OnStartup 应用)
        const opacity = typeof cfg.opacity === "number" ? cfg.opacity : 1;
        document.getElementById("input-opacity").value = opacity;
        applyOpacity(opacity);
    } catch (e) {
        console.error("loadConfig error:", e);
        toast("加载配置失败: " + e, "error");
    }
}

// 收集当前 UI 上的 Provider 状态(保存/测试共用)
// keys = 凭证组数组(每组一套字段值);输入框为空时提交 placeholder(掩码值),
// 后端据此还原旧值,未修改的组自然保留
function collectProviders() {
    return providerCards.map((c) => {
        // 过滤全空组(无值且无掩码占位):提交无意义,且避免 keys/keyNames 错位
        const groups = c.groups.filter((g) =>
            g.fields.some((f) => f.input.value || f.input.placeholder)
        );
        const keys = groups.map((g) => {
            const creds = {};
            g.fields.forEach((f) => {
                creds[f.key] = f.input.value || f.input.placeholder || "";
            });
            return creds;
        });
        const keyNames = groups.map((g) => (g.nameInput ? g.nameInput.value.trim() : ""));
        // 每个凭证组的预算与 keys 对齐(0 = 未设);旧渠道级单一预算已废弃
        const budgets = groups.map((g) => (g.budget ? parseFloat(g.budget.value) || 0 : 0));
        return { id: c.id, enabled: c.enabled.checked, keys, keyNames, budgets };
    });
}

// 渲染 Provider 列表(左右两栏:左栏导航选择,右栏详情编辑;详情内多凭证横向标签切换)
function renderProviderList(providers) {
    const nav = document.getElementById("provider-nav");
    const detail = document.getElementById("provider-detail");
    nav.innerHTML = "";
    detail.innerHTML = "";
    providerCards = [];

    providers.forEach((p) => {
        // --- 左栏导航项:勾选框 + 名称 + 状态徽标 ---
        const navItem = document.createElement("div");
        navItem.className = "provider-nav-item" + (p.enabled ? "" : " disabled");
        navItem.dataset.id = p.id;

        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.className = "provider-check";
        cb.checked = !!p.enabled;

        const textWrap = document.createElement("div");
        textWrap.className = "provider-nav-text";
        const name = document.createElement("div");
        name.className = "provider-name";
        name.textContent = p.name;
        const navBadge = document.createElement("div");
        navBadge.className = "provider-nav-badge";
        textWrap.append(name, navBadge);

        navItem.append(cb, textWrap);
        nav.appendChild(navItem);

        // --- 右栏详情面板:标题行 + 凭证标签条 + 凭证页 + 动作按钮 ---
        const pane = document.createElement("div");
        pane.className = "provider-detail-pane";
        pane.dataset.id = p.id;

        const head = document.createElement("div");
        head.className = "provider-detail-head";
        const title = document.createElement("span");
        title.className = "provider-name";
        title.textContent = p.name;
        const badge = document.createElement("span");
        badge.className = "provider-badge";
        head.append(title, badge);
        pane.appendChild(head);

        const tabs = document.createElement("div");
        tabs.className = "cred-tabs";
        const pages = document.createElement("div");
        pages.className = "cred-pages";
        pane.append(tabs, pages);

        const cardObj = { id: p.id, def: p, navItem, pane, enabled: cb, badge, navBadge, tabs, pages, groups: [], active: 0 };

        // 凭证页:已有 keys 逐组渲染,否则默认一组空表单
        const savedKeys = (p.keys && p.keys.length) ? p.keys : [{}];
        const budgets = p.budgets || [];
        savedKeys.forEach((k, i) => {
            cardObj.groups.push(renderCredGroup(cardObj, k, (p.key_names || [])[i] || "", budgets[i] || 0));
        });
        refreshCredTabs(cardObj);

        // 动作按钮:测试 / 打开登录页
        const actions = document.createElement("div");
        actions.className = "provider-actions";
        const testBtn = document.createElement("button");
        testBtn.className = "btn-sm";
        testBtn.dataset.test = p.id;
        testBtn.textContent = "测试";
        actions.appendChild(testBtn);
        if (p.login_url) {
            const loginBtn = document.createElement("button");
            loginBtn.className = "btn-sm";
            loginBtn.dataset.open = p.login_url;
            loginBtn.textContent = "打开登录页";
            actions.appendChild(loginBtn);
        }
        pane.appendChild(actions);
        detail.appendChild(pane);

        // 左栏点击 = 选中查看;勾选框另行处理启用/停用
        navItem.addEventListener("click", () => selectProvider(p.id));

        // 勾选限制:最少 1 个(数量无上限);切换后顺带选中该项
        cb.addEventListener("change", () => {
            const enabledCount = providerCards.filter((c) => c.enabled.checked).length;
            if (enabledCount < 1) {
                cb.checked = true;
                toast("至少保留 1 个 Provider", "error");
                return;
            }
            navItem.classList.toggle("disabled", !cb.checked);
            selectProvider(p.id);
        });

        providerCards.push(cardObj);
        updateProviderBadge(cardObj);
    });

    // 保持上次选中项;无则选中第一个
    const target = providerCards.find((c) => c.id === selectedProviderId) || providerCards[0];
    if (target) selectProvider(target.id);
}

// 选中某个 Provider(左栏高亮 + 右栏显示对应详情)
function selectProvider(id) {
    selectedProviderId = id;
    providerCards.forEach((c) => {
        const active = c.id === id;
        c.navItem.classList.toggle("active", active);
        c.pane.classList.toggle("active", active);
    });
}

// 渲染一组凭证页(按注册表元数据生成,placeholder 显示掩码值);
// 组首为可选的显示名输入(实时同步凭证标签文案,详情页用其代替 "Key N");
// 组尾带"删除此凭证"按钮(组数 >1 时显示)。
// 返回组对象 {fields:[{key,input}], nameInput, budget(input|null), page}。
function renderCredGroup(cardObj, creds, name, budget) {
    const p = cardObj.def;
    const page = document.createElement("div");
    page.className = "provider-group cred-page";

    // 显示名(可选):用于详情页代替 "Key N" 辨识
    const nameGroup = document.createElement("div");
    nameGroup.className = "form-group";
    const nameLabel = document.createElement("label");
    nameLabel.textContent = "名称(可选)";
    const nameInput = document.createElement("input");
    nameInput.type = "text";
    nameInput.className = "provider-group-name";
    nameInput.placeholder = "详情页标识,如 \"工作号\"";
    if (name) nameInput.value = name;
    nameInput.addEventListener("input", () => refreshCredTabs(cardObj));
    nameGroup.append(nameLabel, nameInput);
    page.appendChild(nameGroup);

    const fieldsWrap = document.createElement("div");
    fieldsWrap.className = "provider-fields";
    const fields = [];
    (p.fields || []).forEach((f) => {
        const fg = document.createElement("div");
        fg.className = "form-group";
        const label = document.createElement("label");
        label.textContent = f.label;
        if (f.type === "select") {
            // 复选框组(多选):隐藏 input 承载逗号拼接值,collectProviders 统一读取。
            // select 字段为 plain(不掩码),直接用已存值初始化勾选状态。
            const hidden = document.createElement("input");
            hidden.type = "hidden";
            const saved = parseOptionValues((creds && creds[f.key]) || "", f.options || []);
            hidden.value = saved.join(",");
            const wrap = document.createElement("div");
            wrap.className = "field-options";
            (f.options || []).forEach((opt) => {
                const optLabel = document.createElement("label");
                optLabel.className = "field-option";
                const cb = document.createElement("input");
                cb.type = "checkbox";
                cb.value = opt.value; // 必须显式设置,否则默认值 "on" 会污染存储值
                cb.checked = saved.includes(opt.value);
                cb.addEventListener("change", () => {
                    const checked = wrap.querySelectorAll("input[type='checkbox']:checked");
                    hidden.value = Array.from(checked).map((x) => x.value).join(",");
                    updateProviderBadge(cardObj);
                });
                const text = document.createElement("span");
                text.textContent = opt.label;
                optLabel.append(cb, text);
                wrap.appendChild(optLabel);
            });
            fg.append(label, wrap, hidden);
            fieldsWrap.appendChild(fg);
            fields.push({ key: f.key, input: hidden });
            return;
        }
        const input = document.createElement(f.type === "textarea" ? "textarea" : "input");
        if (f.type === "password") input.type = "password";
        if (f.type === "text") input.type = "text";
        if (f.type === "textarea") input.rows = 2;
        input.placeholder = (creds && creds[f.key]) || "";
        input.addEventListener("input", () => updateProviderBadge(cardObj));
        fg.append(label, input);
        fieldsWrap.appendChild(fg);
        fields.push({ key: f.key, input });
    });
    page.appendChild(fieldsWrap);

    // 余额型凭证组的预算输入(仅 balance 渠道;与同渠道其它凭证组相互独立)
    let budgetInput = null;
    if (p.kind === "balance") {
        const budgetGroup = document.createElement("div");
        budgetGroup.className = "form-group";
        const budgetLabel = document.createElement("label");
        budgetLabel.textContent = "预算(用于进度条计算)";
        budgetInput = document.createElement("input");
        budgetInput.type = "number";
        budgetInput.min = "0";
        budgetInput.step = "0.01";
        budgetInput.placeholder = "设为 0 则不计算进度条";
        if (budget > 0) budgetInput.value = budget;
        budgetGroup.append(budgetLabel, budgetInput);
        page.appendChild(budgetGroup);
    }

    const g = { fields, nameInput, budget: budgetInput, page };
    const del = document.createElement("button");
    del.className = "btn-sm del-cred";
    del.textContent = "删除此凭证";
    del.addEventListener("click", () => {
        if (cardObj.groups.length <= 1) return;
        const idx = cardObj.groups.indexOf(g);
        cardObj.groups.splice(idx, 1);
        cardObj.pages.removeChild(page);
        refreshCredTabs(cardObj);
        setActiveCred(cardObj, Math.min(idx, cardObj.groups.length - 1)); // 激活相邻页
        updateProviderBadge(cardObj);
    });
    page.appendChild(del);

    cardObj.pages.appendChild(page);
    return g;
}

// 凭证组快照(值 + 掩码占位),供纯函数判定"非空组",口径与 collectProviders 一致
function snapshotGroups(cardObj) {
    return cardObj.groups.map((g) =>
        g.fields.map((f) => ({ value: f.input.value, placeholder: f.input.placeholder })));
}

// 更新状态徽标(左栏导航 + 右栏详情标题行,文案:未配置 / n 个凭证)
function updateProviderBadge(cardObj) {
    const text = providerBadgeText(snapshotGroups(cardObj));
    cardObj.badge.textContent = text;
    cardObj.navBadge.textContent = text;
}

// 应用当前激活凭证页(标签高亮 + 页面显隐 + 删除按钮显隐)
function applyCredActive(cardObj) {
    const tabBtns = cardObj.tabs.querySelectorAll(".cred-tab:not(.add)");
    tabBtns.forEach((btn, i) => btn.classList.toggle("active", i === cardObj.active));
    cardObj.groups.forEach((g, i) => {
        g.page.classList.toggle("active", i === cardObj.active);
        const del = g.page.querySelector(".del-cred");
        if (del) del.style.display = cardObj.groups.length > 1 ? "" : "none";
    });
}

// 切换到第 i 个凭证页(越界钳制)
function setActiveCred(cardObj, i) {
    cardObj.active = Math.max(0, Math.min(i, cardObj.groups.length - 1));
    applyCredActive(cardObj);
}

// 重建凭证标签条(每组一个标签 + 末尾 "+" 添加按钮),并保持激活态
function refreshCredTabs(cardObj) {
    cardObj.tabs.innerHTML = "";
    cardObj.groups.forEach((g, i) => {
        const btn = document.createElement("button");
        btn.className = "cred-tab";
        btn.textContent = credTabLabel(g.nameInput.value, i);
        btn.addEventListener("click", () => setActiveCred(cardObj, i));
        cardObj.tabs.appendChild(btn);
    });
    const add = document.createElement("button");
    add.className = "cred-tab add";
    add.textContent = "+";
    add.title = "添加凭证";
    add.addEventListener("click", () => {
        cardObj.groups.push(renderCredGroup(cardObj, {}, "", 0));
        refreshCredTabs(cardObj);
        setActiveCred(cardObj, cardObj.groups.length - 1); // 切到新页
        updateProviderBadge(cardObj);
    });
    cardObj.tabs.appendChild(add);
    setActiveCred(cardObj, cardObj.active);
}

document.getElementById("btn-save-config").addEventListener("click", async () => {
    const providers = collectProviders();
    const interval = parseInt(document.getElementById("input-interval").value) || 15;
    try {
        await window.go.main.App.SaveConfig(providers, interval);
        // 清空输入框(已保存)
        providerCards.forEach((c) => c.groups.forEach((g) => g.fields.forEach((f) => {
            f.input.value = "";
        })));
        toast("已保存", "success");
        await loadConfig(); // 重新拉取(placeholder 显示新掩码)
        await refreshQuota(); // 立即刷新展示
    } catch (e) {
        console.error("saveConfig error:", e);
        toast("保存配置失败: " + e, "error");
    }
});

// 测试/打开登录页按钮(事件委托,兼容动态生成的按钮)
document.addEventListener("click", async (e) => {
    const testBtn = e.target.closest("[data-test]");
    if (testBtn) {
        const platform = testBtn.getAttribute("data-test");
        // 先保存当前输入(刷新间隔不动),再测试
        try {
            await window.go.main.App.SaveConfig(collectProviders(), 0);
            const result = await window.go.main.App.TestConnection(platform);
            toast(result, result.startsWith("成功") ? "success" : "error");
        } catch (err) {
            console.error("testConnection error:", err);
            toast("测试连接失败: " + err, "error");
        }
        return;
    }
    const openBtn = e.target.closest("[data-open]");
    if (openBtn) {
        const url = openBtn.getAttribute("data-open");
        try {
            window.go.main.App.OpenLoginPage(url);
        } catch (err) {
            console.error("openLoginPage error:", err);
            toast("打开登录页失败: " + err, "error");
        }
    }
});

// === Toast(替代 alert) ===
let toastTimer = null;
function toast(msg, type) {
    const el = document.getElementById("toast");
    el.textContent = msg;
    el.className = "toast show " + (type || "");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => {
        el.classList.remove("show");
    }, 2500);
}

// === 球位置记忆(拖动结束时保存)===
let dragTimer = null;
document.getElementById("ball").addEventListener("mouseup", () => {
    clearTimeout(dragTimer);
    dragTimer = setTimeout(() => {
        // Wails 获取窗口位置
        window.runtime.WindowGetPosition().then((pos) => {
            window.go.main.App.SaveBallPosition(pos.x, pos.y);
        });
    }, 500);
});

// === 启动:加载初始数据 ===
window.go.main.App.Refresh();

// === 托盘事件 ===
window.runtime.EventsOn("tray:refresh", () => {
    refreshQuota();
});

window.runtime.EventsOn("ui:show-settings", () => {
    setView("settings");
    loadConfig();
});
