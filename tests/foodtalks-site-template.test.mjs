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

// 旧 M1 契约「图片 600px centered」已被 M2 契约取代：
// 图片现在用 display:block + margin:auto + class="mc-img"，
// 宽度走 adaptiveImageWidth 自适应，不再硬编码 600px style 字符串
// （参见后文 M2 case「图片 wrapper display:block + margin:auto + mc-img」）。

// 旧 M1 契约「图注 12px #7f7f7f」+「参考来源 16px #7f7f7f」已被 M2 契约取代：
// 图注现在用 #888888（参见后文 M2 case「图注颜色 #888888」），
// 参考来源改为 14px #888888 走 ref-header / ref-item 分支。
// 文件头注释也同步更新到 M2，避免 ghost-pass。

// 旧 M1 契约「auto-numbers references with [1] [2] [3]」已被 M2 契约取代：
// 自动编号 ensureReferenceNumber 函数已删除，改为保留作者原始 [n] 标记
// 由行为模板 applyFoodTalksBehavior 识别 ref-header / ref-item（参见后文 M2 case）。

// 图宽规则（用户 2026-05-30 确认，依据基石 news/62285）：竖图(高>宽)400，横图600。
// adaptiveImageWidth 抽到 image-width.js，真实行为单测见 tests/image-width.test.mjs。
test('foodtalks-site imports adaptiveImageWidth from image-width.js', () => {
  assert.match(templateSource, /image-width\.js/);
  assert.match(templateSource, /adaptiveImageWidth/);
});

// classifyBlocks 必须补出图片 height（data-h 或 data-w×data-ratio），否则自适应宽度
// 对 URL 导入恒返回 600（拿不到比例）。
test('foodtalks-site classifyBlocks derives image height from dataH or dataW×dataRatio', () => {
  assert.match(templateSource, /dataRatio/);
  assert.match(templateSource, /dataH/);
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

// 标题识别逻辑（detectHeadingLevel / isBoldHeading / promoteHeuristicHeadings）已抽到
// heading-detect.js，由 tests/heading-detect.test.mjs 做真实行为测试（含加粗段升级、
// 编号/关键词标题、负样本排除）。这里只校验模板确实从该模块引入并应用 promoteHeuristicHeadings。
test('foodtalks-site imports heading detection from heading-detect.js', () => {
  assert.match(templateSource, /heading-detect\.js/);
  assert.match(templateSource, /promoteHeuristicHeadings/);
});

// h2/h3 输出契约（金标准 news/62285）：<h2 style="text-align: justify;">…</h2>，
// 默认不带 id（id 仅在 mctoc 模块开启时注入）。
test('foodtalks-site renderHeading outputs text-align: justify and conditional mctoc id', () => {
  assert.match(templateSource, /style="text-align: justify;">\$\{esc\(text\)\}/);
  assert.match(templateSource, /mctocEnabled\s*\?\s*` id="\$\{genMctocId\(\)\}"`/);
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

test('foodtalks-site DOCX 分类委托共享模块 docx-article-model（与公众号共用结构层）', () => {
  // 旧契约「源码含 stripDocxPreambleBeforeAuthor」已过时：前置剥离等结构分类已下沉到
  // 共享模块 docx-article-model.js（公众号与网站共用一套）。前置剥离/图片取图等行为
  // 改由真实端到端测试 tests/foodtalks-site-docx.test.mjs 覆盖（规则九：测意图非实现）。
  assert.match(templateSource, /classifyDocxArticle/, '必须 import 共享分类函数');
  assert.match(templateSource, /docx-article-model/, '必须 import 自共享数据模块（红线#19：不互相 import 渲染器）');
  const classifyDocxMatch = templateSource.match(/function\s+classifyDocx[\s\S]*?\n\}/);
  assert.ok(classifyDocxMatch, 'classifyDocx function must exist');
  assert.match(classifyDocxMatch[0], /classifyDocxArticle/, 'classifyDocx 必须委托共享模块');
});

test('foodtalks-site DOCX 走网站口径：consumeAuthorLine/classifyReferences 均 false', () => {
  // 红线：结构共用 + 网站口径保留。作者行留正文流交行为层渲署名；参考来源仍由行为层
  // 按 [n] 口径识别——故注入共享模块的开关必须都是 false。
  assert.match(templateSource, /consumeAuthorLine:\s*false/);
  assert.match(templateSource, /classifyReferences:\s*false/);
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

// ========== 颜色规则驱动 + 原生口径修正（2026-05-28） ==========

test('图注: renderImageCaption 读 config.muted_color 且调用点传 config', () => {
  const fn = templateSource.match(/function\s+renderImageCaption\s*\([\s\S]+?\n\}/);
  assert.ok(fn, '找不到 renderImageCaption');
  assert.match(fn[0], /config/, 'renderImageCaption 必须接受 config');
  assert.match(fn[0], /muted_color/, '图注颜色必须读 config.muted_color');
  // 调用点必须传 config（防止"改了签名没改调用"）。用 lines.push(...) 锚定调用，
  // 避免误匹配函数定义；[^;]* 跨括号但不跨语句分号。
  assert.match(templateSource, /lines\.push\(renderImageCaption\([^;]*config/, 'renderImageCaption 调用点必须传 config');
});

test('参考来源: 读 config.muted_color 且不写死 font-size', () => {
  // 锚定 render() 里的分支（'ref-header') {），避开 promoteImageCaptions 里
  // 的 break 条件（e.k === 'ref-header' || e.k === 'ref-item') break;）。
  const head = templateSource.match(/e\.k === 'ref-header'\)\s*\{[\s\S]+?continue;/);
  const item = templateSource.match(/e\.k === 'ref-item'\)\s*\{[\s\S]+?continue;/);
  assert.ok(head && item, '找不到 ref-header / ref-item 分支');
  const both = head[0] + item[0];
  assert.match(both, /muted_color/, '参考颜色必须读 config.muted_color');
  assert.doesNotMatch(both, /font-size\s*:/, '参考来源不能写死任何 font-size（原生继承正文）');
});

test('署名: 读 config.muted_color 且不再 #7f7f7f', () => {
  const fn = templateSource.match(/e\.k === 'author'[\s\S]+?continue;/);
  assert.ok(fn, '找不到 author 分支');
  assert.match(fn[0], /muted_color/, '署名颜色必须读 config.muted_color');
  assert.doesNotMatch(fn[0], /#7f7f7f/i, '署名不能再用 #7f7f7f（搬运痕迹色）');
});

// 页脚库：render 按 config.modules.fbif_footer.segments 输出，每个勾选片段即时生效
test('页脚库: render 末尾优先走统一区块模型(bottom_blocks)，缺省回退 buildFooterHtml', () => {
  // P3：底部统一为 bottom_blocks 区块模型；rich_html 区块复刻原页脚段（字节一致见 site-blocks-fidelity 测试）。
  assert.match(templateSource, /renderBottomBlocksHtml\s*\(\s*bottomBlocks\s*,\s*override\s*,\s*siteBlockRenderers/, 'render 优先用 renderBottomBlocksHtml(bottom_blocks, 单篇override, 网站渲染器)');
  // 回退路径保留：旧规则无 bottom_blocks 时仍走 buildFooterHtml（兼容不炸）。
  assert.match(templateSource, /buildFooterHtml\s*\(\s*footerCfg\.active_segments\s*,\s*footerCfg\s*,\s*mods\.asset_library/, '回退：无 bottom_blocks 时用 buildFooterHtml(勾选, 页脚库内容, 素材库)');
  assert.match(templateSource, /footerCfg\.enabled/, '回退页脚营销段受 enabled 总开关控制');
});

test('页脚库: 提示声明受 segments.tip 控制（=0 不输出，默认保留版权）', () => {
  const tipBranch = templateSource.match(/e\.k === 'tip'[\s\S]{0,500}/);
  assert.ok(tipBranch, '找到 tip 分支');
  assert.match(tipBranch[0], /segments[\s\S]{0,60}tip\s*===\s*0/, '提示声明受 segments.tip===0 控制（取消勾选生效）');
});
