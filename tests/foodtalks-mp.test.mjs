// 「FoodTalks食品资讯」公众号排 FoodTalks 网站稿（2026-10-27，《FoodTalks公众号排版规范（新）》）。
// 样稿：api/testdata/foodtalks-mp/ 里 FoodTalks 网站上的 3 篇转载导入后的正文块（Go 生成），
// 规则：同目录 rules.json（服务端给这个账号的默认规则，Go 生成）。对照的是编辑手排发出去的公众号：
//   63296 懂经 = https://mp.weixin.qq.com/s/-JpQgfPwmtx1XQK1AEC7CA
//   63278 零售圈 = https://mp.weixin.qq.com/s/gKM213ttsBUI30ZeeRnqzw
//   63234 职业零售网 = https://mp.weixin.qq.com/s/DofmJbUqBJVJ3i3w5O-bWw

import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';

const storage = { getItem() { return null; }, setItem() {}, removeItem() {} };
globalThis.window = { __APP_BUILD_INFO: { build: 'test' }, localStorage: storage, location: { origin: 'https://layout.test' } };
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const template = (await import('../public/js/templates/foodtalks-series.js?test=' + Date.now())).default;

const dir = new URL('../api/testdata/foodtalks-mp/', import.meta.url);
const rules = JSON.parse(fs.readFileSync(new URL('rules.json', dir), 'utf8'));
const config = { ...rules.config, behavior: rules.behavior };
const sample = (id) => JSON.parse(fs.readFileSync(new URL(`${id}.blocks.json`, dir), 'utf8'));

// 每个 <section>/<p> 一行纯文字（图写成 [图 文件名]），方便按读者看到的顺序断言。
function readerLines(lines) {
  return lines.join('\n')
    .replace(/<img[^>]*src="([^"]+)"[^>]*>/g, (_, src) => `[图 ${src.split('/').pop()}]`)
    .replace(/<br>/g, ' / ')
    .split(/<\/(?:section|p)>/)
    .map(s => s.replace(/<[^>]+>/g, '').replace(/&nbsp;/g, ' ').trim())
    .filter(Boolean);
}
const layout = (id, cfg = config) => template.processBlocks(sample(id), { config: cfg });

test('网站开头署名挪到文末：来源、作者逐行深蓝斜体，再空一行接提示（63296 懂经）', () => {
  const out = layout('63296');
  const text = readerLines(out.lines);
  assert.ok(!text.slice(0, 3).some(t => t.startsWith('本文为懂经原创')), '开头不再有网站署名');
  assert.deepEqual(text.slice(-5), [
    '来源：懂经',
    '作者：叶宇',
    '提示：',
    '*本文为懂经原创，作者：叶宇，转载请联系出处。原文标题：《金粒门上海首店太火爆暂停营业，新鲜零食想在上海走长远，没那么简单》',
    '*FoodTalks资讯投稿与商务合作，请联系Bobo。',
  ]);
  const html = out.lines.join('\n');
  assert.match(html, /<section style="margin: 0 8px 0px;[^"]*"><span style="font-size: 15px; line-height: 1\.75; color: #02396b;[^"]*font-style: italic;">来源：懂经<\/span>/, '来源紧贴作者、深蓝斜体');
  assert.match(html, /margin: 0 8px 20px;[^"]*"><span[^>]*>作者：叶宇</, '署名后空一行再接提示');
  assert.match(html, /font-weight: bold; font-style: italic;">提示：</, '「提示：」粗斜体');
});

test('网站文末（提示、投稿、加群、关注公众号）整段换掉，不进公众号正文', () => {
  for (const id of ['63296', '63278', '63234']) {
    const text = readerLines(layout(id).lines).join('\n');
    for (const leak of ['不代表FoodTalks立场', 'FoodTalks商务合作方案', '加入微信群', '关注公众号', '点击获取更多全球食品资讯', 'Lemon']) {
      assert.ok(!text.includes(leak), `${id} 正文里还留着网站文末「${leak}」`);
    }
  }
});

test('来源带网站声明里的公众号 ID；网站声明没写原文标题就不写（63278 零售圈）', () => {
  const text = readerLines(layout('63278').lines);
  assert.deepEqual(text.slice(-5), [
    '来源：零售圈（ID：retaileco）',
    '作者：RT内容组',
    '提示：',
    '*本文为零售圈原创，作者：RT内容组，转载请联系出处。',
    '*FoodTalks资讯投稿与商务合作，请联系Bobo。',
  ]);
});

test('网站标题1 → 饼干序号图 + 18px 深蓝粗体居中；连着两个标题1 是一个两行标题（63234 山姆）', () => {
  const out = layout('63234');
  const text = readerLines(out.lines);
  const at = text.indexOf('黑龙江首家山姆 / 落子哈尔滨');
  assert.ok(at > 0, '两行标题合成一个');
  assert.equal(text[at - 1], '[图 01.png]', '共用第 1 张序号图');
  const third = text.indexOf('山姆2026 / 一边狂开，一边圈地');
  assert.equal(text[third - 1], '[图 03.png]', '第三个标题用 03');
  assert.ok(out.lines.join('\n').includes('/assets/foodtalks-mp/sections/01.png'), '饼干序号图（不是什么是系列那套）');
  assert.match(out.lines.join('\n'), /font-size: 18px; line-height: 1\.75; color: #02396b;[^"]*font-weight: bold;">黑龙江首家山姆<\/span><br><span[^>]*>落子哈尔滨/);
});

test('图注去句末句号，「来源：」写成「图片来源：」', () => {
  const text = readerLines(layout('63296').lines);
  assert.ok(text.includes('金粒门上海首店周末排队'), '网站图注「金粒门上海首店周末排队。」去掉句号');
  const out = template.processBlocks({
    title: 't', blocks: [
      { k: 'img', src: 'https://example.com/a.png' },
      { k: 'cap', text: '来源：星巴克' },
      { k: 'txt', runs: [{ type: 'txt', text: '正文。' }] },
    ],
  }, { config });
  assert.ok(readerLines(out.lines).includes('图片来源：星巴克'));
});

test('FoodTalks 原创：不写来源行，提示写「转载请底部留言」', () => {
  const out = template.processBlocks({
    title: '食装｜茶包装', source_name: 'FoodTalks', blocks: [
      { k: 'txt', runs: [{ type: 'txt', text: '作者：Doris' }] },
      { k: 'txt', runs: [{ type: 'txt', text: '本期「食装」邀您欣赏三款茶品牌包装。' }] },
      { k: 'txt', runs: [{ type: 'txt', text: '提示：', bold: true }] },
      { k: 'txt', runs: [{ type: 'txt', text: '*本文为FoodTalks原创，不代表FoodTalks立场，转载请联系出处。' }] },
    ],
  }, { config });
  assert.deepEqual(readerLines(out.lines).slice(-4), [
    '作者：Doris', '提示：', '*本文为FoodTalks原创，转载请底部留言。', '*FoodTalks资讯投稿与商务合作，请联系Bobo。',
  ]);
});

test('英文稿正文 14px；列表项加小黑点', () => {
  const out = template.processBlocks({
    title: 'x', source_name: 'Food Dive', blocks: [
      { k: 'txt', runs: [{ type: 'txt', text: 'Nestle is cutting prices on coffee as demand for premium capsules softens across Europe.' }] },
      { k: 'li', runs: [{ type: 'txt', text: 'Capsules down 4% year over year' }] },
    ],
  }, { config });
  const html = out.lines.join('\n');
  assert.match(html, /font-size: 14px;[^"]*">Nestle is cutting/);
  assert.match(html, />• <\/span><span[^>]*>Capsules down/);
});

test('页脚：Bobo 名片 →「关注公众号」→ 名片卡提示 →「加入微信群」→ 食品资讯群码 → Lemon', () => {
  globalThis.window._activeConfig = { config };
  globalThis.window._activeRulePack = { id: 'foodtalks-mp' };
  const footer = template.renderBottomFooter({ isRepost: true });
  const text = readerLines([footer.html]);
  assert.deepEqual(text, [
    '[图 bobo-card.webp]',
    '关注公众号',
    '点击卡片关注FoodTalks公众号',
    '加入微信群',
    '[图 group-news.webp]',
    '*如遇二维码失效无法加群，请联系工作人员Lemon（微信号：FBIF_Lemon）。',
  ]);
  assert.match(footer.html, /color:#02396b;font-size:18px;[^"]*font-style:italic;font-weight:bold;[^"]*">关注公众号/);
});

test('网站自动加的公司库链接去掉只留字，公众号文章链接保留', () => {
  const out = template.processBlocks({
    title: 't', source_name: '职业零售网', blocks: [
      { k: 'txt', runs: [
        { type: 'txt', text: '期待已久的哈尔滨' },
        { type: 'link', text: '山姆', href: 'https://www.foodtalks.cn/company/sams-club' },
        { type: 'txt', text: '，详见' },
        { type: 'link', text: '上一篇', href: 'https://mp.weixin.qq.com/s/abc' },
      ] },
    ],
  }, { config });
  const html = out.lines.join('\n');
  assert.ok(!html.includes('foodtalks.cn/company'), '公司库链接去掉');
  assert.match(html, /<a href="https:\/\/mp\.weixin\.qq\.com\/s\/abc"/, '公众号文章链接保留');
});

test('网页里 engine 只给通用扁平参数 + 行为：序号图、标题 18px、图片边距照样用本账号规则（不落回什么是系列）', () => {
  globalThis.window._activeConfig = { config: rules.config, behavior: rules.behavior };
  try {
    const out = template.processBlocks(sample('63234'), { config: { font_size: '15', behavior: rules.behavior } });
    const html = out.lines.join('\n');
    assert.ok(html.includes('/assets/foodtalks-mp/sections/01.png'));
    assert.ok(!html.includes('/assets/foodtalks-series/sections/'));
    assert.match(html, /font-size: 18px;[^"]*font-weight: bold;">黑龙江首家山姆</, '标题 18px（曾按什么是系列排成 20px）');
    assert.doesNotMatch(html, /font-size: 20px/);
    assert.match(html, /<section style="margin: 0 8px 20px; text-align: center; line-height: 0; font-size: 0;"><img/, '图片与正文同为两端 8px');
    assert.doesNotMatch(html, /<section style="margin: 0 0 \d+px; text-align: center; line-height: 0; font-size: 0;"><img/);
  } finally {
    globalThis.window._activeConfig = undefined;
  }
});

// 2026-10-28 用户贴灵兽 https://mp.weixin.qq.com/s/frys28EF1-FyWlzNOrsj_A：原文章节是单独一行的大号「1」
// 下面一行加粗标题，抓取出来是 h「1」+ 加粗段；排出来成了两行灰色加粗正文。应是饼干序号图 + 深蓝标题。
test('贴公众号链接：小标题和 FoodTalks 网站同一套判定，单独一行的章节号「1」不单独成标题', () => {
  const bold = (text) => ({ k: 'txt', runs: [{ type: 'txt', text, bold: true }] });
  const txt = (text) => ({ k: 'txt', runs: [{ type: 'txt', text }] });
  const out = template.processBlocks({
    title: '警惕新鲜零食', source_name: '灵兽', blocks: [
      txt('排队的顾客多，证明了新鲜零食的吸引力。'),
      { k: 'h', text: '1' }, { k: 'h', text: '割裂的新鲜零食赛道' }, txt('上海闵行万象城B1层。'),
      { k: 'h', text: '02' }, { k: 'h', text: '资本对新鲜零食为什么犹豫？金粒门为什么先拿到了融资？' }, txt('资本之所以犹豫。'),
      { k: 'h', text: '五个绕不过去的坎' }, txt('第一个坎。'),
      { k: 'h', text: '（一）供应链', level: 3 }, txt('二级标题下的正文。'),
      bold('加粗强调句'), txt('普通正文。'),
      bold('“新鲜零食不是噱头，是真实需求。”'), txt('受访者这样说。'),
    ],
  }, { config });
  const text = readerLines(out.lines);
  const html = out.lines.join('\n');
  assert.deepEqual(text.slice(0, 11), [
    '排队的顾客多，证明了新鲜零食的吸引力。',
    '[图 01.png]', '割裂的新鲜零食赛道', '上海闵行万象城B1层。',
    '[图 02.png]', '资本对新鲜零食为什么犹豫？金粒门为什么先拿到了融资？', '资本之所以犹豫。',
    '[图 03.png]', '五个绕不过去的坎', '第一个坎。',
    '（一）供应链',
  ]);
  assert.ok(!text.includes('1') && !text.includes('02'), '原文序号由序号图给，不再单独成行');
  assert.match(html, /font-size: 18px; line-height: 1\.75; color: #02396b;[^"]*font-weight: bold;">割裂的新鲜零食赛道</);
  assert.match(html, /alt="加粗强调句"/, '独占一行的加粗短句和网站一样升成标题1');
  assert.match(html, /font-size: 15px; line-height: 1\.75; color: #544545;[^"]*font-weight: bold;">“新鲜零食不是噱头，是真实需求。”</, '整句引语和网站一样仍是加粗正文');
});

test('什么是系列不受影响：同一份块不挪署名、不加提示、标题不按块级别、图片满列', () => {
  const series = { behavior: {} };
  const out = layout('63234', series);
  const text = readerLines(out.lines);
  assert.equal(text[0], '本文为职业零售网原创，作者：零售哥，转载请联系出处。');
  assert.ok(!text.includes('来源：职业零售网'));
  assert.ok(!text.some(t => t.includes(' / ')), '不合并两行标题');
  assert.match(out.lines.join('\n'), /<section style="margin: 0 0 20px; text-align: center; line-height: 0; font-size: 0;"><img/);
});
