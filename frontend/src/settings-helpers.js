// 设置界面纯逻辑函数(无 DOM/Wails 依赖,可被 node --test 单测)。
// 由 main.js 的设置面板渲染逻辑引用,见 .trae/documents/settings-ui-redesign.md。

// 凭证标签文案:优先用户配置的显示名,否则回退 "凭证 n"(n 从 1 起)。
export function credTabLabel(name, index) {
    const n = (name || "").trim();
    return n || `凭证 ${index + 1}`;
}

// 凭证组是否视为"有数据":任一字段有输入值或掩码占位符。
// fields: [{value, placeholder}]。口径与 main.js collectProviders 的空组过滤一致。
export function groupHasData(fields) {
    return fields.some((f) => f.value || f.placeholder);
}

// Provider 摘要行徽标文案:groups 为各凭证组的字段快照数组。
// 无非空组 → "未配置";否则 "n 个凭证"。
export function providerBadgeText(groups) {
    const n = groups.filter(groupHasData).length;
    return n > 0 ? `${n} 个凭证` : "未配置";
}

// 解析 select 型字段存储的逗号拼接值(如 "ots,cdt")为合法选项值数组:
// 去空白、过滤未知值、去重,并按 options 的固定顺序返回(保证回显稳定)。
// options: [{value, label}]。
export function parseOptionValues(value, options) {
    const vals = String(value || "")
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
    const selected = new Set(vals);
    return (options || [])
        .map((o) => o.value)
        .filter((v, i, arr) => selected.has(v) && arr.indexOf(v) === i);
}
