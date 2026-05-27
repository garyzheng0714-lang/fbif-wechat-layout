// rule-behavior-foodtalks.js — FoodTalks 网站专属行为模板
//
// 与 public/js/rule-behavior.js（公众号）平行，不互相 import。
// 纯函数模块，不依赖 engine.js / 浏览器 API，可在 Node 直接测试。
//
// 输入：elems 数组（{k: 'p'|'h'|'img'|..., runs: [...]}）+ config 对象
// 输出：新的 elems 数组（不修改输入），其中部分元素被提升为：
//   - k:'author'      作者署名行（仅第一个非空 p 命中时）
//   - k:'tip'         提示行（任意 p 行首命中）
//   - k:'ref-header'  参考来源段头
//   - k:'ref-item'    参考来源条目（[1] [2] 等，遇非 [n] 段则停止）
//
// 规则来源：docs/superpowers/specs/2026-05-27-foodtalks-site-layout-design.md §4

function runsToText(runs) {
  return (runs || [])
    .filter(r => r.type === 'txt' || r.type === 'link')
    .map(r => r.text || '')
    .join('');
}

function isEmptyP(e) {
  if (!e || e.k !== 'p') return false;
  return runsToText(e.runs).trim() === '';
}

function firstNonEmptyPIndex(elems) {
  for (let i = 0; i < elems.length; i++) {
    if (elems[i].k === 'p' && !isEmptyP(elems[i])) return i;
  }
  return -1;
}

export function applyFoodTalksBehavior(elems, config = {}) {
  if (!Array.isArray(elems)) return elems;

  const behavior = (config && config.behavior) || {};
  const authorRe    = new RegExp(behavior.author_regex     || '^\\s*作者[：:]\\s*');
  const tipRe       = new RegExp(behavior.tip_regex        || '^\\s*提示[：:]');
  const refHeaderRe = new RegExp(behavior.ref_header_regex || '^\\s*参考(?:来源|资料)[：:]\\s*$');
  const refItemRe   = new RegExp(behavior.ref_item_regex   || '^\\s*\\[\\d+\\]');

  // 浅拷贝每个元素（runs 数组本身也新建），保证不原地修改输入
  const out = elems.map(e => ({
    ...e,
    runs: Array.isArray(e.runs) ? e.runs.slice() : e.runs,
  }));

  // 阶段 1：作者识别（仅第一个非空 p）
  const firstIdx = firstNonEmptyPIndex(out);
  if (firstIdx >= 0) {
    const text = runsToText(out[firstIdx].runs);
    if (authorRe.test(text)) {
      out[firstIdx] = { k: 'author', text };
    }
  }

  // 阶段 2：提示行识别（任意 k:'p'）
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (e.k !== 'p') continue;
    const text = runsToText(e.runs);
    if (tipRe.test(text)) {
      out[i] = { k: 'tip', runs: e.runs };
    }
  }

  // 阶段 3：参考来源段头 + 后续 ref-item（遇非 [n] 开头段即中断）
  let refMode = false;
  for (let i = 0; i < out.length; i++) {
    const e = out[i];
    if (e.k !== 'p') {
      // 任何不是 p 的元素（包括前面阶段提升出来的 author/tip，以及 h/img）
      // 都中断 refMode —— 否则参考段中间夹一个提示行会让其后的 [n] 仍然被
      // 误识别为 ref-item。
      if (refMode) refMode = false;
      continue;
    }
    const text = runsToText(e.runs).trim();
    if (!refMode) {
      if (refHeaderRe.test(text)) {
        out[i] = { k: 'ref-header' };
        refMode = true;
      }
      continue;
    }
    // refMode 已开启：期待 [n] 开头
    if (refItemRe.test(text)) {
      out[i] = { k: 'ref-item', runs: e.runs };
    } else {
      refMode = false;  // 遇到非 [n] 段，停止识别
    }
  }

  return out;
}

// FBIF 公众号原创页脚片段（可选，由 config.modules.fbif_footer.enabled 决定）
//
// 注意：刻意不包含「关注公众号 + FoodTalks logo + 点击获取更多」部分——
// 那一段由 FoodTalks 系统在发布时自动追加，若此处重复输出会导致页面重复。
export const FBIF_FOOTER_HTML = [
  '<p style="text-align: justify;"><em><span style="font-size: 14px; color: #7f7f7f;">*资讯投稿与商务合作，请查看 FoodTalks 商务合作方案，并联系 Bobo。</span></em></p>',
  '<p><br></p>',
  '<p style="color: #01396a; font-size: 20px;"><strong>加入微信群</strong></p>',
  '<p><br></p>',
  '<p>[加入微信群二维码请在 FoodTalks 后台手动插入]</p>',
  '<p style="text-align: justify;"><em><span style="color: #7f7f7f; font-size: 14px;">*如遇二维码失效无法加群，请联系工作人员 Lemon（微信号：FBIF_Lemon）。</span></em></p>',
].join('\n');
