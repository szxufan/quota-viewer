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

// 状态同步各模式需展示的字段清单(多机状态同步,见 oss-state-sync.md)。
// secret: true 的字段后端下发为掩码,显示在 placeholder;提交掩码由后端还原旧值。
// placeholder 为非密文字段的输入提示。
export function syncFieldsForMode(mode) {
    const password = { key: "password", label: "加密密码", type: "password", secret: true, placeholder: "两端密码必须一致" };
    if (mode === "publish") {
        return [
            { key: "oss_endpoint", label: "OSS Endpoint", type: "text", secret: false, placeholder: "https://oss-cn-hangzhou.aliyuncs.com" },
            { key: "oss_bucket", label: "OSS Bucket", type: "text", secret: false, placeholder: "需开启公共读" },
            { key: "oss_key", label: "对象路径", type: "text", secret: false, placeholder: "如 quota/state.enc" },
            { key: "oss_access_id", label: "AccessKey ID", type: "text", secret: false, placeholder: "" },
            { key: "oss_access_secret", label: "AccessKey Secret", type: "password", secret: true, placeholder: "" },
            password,
        ];
    }
    if (mode === "subscribe") {
        return [
            { key: "url", label: "状态文件地址", type: "text", secret: false, placeholder: "https://<bucket>.oss-<region>.aliyuncs.com/<对象路径>" },
            password,
        ];
    }
    return [];
}

// 悬浮球网格布局:返回 {cols, rows, size}。
// 规则:1-3 单行 60×60;4 个 2×2;≥5 按 ceil(sqrt(n)) 方形扩展,边长 = max(60, cols*22)。
// 纯函数,供 main.js updateBall 与收起时判断尺寸是否需要补调(SetBallSize)复用。
export function ballGridFor(n) {
    let cols = n, size = 60;
    if (n > 4) {
        cols = Math.ceil(Math.sqrt(n));
        size = Math.max(60, cols * 22);
    } else if (n === 4) {
        cols = 2;
    }
    return { cols, rows: Math.ceil(n / Math.max(cols, 1)), size };
}
