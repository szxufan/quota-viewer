// 额度面板纯逻辑函数(无 DOM/Wails 依赖,可被 node --test 单测)。
// 由 main.js 的额度详情渲染与倒计时刷新逻辑引用。

// formatCountdown 计算距 isoStr 的倒计时文案。
// label 非空时带周期前缀(多周期渠道),如 "距下次刷新(5小时窗口): 2时30分";
// 已过期时单周期返回 "已过期",多周期返回 "<label>: 已过期"。
// now 可注入(毫秒时间戳),便于测试。
export function formatCountdown(isoStr, label = "", now = Date.now()) {
    const target = new Date(isoStr);
    if (isNaN(target.getTime())) return "";
    const diff = target.getTime() - now;
    if (diff <= 0) return label ? `${label}: 已过期` : "已过期";
    const days = Math.floor(diff / 86400000);
    const hours = Math.floor((diff % 86400000) / 3600000);
    const mins = Math.floor((diff % 3600000) / 60000);
    const prefix = label ? `距下次刷新(${label})` : "距下次刷新";
    if (days > 0) return `${prefix}: ${days}天${hours}时${mins}分`;
    if (hours > 0) return `${prefix}: ${hours}时${mins}分`;
    return `${prefix}: ${mins}分`;
}

// buildResetHtml 生成额度单元格的刷新倒计时 HTML。
// 多周期渠道(period_resets 非空)包在 .quota-reset-row 弹性行内:宽度足够时
// 各周期同行排列,不足时自动换行;单周期渠道回退 reset_at 一行;
// 错误或未知刷新时间返回空串。
// 倒计时文本由 data-reset-at(/data-period-label) 驱动,updateCountdowns 定时刷新。
export function buildResetHtml(r) {
    if (r.error) return "";
    const periods = Array.isArray(r.period_resets)
        ? r.period_resets.filter((p) => p && p.reset_at)
        : [];
    if (periods.length > 0) {
        const items = periods
            .map(
                (p) =>
                    `<span class="quota-reset" data-reset-at="${p.reset_at}" data-period-label="${p.label || ""}">${formatCountdown(p.reset_at, p.label || "")}</span>`
            )
            .join("");
        return `<div class="quota-reset-row">${items}</div>`;
    }
    if (r.reset_at) {
        return `<div class="quota-reset" data-reset-at="${r.reset_at}">${formatCountdown(r.reset_at)}</div>`;
    }
    return "";
}
