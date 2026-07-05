// render-service/render.mjs — headless 复用生产渲染器，Block[] → 微信内联样式 HTML。
//
// 桩 globals 后 import 生产模块（与 tests/fbif-template.test.mjs 同款 bootstrap，
// 那是渲染器可 headless 运行的既有证明）。渲染流程对齐 engine.js copyContent：
// processBlocks → renderBottomFooter（bottom_blocks 页脚，转载走 repost 预设）→
// 外链降纯文本 → 素材相对路径绝对化 → processForCopy(内联) → normalizeWechatRichHtml。
// 与网页版复制的差异（均为 API 场景刻意为之，理由见 WORKLOG-render-api.md）：
//   - 不追加 copyContent 末尾的零高 <p>（编辑器粘贴光标 hack，draft/add 不需要）
//   - 「更多文章」块默认隐藏：卡片依赖编辑挑选往期文章 + canvas 合成图，headless 无法产出，
//     留空标题反而难看；后续接自动选卡再放开（footerVisibilityOverride 已预留）
//   - 非 mp.weixin.qq.com 的 <a> 降为正文样式纯文本（微信草稿会过滤外链，先降级保版式）
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
// mp-blocks 富文本块的 parseDoc 缺省路径依赖 DOMParser（浏览器原生）；Node 用 linkedom 补位。
globalThis.DOMParser = DOMParser;
// 渲染模块内部的 fetch 都是相对路径（浏览器语境），Node 里一律按失败降级处理。
globalThis.fetch = async () => ({ ok: false, text: async () => '', json: async () => ({}) });

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const PUBLIC_DIR = process.env.PUBLIC_DIR || path.resolve(__dirname, '../public');
const RULES_API_ORIGIN = (process.env.RULES_API_ORIGIN || 'http://127.0.0.1:9000').replace(/\/$/, '');
// 页脚素材（Bobo 名片等）在规则里是站内相对路径，进公众号草稿前必须绝对化到线上域名。
const ASSET_ORIGIN = (process.env.ASSET_ORIGIN || 'https://fbifmp-layout.foodtalks.cn').replace(/\/$/, '');

const mod = (p) => import(pathToFileURL(path.join(PUBLIC_DIR, 'js', p)).href);

const template = (await mod('templates/fbif.js')).default;
const { processForCopy } = await mod('css-inline.js');
const { normalizeWechatRichHtml } = await mod('wechat-normalize.js');
const { DEFAULT_RULE_CONFIG, normalizeRuleConfig, applyActiveRuleConfig } = await mod('rule-presets.js');
const { normalizeRuleBehavior } = await mod('rule-behavior.js');

const themeCSS = await readFile(path.join(PUBLIC_DIR, 'css', 'wx-theme.css'), 'utf8');

// 当前生效规则预设：与网页端 loadActiveRulePreset + applyActiveRuleConfig 同语义——
// 服务端为准、拉不到降级默认。除返回 config（processForCopy 用），还把完整 preset
// 灌进 window._activeConfig，让 fbif.js 的 rawActiveModules() 能读到 bottom_blocks/
// asset_library 等结构化模块（页脚渲染的数据源）。60s 缓存。
let _configCache = { at: 0, config: null };
async function activeRuleConfig() {
  const now = Date.now();
  if (_configCache.config && now - _configCache.at < 60_000) return _configCache.config;
  let config = normalizeRuleConfig(DEFAULT_RULE_CONFIG);
  try {
    const resp = await realFetch(RULES_API_ORIGIN + '/api/rules/active?account=fbif', { signal: AbortSignal.timeout(5000) });
    if (resp.ok) {
      const data = await resp.json();
      if (data && data.preset && data.preset.config) {
        const preset = {
          ...data.preset,
          raw_config: data.preset.config,
          config: normalizeRuleConfig(data.preset.config),
          behavior: normalizeRuleBehavior(data.preset.behavior),
        };
        config = applyActiveRuleConfig(data.preset.config, preset);
      }
    }
  } catch (err) {
    console.warn('[render] 规则预设拉取失败，使用默认配置（页脚将缺失）：', err.message || err);
  }
  _configCache = { at: now, config };
  return config;
}

// 微信草稿只保留 mp.weixin.qq.com 链接，其余 <a> 会被过滤破坏版式——渲染期即降级：
// 保留文字、套正文 leaf 样式（wx-t 随后被 processForCopy 内联），链接本体丢弃。
// 原文链接由调用方走草稿 content_source_url 字段承载。
export function stripExternalAnchors(html) {
  return html.replace(/<a\b([^>]*)>([\s\S]*?)<\/a>/gi, (m, attrs, inner) => {
    const href = (attrs.match(/\bhref="([^"]*)"/i) || [])[1] || '';
    if (/^https?:\/\/mp\.weixin\.qq\.com\//i.test(href)) return m;
    return '<span textstyle="" class="wx-t">' + inner + '</span>';
  });
}

// 页脚素材相对路径绝对化：规则里的 /assets/... 引用只在排版站内有效，
// 进公众号草稿前补全为线上域名（随后由发布方逐图转存到微信图床）。
export function absolutizeAssetURLs(html, origin = ASSET_ORIGIN) {
  return html.replace(/src="\/(?!\/)/g, 'src="' + origin + '/');
}

// renderArticle({title, source, blocks, isRepost, withFooter}) → { html, imgN, headingN }
// blocks 契约 = api/wechat_blocks.go 的 Block JSON（/api/fetch-article、/api/html-to-blocks 的输出）。
// withFooter（默认开）：按生产规则 bottom_blocks 渲染整段页脚——转载走 repost 预设
//（提示声明 + Bobo 名片等），与网页版人工排版同一数据源、同一渲染器。
export async function renderArticle({ title, source, blocks, isRepost = true, withFooter = true } = {}) {
  if (!Array.isArray(blocks) || blocks.length === 0) {
    throw Object.assign(new Error('blocks 不能为空'), { status: 400 });
  }
  const config = await activeRuleConfig();
  const result = template.processBlocks(
    { title: title || '', source: source || '', blocks },
    { isRepost: isRepost !== false },
  );
  let html = result.lines.join('\n');
  if (withFooter !== false) {
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
  html = stripExternalAnchors(html);
  html = processForCopy(html, themeCSS, config);
  html = normalizeWechatRichHtml(html);
  return { html, imgN: result.imgN, headingN: result.headingN };
}
