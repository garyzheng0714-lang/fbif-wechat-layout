// footer-library-editor.test.mjs — 源码结构检查（参照 more-articles-rule-editor.test.mjs 风格）
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const src = await readFile(
  new URL('../public/js/footer-library-rule-editor.js', import.meta.url),
  'utf8'
);

// ── 导出 ──

test('导出 createFooterLibraryEditor', () => {
  assert.match(src, /export function createFooterLibraryEditor/,
    'createFooterLibraryEditor 必须作为命名导出');
});

test('接受 { container, footerData, onChange } 参数', () => {
  assert.match(src, /\{\s*container[\s\S]*?footerData[\s\S]*?onChange[\s\S]*?\}/,
    '函数参数应解构 container / footerData / onChange');
});

test('返回 destroy() 清理句柄', () => {
  assert.match(src, /destroy\s*\(\s*\)/, '返回对象需包含 destroy()');
});

// ── 页脚片段 key ──

test('包含片段 biz_coop', () => {
  assert.match(src, /biz_coop/, '应含 biz_coop 片段');
});

test('包含什么是系列 BD 名片和相关合作表片段', () => {
  assert.match(src, /bd_card/, '应含 bd_card 片段');
  assert.match(src, /coop_table/, '应含 coop_table 片段');
});

test('包含片段 join_group', () => {
  assert.match(src, /join_group/, '应含 join_group 片段');
});

test('包含片段 follow_account', () => {
  assert.match(src, /follow_account/, '应含 follow_account 片段');
});

test('包含片段 logo', () => {
  assert.match(src, /['"]logo['"]/, '应含 logo 片段');
});

test('包含片段 more_info', () => {
  assert.match(src, /more_info/, '应含 more_info 片段');
});

test('中文片段名全部覆盖', () => {
  assert.match(src, /商务合作/, '应含商务合作');
  assert.match(src, /BD 名片/, '应含 BD 名片');
  assert.match(src, /相关合作表/, '应含相关合作表');
  assert.match(src, /加入微信群/, '应含加入微信群');
  assert.match(src, /关注公众号/, '应含关注公众号');
  assert.match(src, /FoodTalks logo/, '应含 FoodTalks logo');
  assert.match(src, /点击获取更多/, '应含点击获取更多');
});

test('后台编辑器按服务端片段动态渲染，不能写死旧 5 项列表', () => {
  assert.match(src, /function\s+allSegKeys/, '应从 segment_order / segments 动态收集片段');
  assert.match(src, /function\s+orderedSegKeys/, '应以服务端顺序为主并补齐新增片段');
  assert.doesNotMatch(src, /ALL_SEG_KEYS\s*=\s*\[/, '不能再用 ALL_SEG_KEYS 写死旧片段');
});

// ── 预设矩阵 ──

test('含预设列维度 tip', () => {
  assert.match(src, /['"]tip['"]/, '预设矩阵应含 tip 维度');
});

test('含预设「设为默认」交互', () => {
  assert.match(src, /set-default/, '应含 set-default action');
  assert.match(src, /active_preset_id/, '应操作 active_preset_id');
});

test('含新增预设逻辑', () => {
  assert.match(src, /add-preset/, '应含 add-preset action');
  assert.match(src, /新增预设|新预设/, '应含新增预设相关文案');
});

test('含删除预设逻辑', () => {
  assert.match(src, /delete-preset/, '应含 delete-preset action');
});

test('含预设名称内联编辑', () => {
  assert.match(src, /rename-preset/, '应含 rename-preset action');
});

// ── JSON 折叠区 ──

test('含 JSON 折叠 <details>', () => {
  assert.match(src, /<details/, '应含 <details> 折叠元素');
});

test('含校验 JSON 按钮', () => {
  assert.match(src, /validate-json/, '应含 validate-json action');
});

test('含应用 JSON 按钮', () => {
  assert.match(src, /apply-json/, '应含 apply-json action');
});

test('JSON 区域 data-role="json-area"', () => {
  assert.match(src, /data-role="json-area"/, '应含 json-area textarea');
});

// ── XSS 校验 ──

test('含 XSS 正则（script/on*=/javascript:）', () => {
  assert.match(src, /<script/, '应检测 <script');
  assert.match(src, /on\\w\+\\s\*=|on\\w\+=/, '应检测 on*= 事件属性');
  assert.match(src, /javascript:/, '应检测 javascript: 协议');
});

test('XSS 校验对齐后端（validateHtmlLines 函数存在）', () => {
  assert.match(src, /validateHtmlLines/, '应定义 validateHtmlLines 校验函数');
});

test('XSS 错误内联展示（不得 throw 或 alert）', () => {
  assert.match(src, /footer-lib-err/, '应用 footer-lib-err 样式内联展示错误');
  assert.doesNotMatch(src, /alert\(/, '不得用 alert 弹窗');
});

// ── 状态栏 ──

test('含状态栏「已同步」/「有未保存改动」文案', () => {
  assert.match(src, /已同步/, '应含已同步文案');
  assert.match(src, /有未保存改动/, '应含有未保存改动文案');
});

test('含撤销所有修改按钮', () => {
  assert.match(src, /undo-all/, '应含 undo-all action');
  assert.match(src, /撤销所有修改/, '应含撤销文案');
});

// ── 卡片折叠 ──

test('含「● 已改」变更标记', () => {
  assert.match(src, /已改/, '应含已改变更标记');
});

test('含重置为默认', () => {
  assert.match(src, /重置为默认/, '应含重置为默认链接');
  assert.match(src, /reset-seg/, '应含 reset-seg action');
  assert.match(src, /FOOTER_SEGMENTS_DEFAULT/, '应从默认常量恢复');
});

// ── 图片链接快速替换 ──

test('含图片链接快速替换行', () => {
  assert.match(src, /replace-img/, '应含 replace-img action');
  assert.match(src, /图片链接快速替换/, '应含图片替换文案');
});

test('图片 URL 校验', () => {
  assert.match(src, /validateImgUrl/, '应定义 validateImgUrl');
  assert.match(src, /https?:\/\//, '应校验 http(s)://');
});

// ── 引用默认常量 ──

test('引用 FOOTER_SEGMENTS_DEFAULT 降级常量', () => {
  assert.match(src, /FOOTER_SEGMENTS_DEFAULT/, '应引用前端默认片段常量');
});

test('引用 FOOTER_PRESETS_DEFAULT 降级常量', () => {
  assert.match(src, /FOOTER_PRESETS_DEFAULT/, '应引用前端默认预设常量');
});

// ── 样式 class 前缀 ──

test('自包含 CSS 使用 footer-lib- 前缀', () => {
  assert.match(src, /footer-lib-/, 'CSS class 应使用 footer-lib- 前缀');
});

test('复用 admin-secondary / admin-primary 按钮 class', () => {
  assert.match(src, /admin-secondary/, '应复用 admin-secondary 按钮样式');
});
