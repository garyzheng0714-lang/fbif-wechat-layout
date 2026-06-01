// 共享 DOCX 文章元素模型 —— 公众号(fbif.js)与 FoodTalks 网站(foodtalks-site.js)
// 共用的 DOCX 内容【结构分类】层。把「DOCX 解析后的文章元素模型」收拢成一套，
// 两侧渲染器都消费同一中性 elem 序列，只在最终 HTML 渲染上各自不同（红线#19：
// 两渲染器不互相 import，都只 import 本数据模块）。
//
// 设计边界（关键）：
//   1. 规则源无关——模块内部零全局访问（不读 window / getActiveRuleBehavior / config）。
//      正则与选项一律由调用方从各自规则源（公众号走全局 behavior，网站走 config.behavior）
//      解析成「已编译 RegExp」后通过 opts 注入。
//   2. 只产「中性 elem + author + imgN」，不产 HTML、不决定间距(wx-gap)/署名色/页脚——
//      那些是各自渲染器的局部规则。
//   3. 图片一律用 imgCache[run.file] 取 src（parser.js 产出的图片 run 是 {type:'img', file, w}，
//      没有 id/width/height/alt）。这根治网站此前用 imgCache[imgRun.id] 导致 src 恒空的 bug。
//
// 两处「结构共用、口径分离」的分歧点由 opts 开关控制（用户 2026-06-01 裁定
// 「结构共用 + 网站口径保留」）：
//   - consumeAuthorLine：true（公众号）吃掉首个「作者：」行、作者单独渲染在顶部；
//     false（网站）保留该行在正文流，交给网站行为层 applyFoodTalksBehavior 渲成
//     #888 斜体署名。两者都剥离作者行【之前】的前置材料。
//   - classifyReferences：控制是否应用【公众号 DOCX 内容标记语义】整套——参考来源段头/条目
//     归 refH/ref、「引言」「标题」结构标记跳过、「标题」front-matter 正文起点回退。
//     true（公众号）全应用；false（网站）全不应用：参考段落落为普通 txt 交网站行为层按更
//     保守的 [n] 口径识别，「引言/标题」不跳过/不删（避免内容丢失），正文起点纯由作者行决定。

import { looksLikeGifSource } from './image-utils.mjs';

// 模块内温和兜底默认值（与公众号现有写死值逐字一致；正常路径由 opts 注入覆盖）。
const DEFAULT_AUTHOR_LINE = /^作者[：:]\s*(.+)$/;
const DEFAULT_REF_SECTION = /^(参考(来源|文献|资料|链接)|信息来源)[：:]?$/;
const DEFAULT_REF_HEADING_PREFIX = /^(参考(来源|文献|资料|链接)|信息来源)/;
const DEFAULT_FRONT_MATTER_SCAN_LIMIT = 25;

// 平铺 docx 元数据：#18 空段语义 / 公众号成对紧凑间距(isCompactDocxPair) 的全部输入。
// 共享模块原样透传，渲染器各自决定是否消费（公众号消费、网站当前不消费）。
function docxMeta(pd) {
  return {
    fromDocx: true,
    isList: !!pd.isList,
    styleId: pd.styleId || '',
    styleName: pd.styleName || '',
    contextualSpacing: !!pd.contextualSpacing,
    spacing: pd.spacing || {},
  };
}

// 从一行文本里按注入的正则提取作者名（捕获组 1）。无匹配 / 无捕获返回空串。
export function parseAuthorLineWith(text, regex) {
  const re = regex || DEFAULT_AUTHOR_LINE;
  const m = String(text || '').trim().match(re);
  return m ? (m[1] != null ? m[1].trim() : '') : '';
}

// 把 DOCX 段落数组分类成中性文章元素模型。
//   paragraphs：parser.buildParagraph 产出的段落对象数组
//   imgCache：{ 文件名 -> 可用作 <img src> 的字符串 }
//   opts：{ authorLineRegex, refSectionRegex, refHeadingPrefixRegex,
//           frontMatterScanLimit, consumeAuthorLine, classifyReferences }
// 返回：{ elems, author, imgN }
export function classifyDocxArticle(paragraphs, imgCache, opts = {}) {
  const authorLineRegex = opts.authorLineRegex || DEFAULT_AUTHOR_LINE;
  const refSectionRegex = opts.refSectionRegex || DEFAULT_REF_SECTION;
  const refHeadingPrefixRegex = opts.refHeadingPrefixRegex || DEFAULT_REF_HEADING_PREFIX;
  const frontMatterScanLimit = opts.frontMatterScanLimit || DEFAULT_FRONT_MATTER_SCAN_LIMIT;
  const consumeAuthorLine = opts.consumeAuthorLine !== false; // 默认 true（公众号语义）
  const classifyReferences = opts.classifyReferences !== false; // 默认 true（公众号语义）
  const cache = imgCache || {};

  const pds = (paragraphs || []).map(p => ({
    ...p,
    isHeading: p.hasOutlineLevel || p.hasHeadingStyle,
  }));

  let author = '', contentStart = 0, resolved = false;
  let nonEmptySeen = 0;
  // 首个「作者：」行之前的前置材料是编辑前置材料，不是文章正文。
  for (let i = 0; i < pds.length; i++) {
    if (pds[i].isEmpty) continue;
    if (pds[i].type === 'table') continue; // 表格块不是前置材料，不参与作者行/起点扫描
    nonEmptySeen++;
    // 把「是否作者行（=正文起点/前置剥离触发）」与「捕获到的作者名」解耦：用 .test() 判定
    // 命中，作者名另取。这样「作者：」后无内容（网站正则 (.*) 允许空名）也能触发前置剥离，
    // 与旧 stripDocxPreambleBeforeAuthor「前缀即剥离，不要求有名」一致；公众号正则 (.+)
    // 要求非空名，.test() 对空名行不命中，逐字节维持旧行为。
    if (authorLineRegex.test(String(pds[i].text || '').trim())) {
      author = parseAuthorLineWith(pds[i].text, authorLineRegex);
      contentStart = consumeAuthorLine ? i + 1 : i;
      resolved = true;
      break;
    }
    if (nonEmptySeen >= frontMatterScanLimit) break;
  }

  // 「标题/引言」结构标记的正文起点回退是公众号 DOCX front-matter 约定（属公众号内容标记
  // 语义，与 refH/引言·标题 跳过同源，统一受 classifyReferences 控制）。网站口径
  // （classifyReferences=false）不应用：正文起点纯由作者行决定，与旧 stripDocxPreambleBeforeAuthor
  // 一致，避免误跳/误删 leading「引言」「标题」段（内容丢失）。fbif 默认 true → 逐字节不变。
  if (classifyReferences) {
    // 无作者行时的「标题」启发式正文起点回退。
    for (let i = 0; i < pds.length; i++) {
      if (resolved) break;
      if (!pds[i].isHeading) continue;
      if (pds[i].text.trim().startsWith('标题')) {
        let foundNext = false;
        for (let j = i + 1; j < pds.length; j++) {
          if (pds[j].isHeading) { contentStart = j; foundNext = true; break; }
          const byline = parseAuthorLineWith(pds[j].text, authorLineRegex);
          if (byline) author = byline;
        }
        if (!foundNext) {
          contentStart = i + 1;
          while (contentStart < pds.length && !pds[contentStart].isHeading) contentStart++;
          if (contentStart >= pds.length) contentStart = pds.length;
        }
      }
      break;
    }
    if (contentStart < pds.length && pds[contentStart].isHeading &&
        pds[contentStart].text.trim().replace(/[：:]$/, '') === '引言') contentStart++;
  }

  const elems = [];
  let inRef = false, imgN = 0;
  let blankBefore = false;
  const takeDocxMeta = (pd) => {
    const meta = { ...docxMeta(pd), blankBefore };
    blankBefore = false;
    return meta;
  };

  for (let i = contentStart; i < pds.length; i++) {
    const pd = pds[i];
    if (pd.isEmpty) { if (elems.length) blankBefore = true; continue; }
    if (pd.hasHL && pd.text.includes('插入视频')) continue;

    // 表格块：parser.buildTableBlock 产出的二维 rows 直接透传成中性 k:'table' 元素，
    // 两侧渲染器各自渲成 <table>（红线#19：渲染各写各的，只共享此结构层）。
    if (pd.type === 'table') {
      inRef = false;
      elems.push({ k: 'table', rows: pd.rows || [], ...takeDocxMeta(pd) });
      continue;
    }

    // WPS 导出常把「参考来源：」留成无样式普通段（无 heading 样式 / outlineLvl），
    // 故也按文本内容识别。classifyReferences=false（网站）时不在此分类，让参考段落
    // 落为普通 txt，由网站行为层按自己的 [n] 口径识别。
    const trimmed = pd.text.trim();
    if (classifyReferences && refSectionRegex.test(trimmed)) {
      inRef = true;
      elems.push({ k: 'refH', text: trimmed.replace(/[：:]$/, ''), ...takeDocxMeta(pd) });
      continue;
    }

    if (pd.isHeading) {
      const ht = trimmed.replace(/[：:]$/, '');
      if (classifyReferences) {
        // 公众号语义：跳过「引言」「标题」结构标记；参考段头归 refH。
        if (ht === '引言' || ht === '标题') continue;
        if (refHeadingPrefixRegex.test(ht)) {
          inRef = true;
          elems.push({ k: 'refH', text: ht, ...takeDocxMeta(pd) });
          continue;
        }
      } else if (refSectionRegex.test(trimmed) || refHeadingPrefixRegex.test(ht)) {
        // 网站口径（classifyReferences=false）：带标题样式 / 大纲级别的参考段头若升为
        // heading，会逃逸网站行为层的参考识别（它只扫 k:'p'），导致参考区块丢失 #888 灰色
        // 口径、[n] 条目降级为黑色正文。故降级为正文段交还行为层按 [n] 口径识别。
        // 注意：此处【不】跳过「引言」「标题」heading——那是公众号专属语义，网站若跳过会
        // 静默删字（内容丢失），网站口径下让它们作为普通标题保留。
        elems.push({ k: 'txt', runs: pd.runs, ...takeDocxMeta(pd) });
        continue;
      }
      // 真正的非参考标题会关闭之前的参考段。
      inRef = false;
      elems.push({ k: 'h', text: ht.replace(/^0?\d+\s+/, ''), ...takeDocxMeta(pd) });
      continue;
    }
    if (inRef) {
      if (!trimmed) continue;
      elems.push({ k: 'ref', runs: pd.runs, ...takeDocxMeta(pd) });
      continue;
    }
    if (pd.hasImg) {
      const meta = takeDocxMeta(pd);
      for (const r of pd.runs) {
        if (r.type === 'img') {
          const src = cache[r.file] || '';
          imgN++;
          elems.push({ k: 'img', src, w: r.w, gif: looksLikeGifSource(src) || /\.gif$/i.test(r.file), ...meta });
        }
      }
      while (i + 1 < pds.length) {
        const nx = pds[i + 1];
        if (nx.isEmpty) { i++; continue; }
        if (!nx.hasImg && !nx.isHeading && nx.align === 'center' && nx.text.trim()) {
          let ct = nx.text.trim().replace(/^[（(]插入视频[）)]\s*/, '');
          if (ct) elems.push({ k: 'cap', text: ct });
          i++;
        } else break;
      }
      continue;
    }
    elems.push({ k: 'txt', runs: pd.runs, ...takeDocxMeta(pd) });
  }

  return { elems, author, imgN };
}
