// quota-helpers.js 单测(Node 内置 node:test,零依赖;运行:cd frontend && npm test)
import { test } from "node:test";
import assert from "node:assert/strict";
import { buildResetHtml, formatCountdown } from "../src/quota-helpers.js";

const NOW = new Date("2026-10-09T12:00:00Z").getTime();

test("formatCountdown: 天/时/分分级", () => {
    assert.equal(formatCountdown("2026-10-12T15:30:00Z", "", NOW), "距下次刷新: 3天3时30分");
    assert.equal(formatCountdown("2026-10-09T14:30:00Z", "", NOW), "距下次刷新: 2时30分");
    assert.equal(formatCountdown("2026-10-09T12:45:00Z", "", NOW), "距下次刷新: 45分");
});

test("formatCountdown: 多周期带周期标签前缀", () => {
    assert.equal(formatCountdown("2026-10-09T14:30:00Z", "5小时窗口", NOW), "距下次刷新(5小时窗口): 2时30分");
});

test("formatCountdown: 已过期与非法时间", () => {
    assert.equal(formatCountdown("2026-10-09T11:00:00Z", "", NOW), "已过期");
    assert.equal(formatCountdown("2026-10-09T11:00:00Z", "周窗口", NOW), "周窗口: 已过期");
    assert.equal(formatCountdown("not-a-date", "", NOW), "");
});

test("buildResetHtml: 多周期包在弹性行内(宽度足够时同行),带 data-period-label", () => {
    const html = buildResetHtml({
        period_resets: [
            { label: "5小时窗口", reset_at: "2026-10-09T14:30:00Z" },
            { label: "周窗口", reset_at: "2026-10-12T00:00:00Z" },
        ],
    });
    assert.ok(html.includes('class="quota-reset-row"'));
    const matches = html.match(/class="quota-reset"/g);
    assert.equal(matches.length, 2);
    assert.ok(html.includes('data-period-label="5小时窗口"'));
    assert.ok(html.includes('data-period-label="周窗口"'));
    assert.ok(html.includes('data-reset-at="2026-10-09T14:30:00Z"'));
    assert.ok(html.includes("距下次刷新(5小时窗口)"));
    assert.ok(html.includes("距下次刷新(周窗口)"));
});

test("buildResetHtml: 单周期回退 reset_at,不带周期标签", () => {
    const html = buildResetHtml({ reset_at: "2026-10-10T12:00:00Z" });
    assert.ok(html.includes('class="quota-reset"'));
    assert.ok(html.includes('data-reset-at="2026-10-10T12:00:00Z"'));
    assert.ok(!html.includes("data-period-label"));
    assert.ok(html.includes("距下次刷新:"));
});

test("buildResetHtml: 错误或未知刷新时间返回空串", () => {
    assert.equal(buildResetHtml({ error: "HTTP 401", reset_at: "2026-10-10T12:00:00Z" }), "");
    assert.equal(buildResetHtml({}), "");
    assert.equal(buildResetHtml({ period_resets: [] }), "");
});

test("buildResetHtml: period_resets 中缺 reset_at 的周期被过滤", () => {
    const html = buildResetHtml({
        period_resets: [
            { label: "5小时窗口", reset_at: "" },
            { label: "周窗口", reset_at: "2026-10-12T00:00:00Z" },
        ],
    });
    const matches = html.match(/class="quota-reset"/g);
    assert.equal(matches.length, 1);
    assert.ok(html.includes('data-period-label="周窗口"'));
});

test("buildResetHtml: period_resets 优先于 reset_at", () => {
    // 多周期渠道仍填 reset_at(主窗口)作兼容;前端应以 period_resets 为准
    const html = buildResetHtml({
        reset_at: "2026-10-09T14:30:00Z",
        period_resets: [{ label: "周窗口", reset_at: "2026-10-12T00:00:00Z" }],
    });
    const matches = html.match(/class="quota-reset"/g);
    assert.equal(matches.length, 1);
    assert.ok(html.includes('data-period-label="周窗口"'));
    assert.ok(!html.includes('data-reset-at="2026-10-09T14:30:00Z"'));
});
