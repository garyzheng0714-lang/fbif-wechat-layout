import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';

const storage = {
  getItem() { return null; },
  setItem() {},
  removeItem() {},
};

globalThis.window = {
  __APP_BUILD_INFO: { build: 'test' },
  localStorage: storage,
};
globalThis.localStorage = storage;
globalThis.fetch = async () => ({ ok: false, text: async () => '' });

const template = (await import('../public/js/templates/fbif.js?test=' + Date.now())).default;
const moteImageUrl = 'https://fbif-feishu-base.oss-cn-shanghai.aliyuncs.com/fbif-attachment-to-url/2026/05/tblMQeXvSGd7Hebf_-CkOuPdpNzb-_3u9Rxc5KA_1778028720411/img_v3_0211e_8b79127d-72a5-4011-9f01-fe3b30c7548g_1778028720565.png';

function p(text, extra = {}) {
  return {
    text,
    runs: text ? [{ type: 'txt', text }] : [],
    isEmpty: !String(text || '').trim(),
    ...extra,
  };
}

function h(text) {
  return p(text, { hasHeadingStyle: true });
}

function docxBody(text, extra = {}) {
  return p(text, {
    spacing: { before: '120', after: '120', line: '288' },
    ...extra,
  });
}

function renderDocx(paragraphs, opts = {}) {
  return template.processDocx({
    paragraphs,
    imgCache: opts.imgCache || {},
    isRepost: !!opts.isRepost,
  }).lines.join('\n');
}

function txtBlock(text) {
  return { k: 'txt', runs: [{ type: 'txt', text }] };
}

function renderBlocks(blocks, opts = {}) {
  return template.processBlocks({
    title: opts.title || '测试文章',
    blocks,
  }, { isRepost: opts.isRepost !== false }).lines.join('\n');
}

test('docx lead author starts the article and hides front matter', () => {
  const html = renderDocx([
    p('不应显示的标题'),
    h('头图'),
    h('推荐语'),
    p('不应显示的推荐语'),
    h('正文'),
    p('作者：Mote莫特'),
    p('第一段内容'),
    h('参考来源'),
    p('[1] ALDI source'),
  ]);

  assert.match(html, /class="wx-ta wx-ta-author">作者：Mote莫特/);
  assert.equal((html.match(/作者：Mote莫特/g) || []).length, 1);
  assert.doesNotMatch(html, /不应显示的标题|不应显示的推荐语|>头图<|>推荐语<|>正文</);
  assert.match(html, /第一段内容/);

  const imageIdx = html.indexOf(moteImageUrl);
  const refIdx = html.indexOf('参考来源：');
  assert.ok(imageIdx >= 0, 'Mote image should be inserted');
  assert.ok(refIdx > imageIdx, 'Mote image should appear above reference heading');
});

test('docx Mote author accepts the spaced name form', () => {
  const html = renderDocx([
    p('作者：Mote 莫特'),
    p('正文内容'),
    h('参考来源'),
    p('[1] source'),
  ]);

  assert.match(html, /作者：Mote 莫特/);
  assert.match(html, new RegExp(moteImageUrl.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
});

test('behavior rules control author label and Mote image insertion', () => {
  const previous = globalThis.window._activeConfig;
  globalThis.window._activeConfig = {
    config: {},
    behavior: {
      actions: [],
      templates: {
        author_line: '撰文：{{ article.author }}',
      },
    },
  };
  try {
    const html = renderDocx([
      p('作者：Mote莫特'),
      p('正文内容'),
      h('参考来源'),
      p('[1] source'),
    ]);

    assert.match(html, /撰文：Mote莫特/);
    assert.doesNotMatch(html, /作者：Mote莫特/);
    assert.doesNotMatch(html, new RegExp(moteImageUrl.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  } finally {
    globalThis.window._activeConfig = previous;
  }
});

test('behavior image URL is escaped before rendering into img attributes', () => {
  const previous = globalThis.window._activeConfig;
  globalThis.window._activeConfig = {
    config: {},
    behavior: {
      params: {
        mote_ref_image_url: {
          type: 'url',
          required: true,
          default: 'https://example.com/a.png" onerror="alert(1)',
        },
      },
    },
  };
  try {
    const html = renderDocx([
      p('作者：Mote莫特'),
      p('正文内容'),
      h('参考来源'),
      p('[1] source'),
    ]);

    assert.match(html, /src="https:\/\/example\.com\/a\.png&quot; onerror=&quot;alert\(1\)"/);
    assert.doesNotMatch(html, /src="[^"]*"\s+onerror=/i);
  } finally {
    globalThis.window._activeConfig = previous;
  }
});

test('docx consecutive body paragraphs stay compact until a blank paragraph separates them', () => {
  const html = renderDocx([
    docxBody('作者：张三'),
    docxBody('Tea Forté的设计太棒了，去它的官网、Facebook或者Instagram上欣赏它的设计吧：'),
    docxBody('www.teaforte.com'),
    docxBody('www.facebook.com/teaforteofficial/photos'),
    docxBody('www.instagram.com/teaforteofficial/'),
    docxBody(''),
    docxBody('这是我茶叶系列第9篇。下一篇我写美国最大的本土茶叶品牌Bigelow Tea。'),
  ]);
  const classesByText = Object.fromEntries([...html.matchAll(/<section class="([^"]+)">([\s\S]*?)<\/section>/g)]
    .map((m) => [m[2].replace(/<[^>]+>/g, ''), m[1]]));

  assert.equal(classesByText['Tea Forté的设计太棒了，去它的官网、Facebook或者Instagram上欣赏它的设计吧：'], 'wx-p');
  assert.equal(classesByText['www.teaforte.com'], 'wx-p');
  assert.equal(classesByText['www.facebook.com/teaforteofficial/photos'], 'wx-p');
  assert.equal(classesByText['www.instagram.com/teaforteofficial/'], 'wx-p wx-gap');
  assert.equal(classesByText['这是我茶叶系列第9篇。下一篇我写美国最大的本土茶叶品牌Bigelow Tea。'], 'wx-p wx-gap');
});

test('original author color matches RGB 57,111,188', async () => {
  const css = await fs.readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
  assert.match(css, /\.wx-ta-author\s*\{\s*color:\s*#396FBC;\s*font-style:\s*normal;\s*\}/);
});

test('text styles carry explicit line-height for WeChat paste', async () => {
  const css = await fs.readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
  for (const cls of ['wx-t', 'wx-th', 'wx-tc', 'wx-tr', 'wx-ta', 'wx-auth', 'wx-a']) {
    assert.match(css, new RegExp(`\\.${cls}\\s*\\{[^}]*line-height:\\s*var\\(--wx-line-height\\)`));
  }
});

test('article images render as zero-line-height blocks for manual copy', async () => {
  const css = await fs.readFile(new URL('../public/css/wx-theme.css', import.meta.url), 'utf8');
  const html = renderDocx([
    p('作者：Mote莫特'),
    { text: '', runs: [{ type: 'img', file: 'pic.png', w: '100%' }], isEmpty: false, hasImg: true },
  ], { imgCache: { 'pic.png': 'https://example.com/pic.png' } });

  assert.match(css, /\.wx-pi\s*\{[^}]*line-height:\s*0;[^}]*font-size:\s*0;/);
  assert.match(html, /<section class="wx-pi/);
  assert.match(html, /display: block; margin: 0 auto; vertical-align: top/);
});

test('url-import image source captions render with muted caption styling', () => {
  const html = renderBlocks([
    { k: 'img', src: 'https://example.com/a.jpg' },
    { k: 'cap', text: '星巴克高蛋白咖啡（美国市场）' },
    { k: 'cap', text: '图片来源：Facebook@Emily Velasco' },
  ]);

  assert.match(html, /class="wx-tc">图片来源：Facebook@Emily Velasco/);
  assert.doesNotMatch(html, /class="wx-t">图片来源：Facebook@Emily Velasco/);
});

test('url-import attribution is emitted once at article tail before the standard footer', () => {
  const html = renderBlocks([
    txtBlock('正文第一段'),
    txtBlock('来源：剁椒Spicy'),
    txtBlock('作者：亚娜'),
    txtBlock('提示：'),
    txtBlock('* 本文为转载。'),
  ]);

  assert.equal((html.match(/来源：剁椒Spicy/g) || []).length, 1);
  assert.equal((html.match(/作者：亚娜/g) || []).length, 1);
  assert.doesNotMatch(html, /提示：/);
  assert.ok(html.indexOf('正文第一段') < html.indexOf('来源：剁椒Spicy'));
  assert.ok(html.indexOf('来源：剁椒Spicy') < html.indexOf('作者：亚娜'));
});

test('url-import keeps body prompt sections that are not the standard footer', () => {
  const html = renderBlocks([
    txtBlock('来源：剁椒Spicy'),
    txtBlock('提示：'),
    txtBlock('这里是正文里的提示内容，不是页脚。'),
  ]);

  assert.match(html, /提示：/);
  assert.match(html, /这里是正文里的提示内容/);
  assert.equal((html.match(/来源：剁椒Spicy/g) || []).length, 1);
});

// ---- 统一表格样式（公众号渲染：全 inline-style，css-inline 不碰裸 table） ----
// WHY：旧路径表格被拍平丢结构；这里锁住公众号侧渲成真正的 <table>，样式默认复刻既有
// 公众号表格（蓝底白字表头 + 浅灰网格线），且全部 inline（复制到微信不丢样式）。
function tablePara(rows) {
  return { text: '', runs: [], isEmpty: false, type: 'table', rows };
}
const tcell = (text, header = false) => ({ runs: [{ type: 'txt', text }], header });

test('公众号 DOCX 表格渲成 inline-style <table>（蓝底白字表头 + 网格线）', () => {
  const html = renderDocx([
    tablePara([
      [tcell('产品名称', true), tcell('价格', true)],
      [tcell('话梅'), tcell('¥11.9')],
    ]),
  ]);
  assert.match(html, /<table[^>]*border-collapse: collapse/, '真正的 table 且 border-collapse');
  assert.match(html, /background-color: #01396a/i, '表头底色默认 FoodTalks 蓝（hex 大小写不敏感）');
  assert.match(html, /color: #ffffff/i, '表头白字');
  assert.match(html, /border: 1px solid #e5e7eb/i, '浅灰网格线');
  assert.match(html, /font-weight: bold/, '表头加粗');
  assert.match(html, /产品名称/);
  assert.match(html, /¥11\.9/);
  // 单元格文本不走 wx-t（那会被 css-inline 套正文色，与表格自有色冲突）
  assert.ok(!/<td[^>]*>[^<]*<span[^>]*class="wx-t"/.test(html), '单元格不用 wx-t class');
});

test('公众号 URL 导入表格块（processBlocks）也渲成 <table>', () => {
  const html = renderBlocks([
    { k: 'table', rows: [
      [tcell('品牌', true), tcell('产地', true)],
      [tcell('SENSE ASIA'), tcell('越南')],
    ] },
  ]);
  assert.match(html, /<table[^>]*border-collapse/);
  assert.match(html, /background-color: #01396a/i);
  assert.match(html, /SENSE ASIA/);
  assert.match(html, /越南/);
});
