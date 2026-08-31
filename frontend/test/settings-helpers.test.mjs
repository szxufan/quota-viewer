// settings-helpers.js 单测(Node 内置 node:test,零依赖;运行:cd frontend && npm test)
import { test } from "node:test";
import assert from "node:assert/strict";
import { credTabLabel, groupHasData, parseOptionValues, providerBadgeText } from "../src/settings-helpers.js";

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

const PKG_OPTIONS = [
    { value: "ots", label: "OTS 资源包" },
    { value: "flowbag", label: "VPC 共享流量包" },
    { value: "cdt", label: "CDT 流量包" },
];

test("parseOptionValues: 空值与空选项", () => {
    assert.deepEqual(parseOptionValues("", PKG_OPTIONS), []);
    assert.deepEqual(parseOptionValues(undefined, PKG_OPTIONS), []);
    assert.deepEqual(parseOptionValues(null, PKG_OPTIONS), []);
    assert.deepEqual(parseOptionValues("ots", []), []);
    assert.deepEqual(parseOptionValues("ots", undefined), []);
});

test("parseOptionValues: 解析逗号拼接值", () => {
    assert.deepEqual(parseOptionValues("ots", PKG_OPTIONS), ["ots"]);
    assert.deepEqual(parseOptionValues("ots,cdt", PKG_OPTIONS), ["ots", "cdt"]);
});

test("parseOptionValues: 去空白、去重、过滤未知值", () => {
    assert.deepEqual(parseOptionValues(" ots , flowbag ", PKG_OPTIONS), ["ots", "flowbag"]);
    assert.deepEqual(parseOptionValues("ots,ots,cdt", PKG_OPTIONS), ["ots", "cdt"]);
    assert.deepEqual(parseOptionValues("unknown,ots", PKG_OPTIONS), ["ots"]);
    assert.deepEqual(parseOptionValues("unknown", PKG_OPTIONS), []);
});

test("parseOptionValues: 按选项定义顺序返回(与存储顺序无关)", () => {
    assert.deepEqual(parseOptionValues("cdt,ots", PKG_OPTIONS), ["ots", "cdt"]);
});

test("parseOptionValues: 过滤复选框默认值 on(回归:未设 cb.value 时的脏数据)", () => {
    assert.deepEqual(parseOptionValues("on", PKG_OPTIONS), []);
    assert.deepEqual(parseOptionValues("on,on", PKG_OPTIONS), []);
});
