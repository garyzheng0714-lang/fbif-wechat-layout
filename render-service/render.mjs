// render-service/render.mjs — headless 复用生产渲染器，Block[] → 各渠道内联样式 HTML。
//
// 桩 globals 后 import 生产模块（与 tests/fbif-template.test.mjs 同款 bootstrap，
// 那是渲染器可 headless 运行的既有证明）。支持渠道见 CHANNELS 表：
//   - fbif（公众号）：流程对齐 engine.js copyContent：processBlocks →
//     renderBottomFooter（bottom_blocks 页脚，转载走 repost 预设）→ 外链降纯文本 →
//     素材绝对化 → processForCopy(内联) → normalizeWechatRichHtml。
//   - ft-default（FoodTalks 网站）：processBlocks(opts.config) 已含 bottom_blocks 页脚 →
//     素材绝对化 → processForCopy(内联)。粘贴目标是 FoodTalks CMS（TinyMCE），
//     不做微信归一化（engine.js isWebsiteTemplate 同语义——归一化会把网站内联 logo
//     误居中），也不降级外链（网站文章保留外链是正常形态）。
// 渠道差异只能以参数进入 CHANNELS 表（docs/adr/2026-07-10-share-vs-isolate-boundary.md
// 四层模型），渲染主流程零 packId 字面量分支；渲染器之间不互相 import（红线 #19）。
// 与网页版人工复制的差异（均为 API 场景刻意为之，理由见 WORKLOG-render-api.md）：
//   - 不追加 copyContent 末尾的零高 <p>（编辑器粘贴光标 hack，draft/add 不需要）
//   - 公众号「更多文章」块默认隐藏：卡片依赖编辑挑选往期文章 + canvas 合成图，headless 无法产出，
//     留空标题反而难看；后续接自动选卡再放开（footerVisibilityOverride 已预留）
//   - 非 mp.weixin.qq.com 的 <a> 降为正文样式纯文本（微信草稿会过滤外链，先降级保版式；仅公众号）
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { DOMParser } from 'linkedom';

// —— globals 桩必须先于渲染模块 import（模块顶层会读 window/localStorage）——
const realFetch = globalThis.fetch;
const storage = {
  getItem() { return null; },
  setItem() {},
  removeItem() {},
};
globalThis.window = {
  __APP_BUILD_INFO: { build: 'render-service' },
  localStorage: storage,
};
globalThis.localStorage = storage;
// mp-blocks / site-blocks 富文本块的 parseDoc 缺省路径依赖 DOMParser（浏览器原生）；Node 用 linkedom 补位。
globalThis.DOMParser = DOMParser;
// 渲染模块内部的 fetch 都是相对路径（浏览器语境），Node 里一律按失败降级处理。
globalThis.fetch = async () => ({ ok: false, text: async () => '', json: async () => ({}) });

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PUBLIC_DIR = process.env.PUBLIC_DIR || path.resolve(__dirname, '../public');
const RULES_API_ORIGIN = (process.env.RULES_API_ORIGIN || 'http://127.0.0.1:9000').replace(/\/$/, '');
// 页脚素材（Bobo 名片等）在规则里是站内相对路径，进公众号草稿/网站 CMS 前必须绝对化到线上域名。
const ASSET_ORIGIN = (process.env.ASSET_ORIGIN || 'https://fbifmp-layout.foodtalks.cn').replace(/\/$/, '');

const mod = (p) => import(pathToFileURL(path.join(PUBLIC_DIR, 'js', p)).href);

// —— 渠道参数表（差异只能以参数进入；渲染主流程按表驱动，不写 if(packId===...) 散落分支）——
//   templatePath          L2 渲染器（按渠道物理隔离，红线 #19）
//   rulesAccount          L3 规则账号（/api/rules/active?account=<id>；缓存按账号分 key）
//   configViaOpts         模板读规则的方式：fbif.js 自行读 window._activeConfig（模板契约），
//                         foodtalks-site.js 读 opts.config（engine.js renderArticleInput 同款）
//   footerViaTemplate     页脚出口：fbif 用 renderBottomFooter 单独渲染拼接；
//                         网站页脚由 render() 内嵌（config.modules.bottom_blocks 主路径，红线 #20/#22）
//   stripExternalAnchors  微信草稿只保留 mp.weixin.qq.com 链接 → 外链降纯文本；网站保留外链
//   normalizeWechat       粘贴目标是微信编辑器才做归一化；FoodTalks CMS 不做（见文件头）
const CHANNELS = {
  fbif: {
    templatePath: 'templates/fbif.js',
    rulesAccount: 'fbif',
    configViaOpts: false,
    footerViaTemplate: true,
    stripExternalAnchors: true,
    normalizeWechat: true,
  },
  'ft-default': {
    templatePath: 'templates/foodtalks-site.js',
    rulesAccount: 'ft-default',
    configViaOpts: true,
    footerViaTemplate: false,
    stripExternalAnchors: false,
    normalizeWechat: false,
  },
};

export const SUPPORTED_PACK_IDS = Object.freeze(Object.keys(CHANNELS));

const templates = {};
for (const [packId, channel] of Object.entries(CHANNELS)) {
  templates[packId] = (await mod(channel.templatePath)).default;
}
const { processForCopy } = await mod('css-inline.js');
const { normalizeWechatRichHtml } = await mod('wechat-normalize.js');
const { DEFAULT_RULE_CONFIG, normalizeRuleConfig, applyActiveRuleConfig } = await mod('rule-presets.js');
const { normalizeRuleBehavior } = await mod('rule-behavior.js');

// 两渠道复制层用同一份主题 CSS：site.html 与 app.html 都引 /css/wx-theme.css，
// engine.js copyContent 对网站输出同样跑 processForCopy（网站 HTML 本就全内联，近似 no-op）。
const themeCSS = await readFile(path.join(PUBLIC_DIR, 'css', 'wx-theme.css'), 'utf8');

// 当前生效规则预设：与网页端 loadActiveRulePreset 同语义——服务端为准、拉不到降级默认。
// 缓存按账号分 key（Map），两个渠道各自 60s 过期，互不污染。
// 注意：这里只取数不碰全局；window._activeConfig 的写入收敛在 applyChannelRuleConfig，
// 且必须在渲染前的同步段执行（避免并发请求间 await 交错互踩全局）。
const _ruleCache = new Map(); // rulesAccount -> { at, preset|null, config }
async function activeRulePreset(account) {
  const hit = _ruleCache.get(account);
  if (hit && Date.now() - hit.at < 60_000) return hit;
  const entry = { at: Date.now(), preset: null, config: normalizeRuleConfig(DEFAULT_RULE_CONFIG) };
  try {
    const resp = await realFetch(RULES_API_ORIGIN + '/api/rules/active?account=' + encodeURIComponent(account), { signal: AbortSignal.timeout(5000) });
    if (resp.ok) {
      const data = await resp.json();
      if (data && data.preset && data.preset.config) {
        entry.preset = {
          ...data.preset,
          raw_config: data.preset.config,
          config: normalizeRuleConfig(data.preset.config),
          behavior: normalizeRuleBehavior(data.preset.behavior),
        };
      }
    }
  } catch (err) {
    console.warn('[render] 规则预设拉取失败（account=' + account + '），使用默认配置（页脚将缺失）：', err.message || err);
  }
  _ruleCache.set(account, entry);
  return entry;
}

// 把该账号规则灌进 window._activeConfig（fbif.js rawActiveModules / getActiveRuleConfig 的数据源），
// 返回渲染用 config（flat + 原始 modules/image 已挂，applyActiveRuleConfig 行为）。
// 拉取失败时显式清空全局——不能留上一账号的 _activeConfig 让 fbif.js 错读别家页脚（跨账号污染）。
function applyChannelRuleConfig(entry) {
  if (entry.preset) return applyActiveRuleConfig(entry.preset.raw_config, entry.preset);
  globalThis.window._activeConfig = undefined;
  return entry.config;
}

// 网站渠道渲染 config：site.html syncEngineConfig 的 headless 等价物——flat 参数 + behavior +
// 原始结构化 modules（bottom_blocks/fbif_footer/asset_library 走 raw，normalizeRuleConfig 会丢它们）。
// 无 localStorage 单篇覆盖 → 服务端默认即最终态（页脚显隐由服务端 bottom_blocks.enabled 决定）。
function siteEngineConfig(entry, { withFooter }) {
  const flat = applyChannelRuleConfig(entry);
  const rawModules = (flat && typeof flat.modules === 'object') ? flat.modules : {};
  // mctoc 锚点 id 强制关：与 site.html 同口径（prevModules.mctoc || {enabled:false}，用户
  // 2026-05-30 确认 FoodTalks 后台自己生成目录锚点），且 genMctocId 随机、开了输出不可复现。
  const modules = { ...rawModules, mctoc: { enabled: false } };
  if (withFooter === false) {
    // withFooter=false = 整组页脚不要：底部区块（bottom_blocks）与页脚库（fbif_footer，
    // 其 tip 片段开关同时驱动行为层「补网站提示声明」）一起摘除。例外：正文带公众号
    // 导流尾被切时，行为层仍会补 tip（cutWechatTip 路径——版权声明不能随切尾丢失）。
    delete modules.bottom_blocks;
    delete modules.fbif_footer;
  }
  return { ...flat, behavior: (entry.preset && entry.preset.behavior) || {}, modules };
}

// 微信草稿只保留 mp.weixin.qq.com 链接，其余 <a> 会被过滤破坏版式——渲染期即降级：
// 保留文字、套正文 leaf 样式（wx-t 随后被 processForCopy 内联），链接本体丢弃。
// 原文链接由调用方走草稿 content_source_url 字段承载。仅公众号渠道启用（CHANNELS 表）。
export function stripExternalAnchors(html) {
  return html.replace(/<a\b([^>]*)>([\s\S]*?)<\/a>/gi, (m, attrs, inner) => {
    const href = (attrs.match(/\bhref="([^"]*)"/i) || [])[1] || '';
    if (/^https?:\/\/mp\.weixin\.qq\.com\//i.test(href)) return m;
    return '<span textstyle="" class="wx-t">' + inner + '</span>';
  });
}

// 页脚素材相对路径绝对化：规则里的 /assets/... 引用只在排版站内有效，
// 进公众号草稿/网站 CMS 前补全为线上域名（随后由发布方逐图转存到目标图床）。
export function absolutizeAssetURLs(html, origin = ASSET_ORIGIN) {
  return html.replace(/src="\/(?!\/)/g, 'src="' + origin + '/');
}

// renderArticle({packId, title, source, blocks, isRepost, withFooter}) → { html, imgN, headingN }
// blocks 契约 = api/wechat_blocks.go 的 Block JSON（/api/fetch-article、/api/html-to-blocks 的输出）。
// packId（默认 fbif）：按 CHANNELS 表选渲染器与渠道参数；未知 packId 显式 400。
// withFooter（默认开）：按生产规则 bottom_blocks 渲染整段页脚——与网页版人工排版同一数据源、
// 同一渲染器（公众号转载走 repost 预设：提示声明 + Bobo 名片等）。
export async function renderArticle({ packId = 'fbif', title, source, blocks, isRepost = true, withFooter = true } = {}) {
  const channel = CHANNELS[packId];
  if (!channel) {
    throw Object.assign(new Error('暂只支持 packId=' + SUPPORTED_PACK_IDS.join(' / ') + '，收到：' + packId), { status: 400 });
  }
  if (!Array.isArray(blocks) || blocks.length === 0) {
    throw Object.assign(new Error('blocks 不能为空'), { status: 400 });
  }
  const entry = await activeRulePreset(channel.rulesAccount);
  // —— 以下到函数尾全部同步：window._activeConfig 是进程级全局，写入与消费之间不得有 await ——
  const template = templates[packId];
  const config = channel.configViaOpts
    ? siteEngineConfig(entry, { withFooter })
    : applyChannelRuleConfig(entry);
  const processOpts = { isRepost: isRepost !== false };
  if (channel.configViaOpts) processOpts.config = config;
  const result = template.processBlocks(
    { title: title || '', source: source || '', blocks },
    processOpts,
  );
  let html = result.lines.join('\n');
  if (channel.footerViaTemplate && withFooter !== false) {
    const footer = template.renderBottomFooter({
      isRepost: isRepost !== false,
      articleMeta: result.articleMeta,
      // 「更多文章」需编辑挑卡片 + canvas 合成图，headless 产不出内容——隐藏而非留空标题
      footerVisibilityOverride: { blocks: { more_articles: false } },
    });
    if (footer && footer.html) {
      html = html + '\n' + footer.html;
    }
  }
  html = absolutizeAssetURLs(html);
  if (channel.stripExternalAnchors) html = stripExternalAnchors(html);
  html = processForCopy(html, themeCSS, config);
  if (channel.normalizeWechat) html = normalizeWechatRichHtml(html);
  return { html, imgN: result.imgN, headingN: result.headingN };
}
