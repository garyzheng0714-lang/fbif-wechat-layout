// render-service/layout.mjs — 排版接口 v1（Go 的 /api/layout/v1/*）背后的排版程序。
//
// 做法：在 Node 里加载网页编辑器自己的代码（engine.js、模板、规则、拆 Word），照网页上「导入一篇 → 点复制」
// 那几步调同一批函数，所以接口排出来的和编辑在网页上复制到的逐字一样（tests/layout-parity.test.mjs 拿网页抓的
// 样稿逐字比）。只有两处不同：图片地址（这里不转存，Go 转存后换成 OSS 地址）、公众号的「更多文章」不出
// （卡片要编辑挑、要画布合成，同网页上把这一块关掉）。
//
// 网页上哪一步对应这里哪一行：
//   粘贴链接   editor-shell.js createUrlImportHandler → _handleBlocks(isRepost=true)；FoodTalks 先 extractSiteSummary 摘走导语
//   上传 Word  handleFile → parseDocx → processDocx（isRepost=false）
//   上传 .md   handleFile → processMd；阅读模式抓回的 Markdown 也走这里（isRepost=true）
//   粘贴文字   _handleText：没有图片链接逐行排正文、不加页脚（行首记号与行内链接、加粗照 Markdown 认），有图片链接按 Markdown 排
//
// 和旧的 render.mjs（/render，发布流水线与 Feed 在用）是两个进程，互不 import、全局互不影响。
// 全局状态（规则、模板、localStorage）按篇重置，所以一次只排一篇：layout() 自己排队。
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { DOMParser as HTMLDOMParser } from 'linkedom';
import { DOMParser as XMLDOMParser } from '@xmldom/xmldom';
import JSZip from 'jszip';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// 出错时回给 Go 的 code（Go 那边 layoutNodeErrorCodes 一一对应）。
export class LayoutError extends Error {
  constructor(code, message, status = 422) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

// 每篇一个干净的 localStorage：等于编辑新开一个标签页，没有上一篇的手改。
function memoryStorage() {
  const items = new Map();
  return {
    getItem: key => (items.has(String(key)) ? items.get(String(key)) : null),
    setItem: (key, value) => { items.set(String(key), String(value)); },
    removeItem: key => { items.delete(String(key)); },
    clear: () => { items.clear(); },
    key: index => Array.from(items.keys())[index] ?? null,
    get length() { return items.size; },
  };
}

// 浏览器的 DOMParser：网页（text/html）用 linkedom，Word 里的 XML 用 xmldom（命名空间、localName 都对）。
class LayoutDOMParser {
  parseFromString(source, type) {
    if (/xml/i.test(String(type || ''))) {
      const doc = new XMLDOMParser().parseFromString(source, type);
      doc.querySelectorAll = selector => Array.from(doc.getElementsByTagName(selector));
      return doc;
    }
    return new HTMLDOMParser().parseFromString(source, type);
  }
}

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.gif': 'image/gif',
  '.webp': 'image/webp',
  '.svg': 'image/svg+xml',
};

// 网页代码里的 fetch 都是站内地址（相对路径，或模板用页面域名补全的 /assets 图）：静态文件从 PUBLIC_DIR 读，
// 规则（/api/rules/active）交给 fetchRules，其余一律失败（网页上那些调用在这里都不该发生）。
function makeFetch({ publicDir, fetchRules, origin }) {
  const root = path.resolve(publicDir);
  return async function layoutFetch(input) {
    const raw = typeof input === 'string' ? input : String(input && input.url || '');
    const url = new URL(raw, origin);
    if (url.origin !== origin) return new Response('', { status: 404 });
    if (url.pathname === '/api/rules/active') {
      const account = url.searchParams.get('account') || '';
      const body = await fetchRules(account);
      return new Response(body, { status: 200, headers: { 'Content-Type': 'application/json; charset=utf-8' } });
    }
    if (url.pathname.startsWith('/api/')) return new Response('', { status: 404 });
    const file = path.resolve(root, '.' + decodeURIComponent(url.pathname));
    if (!file.startsWith(root + path.sep)) return new Response('', { status: 404 });
    try {
      const bytes = await readFile(file);
      return new Response(bytes, {
        status: 200,
        headers: { 'Content-Type': MIME[path.extname(file).toLowerCase()] || 'application/octet-stream' },
      });
    } catch {
      return new Response('', { status: 404 });
    }
  };
}

// 各渠道只在这张表里不同（四层边界：差异参数化）。
//   hideMoreArticles  公众号底部的「更多文章」整块关掉（卡片要编辑挑、要画布合成）
//   consumeSummary    粘贴链接时先把开头导语摘成摘要、从正文拿掉（site.html onBlocks）
const CHANNELS = {
  mp: { hideMoreArticles: true, consumeSummary: false },
  ft: { hideMoreArticles: false, consumeSummary: true },
};

function hasText(value) {
  return typeof value === 'string' && value.trim() !== '';
}

// loadLayoutEngine 每个进程调一次：装好全局、加载网页代码，回一个 layout(request) 函数。
//   publicDir   网页静态目录（生产 /var/www/wechat-layout）
//   fetchRules  (account) => 规则接口 /api/rules/active 回的 JSON 文本；拿不到就抛错
//   origin      网页所在的域名（生产 https://fbifmp-layout.foodtalks.cn）：模板拿它补全站内图片地址，和网页上一样
//   build       回给调用方的版本号
export async function loadLayoutEngine({ publicDir, fetchRules, origin, build = 'development' }) {
  const pageOrigin = new URL(origin).origin;
  const location = new URL(pageOrigin + '/');
  let storage = memoryStorage();
  const storageProxy = {
    getItem: key => storage.getItem(key),
    setItem: (key, value) => storage.setItem(key, value),
    removeItem: key => storage.removeItem(key),
    clear: () => storage.clear(),
    key: index => storage.key(index),
    get length() { return storage.length; },
  };
  globalThis.window = {
    __APP_BUILD_INFO: { build },
    localStorage: storageProxy,
    sessionStorage: storageProxy,
    location,
  };
  globalThis.location = location;
  globalThis.localStorage = storageProxy;
  globalThis.sessionStorage = storageProxy;
  globalThis.DOMParser = LayoutDOMParser;
  globalThis.JSZip = JSZip;
  globalThis.fetch = makeFetch({ publicDir, fetchRules, origin: pageOrigin });

  const mod = p => import(pathToFileURL(path.join(publicDir, 'js', p)).href);
  const engine = await mod('engine.js');
  const { applyActiveRuleConfig, loadActiveRulePreset, normalizeRuleConfig } = await mod('rule-presets.js');
  const { getRulePack, RULE_PACKS } = await mod('rule-packs/registry.js');
  const { mpEngineConfig, siteEngineConfig, ruleModulesOf } = await mod('engine-config.js');
  const { readFooterPanelState } = await mod('site-footer-state.js');
  const { extractSiteSummary } = await mod('editor-shell.js');
  const templates = new Map();
  async function templateFor(id) {
    if (!templates.has(id)) templates.set(id, (await mod('templates/' + id + '.js')).default);
    return templates.get(id);
  }

  // 等于页面打开这个账号：拉规则（只认服务端的）→ applyActiveRuleConfig → syncWorkflowControls/syncEngineConfig。
  async function openAccount(pack) {
    storage = memoryStorage();
    window._activeRulePack = pack;
    window._activeConfig = undefined;
    window._bottomBlocksOverride = undefined;
    const loaded = await loadActiveRulePreset(pack.id);
    if (loaded.source !== 'server') {
      throw new LayoutError('rules_unavailable', '读不到这个账号的排版规则：' + (loaded.error || '规则服务没回'), 503);
    }
    applyActiveRuleConfig(loaded.preset.config, loaded.preset);
    const flat = normalizeRuleConfig(window._activeConfig.config);
    const behavior = window._activeConfig.behavior || {};
    if (pack.channel === 'ft') {
      const modules = ruleModulesOf(window._activeConfig);
      const footerLib = (modules.fbif_footer && typeof modules.fbif_footer === 'object') ? modules.fbif_footer : {};
      const footerState = readFooterPanelState({ storage: storageProxy, accountId: pack.id, footerLib });
      engine.setActiveConfig(siteEngineConfig({ flat, behavior, modules, footerState }));
    } else {
      engine.setActiveConfig(mpEngineConfig({ flat, behavior }));
    }
    return {
      preset: loaded.preset,
      footerChecked: flat.footer_enabled === true,
    };
  }

  // 载体 → 网页同一个入口会交给渲染器的 input。
  async function articleInput(carrier, { repost, title, channel }) {
    const linkImport = hasText(carrier.fetch_meta && carrier.fetch_meta.source);
    if (carrier.kind === 'blocks') {
      const blocks = Array.isArray(carrier.blocks) ? carrier.blocks.map(block => ({ ...block })) : [];
      if (!blocks.length) throw new LayoutError('empty_article', '没有正文段落');
      const summary = channel.consumeSummary ? extractSiteSummary(blocks, { consume: true }) : '';
      const data = { title: hasText(title) ? title : String(carrier.title || ''), blocks };
      if (hasText(carrier.source)) data.source = carrier.source;
      if (hasText(carrier.source_name)) data.source_name = carrier.source_name.trim();
      if (hasText(carrier.byline)) data.byline = carrier.byline.trim();
      const provenance = carrier.fetch_meta && carrier.fetch_meta.provenance;
      if (provenance && typeof provenance === 'object') data.provenance = provenance;
      const isRepost = repost == null ? linkImport : !!repost;
      return { input: { kind: 'blocks', payload: data, isRepost, title: data.title, bodyOnly: false }, summary };
    }
    if (carrier.kind === 'docx') {
      const bytes = Buffer.from(String(carrier.docx_base64 || ''), 'base64');
      if (!bytes.length) throw new LayoutError('bad_request', '没有 Word 内容', 400);
      let payload;
      try {
        payload = await engine.parseDocx(bytes);
      } catch (err) {
        throw new LayoutError('word_unreadable', '这份 Word 读不出来：' + String(err && err.message || err));
      }
      const fileTitle = String(carrier.file_name || '').replace(/\.\w+$/i, '');
      const docTitle = hasText(title) ? title : (hasText(carrier.title) ? carrier.title : fileTitle);
      return { input: { kind: 'docx', payload, isRepost: !!repost, title: docTitle, sourceIsDocx: true }, summary: '' };
    }
    if (carrier.kind === 'markdown') {
      const text = String(carrier.text || '');
      if (!text.trim()) throw new LayoutError('empty_article', 'Markdown 是空的');
      const isRepost = repost == null ? linkImport : !!repost;
      const mdTitle = hasText(title) ? title : String(carrier.title || String(carrier.file_name || '').replace(/\.\w+$/i, ''));
      return { input: { kind: 'md', payload: text, isRepost, title: mdTitle }, summary: '' };
    }
    if (carrier.kind === 'text') {
      const raw = String(carrier.text || '');
      const body = engine.normalizeClipboardTextForMarkdown(raw);
      if (!body) throw new LayoutError('empty_article', '文字是空的');
      const isRepost = !!repost;
      if (!engine.hasImageLikeLink(body)) {
        const data = engine.buildClipboardArticleBlocks(raw);
        return { input: { kind: 'blocks', payload: data, isRepost, title: data.title, bodyOnly: true }, summary: '' };
      }
      return { input: { kind: 'md', payload: body, isRepost, title: hasText(title) ? title : '' }, summary: '' };
    }
    throw new LayoutError('unsupported_input', '不认识的载体：' + String(carrier.kind), 400);
  }

  async function layoutOne(request) {
    const accountID = String(request && request.account || '');
    if (!Object.prototype.hasOwnProperty.call(RULE_PACKS, accountID)) {
      throw new LayoutError('unknown_account', '没有这个账号：' + accountID, 400);
    }
    const carrier = request.input;
    if (!carrier || typeof carrier !== 'object') throw new LayoutError('bad_request', '缺 input', 400);
    const pack = getRulePack(accountID);
    const channel = CHANNELS[pack.channel] || CHANNELS.mp;
    const opened = await openAccount(pack);
    const { input, summary } = await articleInput(carrier, {
      repost: request.repost == null ? null : !!request.repost,
      title: request.title,
      channel,
    });
    const template = await templateFor(pack.templateId);
    const out = await engine.layoutArticleForCopy(template, input, {
      autofillAuthorCard: true,
      footerVisibilityOverride: channel.hideMoreArticles ? { blocks: { more_articles: false } } : undefined,
      footerChecked: opened.footerChecked,
    });
    if (!out.hasBody) throw new LayoutError('empty_article', '排出来没有正文');
    const images = [];
    for (const match of out.html.matchAll(/<img\b[^>]*?\ssrc="([^"]*)"/gi)) images.push(match[1]);
    const preset = opened.preset || {};
    return {
      html: out.html,
      title: hasText(request.title) ? request.title : out.title,
      summary: summary || '',
      repost: out.isRepost,
      footer: out.footer,
      images,
      stats: { images: out.imageCount, headings: out.headingCount },
      rule: {
        preset_id: String(preset.id || ''),
        revision: Number(preset.version || 0),
        config_hash: String(preset.config_hash || ''),
      },
      build,
      output_hash: 'sha256:' + createHash('sha256').update(out.html).digest('hex'),
      warnings: [],
    };
  }

  let queue = Promise.resolve();
  return {
    layout(request) {
      const run = queue.then(() => layoutOne(request));
      queue = run.catch(() => {});
      return run;
    },
  };
}

export async function rendererBuild(publicDir) {
  const fromEnv = String(process.env.RENDER_BUILD_SHA || '').trim();
  if (fromEnv) return fromEnv;
  try {
    const version = JSON.parse(await readFile(path.join(publicDir, 'version.json'), 'utf8'));
    return String(version.build || version.commit || '').trim() || 'development';
  } catch {
    return 'development';
  }
}

export const DEFAULT_PUBLIC_DIR = path.resolve(__dirname, '../public');
