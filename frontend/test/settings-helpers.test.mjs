// settings-helpers.js 单测(Node 内置 node:test,零依赖;运行:cd frontend && npm test)
import { test } from "node:test";
import assert from "node:assert/strict";
import { credTabLabel, groupHasData, providerBadgeText } from "../src/settings-helpers.js";

test("credTabLabel: 优先显示名", () => {
    assert.equal(credTabLabel("工作号", 0), "工作号");
    assert.equal(credTabLabel(" 备用 ", 3), "备用"); // 去除首尾空白
});

test("credTabLabel: 空名回退 凭证 n(从 1 起)", () => {
    assert.equal(credTabLabel("", 0), "凭证 1");
    assert.equal(credTabLabel("   ", 2), "凭证 3");
    assert.equal(credTabLabel(undefined, 1), "凭证 2");
    assert.equal(credTabLabel(null, 4), "凭证 5");
});

test("groupHasData: 空组判定", () => {
    assert.equal(groupHasData([]), false);
    assert.equal(groupHasData([{ value: "", placeholder: "" }]), false);
    assert.equal(groupHasData([{ value: "", placeholder: "" }, { value: "", placeholder: "" }]), false);
});

test("groupHasData: 有输入值或掩码占位均视为有数据", () => {
    assert.equal(groupHasData([{ value: "sk-abc", placeholder: "" }]), true);
    assert.equal(groupHasData([{ value: "", placeholder: "••••abcd" }]), true);
    assert.equal(groupHasData([{ value: "", placeholder: "" }, { value: "x", placeholder: "" }]), true);
});

test("providerBadgeText: 无非空组显示未配置", () => {
    assert.equal(providerBadgeText([]), "未配置");
    assert.equal(providerBadgeText([[{ value: "", placeholder: "" }]]), "未配置");
});

test("providerBadgeText: 按非空组数量显示", () => {
    const g1 = [{ value: "a", placeholder: "" }];
    const g2 = [{ value: "", placeholder: "mask" }];
    const gEmpty = [{ value: "", placeholder: "" }];
    assert.equal(providerBadgeText([g1]), "1 个凭证");
    assert.equal(providerBadgeText([g1, g2]), "2 个凭证");
    assert.equal(providerBadgeText([g1, gEmpty, g2]), "2 个凭证"); // 空组不计数
});
