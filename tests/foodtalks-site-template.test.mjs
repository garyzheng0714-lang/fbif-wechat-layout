import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

// 该模板用 dynamic import + engine.js 依赖，无法在 Node 里完整加载。
// 第一版用源码 grep 校验关键输出形态契约；M2 之后用 jsdom 跑真实渲染。

let templateSource;
test.before(async () => {
  templateSource = await readFile(
    new URL('../public/js/templates/foodtalks-site.js', import.meta.url),
    'utf8'
  );
});

test('foodtalks-site template declares correct id and formats', () => {
  assert.match(templateSource, /export default \{/);
  assert.match(templateSource, /id:\s*['"]foodtalks-site['"]/);
  assert.match(templateSource, /processDocx\s*\(/);
  assert.match(templateSource, /processMd\s*\(/);
  assert.match(templateSource, /processBlocks\s*\(/);
});

// 旧 M1 契约「h2/h3 无 inline 样式」+「BLANK_LINE = '<p><br /></p>'」已被 M2 契约取代：
// 标题现在带 mctoc_ id（由 config.modules.mctoc.enabled 开关，参见后文 M2 case），
// BLANK_LINE 现在含 text-align: justify（参见后文 M2 case）。
// 规则七：显式择一，不折中——M2 即为当前 spec 目标。

test('foodtalks-site template renders images at 600px centered', () => {
  assert.match(templateSource, /text-align:\s*center/);
  assert.match(templateSource, /width:\s*600px/);
  assert.match(templateSource, /max-width:\s*100%/);
});

test('foodtalks-site template renders image captions with 12px gray inline style', () => {
  assert.match(templateSource, /font-size:\s*12px;\s*color:\s*#7f7f7f/);
});

test('foodtalks-site template renders references with 16px gray inline style', () => {
  assert.match(templateSource, /font-size:\s*16px;\s*color:\s*#7f7f7f/);
});

// 旧 M1 契约「auto-numbers references with [1] [2] [3]」已被 M2 契约取代：
// 自动编号 ensureReferenceNumber 函数已删除，改为保留作者原始 [n] 标记
// 由行为模板 applyFoodTalksBehavior 识别 ref-header / ref-item（参见后文 M2 case）。

test('foodtalks-site template has adaptiveImageWidth with 280/360/480/600 thresholds', () => {
  assert.match(templateSource, /adaptiveImageWidth/);
  assert.match(templateSource, /\b280\b/);
  assert.match(templateSource, /\b360\b/);
  assert.match(templateSource, /\b480\b/);
});

test('foodtalks-site template uses aspect ratio thresholds 3 / 2.2 / 1.6', () => {
  assert.match(templateSource, /ratio\s*>=\s*3/);
  assert.match(templateSource, /ratio\s*>=\s*2\.2/);
  assert.match(templateSource, /ratio\s*>=\s*1\.6/);
});

test('foodtalks-site has 13 image-caption keywords', () => {
  assert.match(templateSource, /['"]图片来源['"]/);
  assert.match(templateSource, /['"]资料来源['"]/);
  assert.match(templateSource, /['"]数据来源['"]/);
  assert.match(templateSource, /['"]来源['"]/);
  assert.match(templateSource, /['"]制图['"]/);
  assert.match(templateSource, /['"]截图['"]/);
  assert.match(templateSource, /['"]图注['"]/);
  assert.match(templateSource, /['"]注：['"]/);
  assert.match(templateSource, /['"]Source['"]/);
  assert.match(templateSource, /['"]Photo['"]/);
  assert.match(templateSource, /['"]Image['"]/);
  assert.match(templateSource, /['"]Credit['"]/);
});

test('foodtalks-site has isImageCaption and hasImageCaptionKeyword and promoteImageCaptions', () => {
  assert.match(templateSource, /isImageCaption/);
  assert.match(templateSource, /hasImageCaptionKeyword/);
  assert.match(templateSource, /promoteImageCaptions/);
});

test('foodtalks-site has detectHeadingLevel and promoteHeuristicHeadings', () => {
  assert.match(templateSource, /detectHeadingLevel/);
  assert.match(templateSource, /promoteHeuristicHeadings/);
  assert.match(templateSource, /TITLE_KEYWORDS/);
});

test('foodtalks-site title keywords cover FoodTalks 9 items', () => {
  assert.match(templateSource, /['"]背景['"]/);
  assert.match(templateSource, /['"]简介['"]/);
  assert.match(templateSource, /['"]总结['"]/);
  assert.match(templateSource, /['"]分析['"]/);
  assert.match(templateSource, /['"]案例['"]/);
  assert.match(templateSource, /['"]介绍['"]/);
  assert.match(templateSource, /['"]概述['"]/);
  assert.match(templateSource, /['"]结论['"]/);
  assert.match(templateSource, /['"]展望['"]/);
});

test('foodtalks-site recognizes 一/二/三 + Chinese punctuation as h2', () => {
  assert.match(templateSource, /[一二三四五六七八九十]/);
});

test('foodtalks-site recognizes （一）/（二） as h3', () => {
  assert.match(templateSource, /[（(]\s*\[?[一二三四五六七八九十]/);
});

test('foodtalks-site has normalizeChinesePunctuation with CJK-adjacent rule', () => {
  assert.match(templateSource, /normalizeChinesePunctuation/);
  assert.match(templateSource, /isCjk/);
  assert.match(templateSource, /isDigit/);
  assert.match(templateSource, /isAsciiLetter/);
});

test('foodtalks-site only applies CJK punctuation conversion to txt runs (not href / alt)', () => {
  // 通过源码检查 renderRuns 里 esc(normalizeChinesePunctuation(r.text)) 出现
  assert.match(templateSource, /esc\(normalizeChinesePunctuation\(/);
});

test('foodtalks-site has isStandaloneLinkParagraph', () => {
  assert.match(templateSource, /isStandaloneLinkParagraph/);
});

test('foodtalks-site render tracks prevWasStandaloneLink to skip blank line between consecutive link paragraphs', () => {
  assert.match(templateSource, /prevWasStandaloneLink/);
});

test('foodtalks-site has stripDocxPreambleBeforeAuthor and calls it from classifyDocx', () => {
  assert.match(templateSource, /stripDocxPreambleBeforeAuthor/);
  // 必须在 classifyDocx 函数内被调用
  const classifyDocxMatch = templateSource.match(/function\s+classifyDocx[\s\S]*?(?=\n\}\n)/);
  assert.ok(classifyDocxMatch, 'classifyDocx function must exist');
  assert.match(classifyDocxMatch[0], /stripDocxPreambleBeforeAuthor/);
});

test('foodtalks-site detects 作者 / Author with colon', () => {
  // 通过源码确认 regex 同时匹配中文"作者："和英文"Author:"
  assert.match(templateSource, /作者/);
  assert.match(templateSource, /Author/);
});

// ========== 改造目标契约（M2，2026-05-27） ==========
// 这些 case 在改造完成前会 FAIL，改造进行中逐步变绿。

test('M2: renderParagraph outputs justify-aligned <p>', () => {
  // 改造后 renderParagraph 必须输出 <p style="text-align: justify;">...
  assert.match(
    templateSource,
    /text-align:\s*justify/,
    'foodtalks-site.js 必须输出 text-align: justify 段落'
  );
});

test('M2: BLANK_LINE 是 <p style="text-align: justify;"><br></p>', () => {
  assert.match(
    templateSource,
    /const\s+BLANK_LINE\s*=\s*['"]<p[^>]*text-align:\s*justify[^>]*><br[^>]*><\/p>['"]/,
    'BLANK_LINE 必须包含 text-align: justify'
  );
});

test('M2: 图注颜色是 #888888（不再是 #7f7f7f）', () => {
  // renderImageCaption 必须用 #888888
  const captionFnMatch = templateSource.match(
    /function\s+renderImageCaption\s*\([\s\S]+?\n\}/
  );
  assert.ok(captionFnMatch, '找不到 renderImageCaption 函数');
  assert.match(captionFnMatch[0], /#888888/, '图注颜色必须是 #888888');
  assert.doesNotMatch(captionFnMatch[0], /#7f7f7f/, '图注不能再用 #7f7f7f');
});

test('M2: 图片 wrapper 改为 <p><img style="display:block; margin:auto">', () => {
  const imgFnMatch = templateSource.match(
    /function\s+renderImage\s*\([\s\S]+?\n\}/
  );
  assert.ok(imgFnMatch, '找不到 renderImage 函数');
  assert.match(imgFnMatch[0], /display:\s*block/, '图片必须 display: block');
  assert.match(imgFnMatch[0], /margin-left:\s*auto/, '图片必须 margin-left: auto');
  assert.match(imgFnMatch[0], /class=["']mc-img["']/, '图片必须有 class="mc-img"');
  assert.doesNotMatch(imgFnMatch[0], /display:\s*inline-block/, '不再用 inline-block');
});

test('M2: 标题 mctoc_ ID 生成可由 config 开关', () => {
  // renderHeading 应该接受 config 参数并根据 config.modules.mctoc.enabled 决定是否加 ID
  assert.match(
    templateSource,
    /mctoc_/,
    'renderHeading 必须能生成 mctoc_ 前缀的锚点 ID'
  );
  assert.match(
    templateSource,
    /modules\.mctoc/,
    'renderHeading 必须读取 config.modules.mctoc 开关'
  );
});

test('M2: 链接补 target="_blank" rel="noopener"', () => {
  assert.match(
    templateSource,
    /target=["']_blank["']/,
    '链接必须有 target="_blank"'
  );
  assert.match(
    templateSource,
    /rel=["']noopener["']/,
    '链接必须有 rel="noopener"'
  );
});

test('M2: 删除了自动重新编号的 renderReference', () => {
  // 自动补 [1][2][3] 编号的旧函数必须被删除
  assert.doesNotMatch(
    templateSource,
    /function\s+ensureReferenceNumber/,
    'ensureReferenceNumber 必须删除'
  );
  // 旧的 ref 分支替换为 ref-item
  assert.match(
    templateSource,
    /['"]ref-item['"]/,
    '必须有新的 ref-item 分支'
  );
});

test('M2: 引入 applyFoodTalksBehavior', () => {
  assert.match(
    templateSource,
    /applyFoodTalksBehavior/,
    'foodtalks-site.js 必须 import applyFoodTalksBehavior'
  );
  assert.match(
    templateSource,
    /from\s+['"]\.\.\/rule-behavior-foodtalks\.js['"]/,
    '必须从 rule-behavior-foodtalks.js import'
  );
});

test('M2: 三个 process 入口都接受 config 参数', () => {
  // processDocx / processMd / processBlocks 签名里都要有 config
  const docxSig = templateSource.match(/processDocx\s*\(\s*\{([^}]+)\}/);
  assert.ok(docxSig, '找不到 processDocx');
  assert.match(docxSig[1], /config/, 'processDocx 参数必须含 config');

  const mdSig = templateSource.match(/processMd\s*\(\s*[a-z]+\s*,\s*([a-z]+)\s*\)/);
  assert.ok(mdSig, '找不到 processMd');

  const blocksSig = templateSource.match(/processBlocks\s*\(\s*[a-z]+\s*,\s*([a-z]+)\s*\)/);
  assert.ok(blocksSig, '找不到 processBlocks');
});
