// 红线下放·正向验证：证明【服务端规则配置】真正驱动输出，而非只回退代码内 const。
// 其余特征/行为测试已证明「缺省时字节不变（==const 兜底）」；本文件证明「配置即生效」。
import test from 'node:test';
import assert from 'node:assert/strict';
import {
  applyFoodTalksBehavior,
  buildSiteFooterTipElem,
  buildAuthorLineFromSource,
} from '../public/js/rule-behavior-foodtalks.js';

// foodtalks-site 经 engine.js 顶层会 fetch 主题 CSS——在 Node 里需先搭最小浏览器
// 环境（window + fetch stub），再动态导入模板，避免相对 URL fetch 抛未捕获 rejection。
const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
globalThis.window = { __APP_BUILD_INFO: { build: 'test' }, localStorage: storage };
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });
const siteTemplate = (await import('../public/js/templates/foodtalks-site.js?downstream=' + Date.now())).default;

function p(text) { return { k: 'p', runs: [{ type: 'txt', text }] }; }

// ── 网站：行为模块直接读 behavior 覆盖值 ──

test('buildSiteFooterTipElem 用 behavior.templates 覆盖前缀/后缀/标题', () => {
  const behavior = {
    templates: {
      site_tip_prefix_fbif: 'XPREFIX',
      site_tip_suffix: 'XSUFFIX',
      tip_title_default: 'TIPHEAD：',
    },
  };
  const elem = buildSiteFooterTipElem('某标题', { source: '', behavior });
  assert.equal(elem.title, 'TIPHEAD：');
  const joined = elem.rest_runs.map(r => r.text || '').join('');
  assert.ok(joined.startsWith('XPREFIX'), `应以覆盖前缀开头：${joined}`);
  assert.ok(joined.endsWith('XSUFFIX'), `应以覆盖后缀结尾：${joined}`);
});

test('buildAuthorLineFromSource 用 behavior.templates 覆盖转载句式', () => {
  const behavior = {
    templates: {
      author_line_repost_prefix: 'REWRITE-{source}-原创',
      author_line_repost_suffix: '——转载须授权。',
    },
  };
  const line = buildAuthorLineFromSource([p('来源：食品内参')], behavior);
  assert.ok(line.includes('REWRITE-食品内参-原创'), `应套用覆盖前缀：${line}`);
  assert.ok(line.endsWith('——转载须授权。'), `应套用覆盖后缀：${line}`);
});

test('applyFoodTalksBehavior 用 behavior.lists.wechat_trailer_markers 自定义切点', () => {
  const config = { behavior: { lists: { wechat_trailer_markers: ['切到这里'] } } };
  const elems = [p('正文一'), p('切到这里就停'), p('应被切掉')];
  const out = applyFoodTalksBehavior(elems, config, { isRepost: true });
  const texts = out.map(e => (e.runs || []).map(r => r.text || '').join(''));
  assert.ok(texts.includes('正文一'), '切点前正文保留');
  assert.ok(!texts.some(t => t.includes('应被切掉')), '自定义切点后内容应被切除');
});

// ── 网站：渲染器读 config / config.behavior 覆盖值 ──

test('foodtalks-site 渲染读 config 覆盖的颜色/字号/标签/图注词/图宽', async () => {
  const config = {
    tip_title_color: '#ff0000',
    tip_body_color: '#00ff00',
    author_font_size: '22',
    caption_font_size: '9',
    tip_font_size: '19',
    muted_color: '#abcabc',
    image: { portrait_px: 333, landscape_px: 777 },
    behavior: {
      templates: { ref_header_label: 'REFLABEL：', tip_title_default: 'TIPHEAD：' },
      lists: { image_caption_keywords: ['ZZZKW'] },
    },
    modules: { fbif_footer: { enabled: false } },
  };
  const md = [
    '作者：张三',
    '',
    '提示：本文为测试声明',
    '',
    '![](http://x.com/a.jpg)',
    '',
    'ZZZKW 这是图注内容',
    '',
    '参考来源：',
    '',
    '[1] 某文献',
  ].join('\n');
  const { lines } = await siteTemplate.processMd(md, { config });
  const html = lines.join('\n');
  assert.ok(html.includes('font-size: 22px'), '作者字号应用 author_font_size=22');
  assert.ok(html.includes('#ff0000'), '提示标题色应用 tip_title_color');
  assert.ok(html.includes('font-size: 19px'), '提示字号应用 tip_font_size=19');
  assert.ok(html.includes('#00ff00'), '提示正文色应用 tip_body_color');
  assert.ok(html.includes('TIPHEAD'), '提示标题文案应用 tip_title_default');
  assert.ok(html.includes('REFLABEL'), '参考来源标签应用 ref_header_label');
  assert.ok(html.includes('width="777"'), '横图宽度应用 image.landscape_px=777');
  assert.ok(/font-size: 9px;[^>]*#abcabc/.test(html) || (html.includes('font-size: 9px') && html.includes('#abcabc')),
    '图注字号/颜色应用 caption_font_size=9 + muted_color（证明 ZZZKW 图注词命中）');
});
