// FoodTalks 网站「什么是系列」联系区归一化。
// 只处理 config.justify_paragraphs=false 的系列网站包：联系信息块居中，组内紧凑。

function behParamStr(config, key, fallback) {
  const p = config && config.behavior && config.behavior.params;
  const s = p && p[key] && p[key].default;
  return (typeof s === 'string' && s) ? s : fallback;
}

function compileRegex(src) {
  let s = String(src), flags = '';
  const m = s.match(/^\(\?([a-z]+)\)/);
  if (m) { flags = m[1].replace(/[^gimsuy]/g, ''); s = s.slice(m[0].length); }
  return new RegExp(s, flags);
}

function behaviorRegex(config, key, fallback) {
  const src = behParamStr(config, key, fallback);
  try { return compileRegex(src); } catch (e) { return compileRegex(fallback); }
}

function isSeriesSiteConfig(config = {}) {
  return config && config.justify_paragraphs === false;
}

function runsToText(runs) {
  return (runs || [])
    .filter(r => r && (r.type === 'txt' || r.type === 'link'))
    .map(r => r.text || '')
    .join('');
}

function elemText(e) {
  if (!e) return '';
  if (Array.isArray(e.runs)) return runsToText(e.runs);
  return typeof e.text === 'string' ? e.text : '';
}

function allVisibleRunsBold(runs) {
  const visible = (runs || []).filter(r => r && (r.type === 'txt' || r.type === 'link') && String(r.text || '').trim());
  return visible.length > 0 && visible.every(r => r.bold);
}

function cloneElem(e) {
  return {
    ...e,
    runs: Array.isArray(e.runs) ? e.runs.map(r => ({ ...r })) : e.runs,
  };
}

function isContactBoundaryText(text) {
  return /^(参考(来源|文献|资料|链接)|信息来源)\s*[：:]?$/.test(text) ||
    /^本文(作者|图片来源)\s*[：:]/.test(text) ||
    /^图片均来自/.test(text) ||
    /^提示\s*[：:]/.test(text);
}

function markContactInfo(e, text) {
  e.center = true;
  e.contactInfo = true;
  e.skipHeadingPromote = true;
  e.contactNote = /^[（(]?\s*添加请备注/.test(text);
}

export function applySeriesContactLayout(elems, config = {}) {
  if (!isSeriesSiteConfig(config)) return elems;
  const out = (elems || []).map(cloneElem);
  const hPos = [];
  for (let i = 0; i < out.length; i++) if (out[i] && out[i].k === 'h') hPos.push(i);
  if (!hPos.length) return elems;

  const sectionRe = behaviorRegex(config, 'contact_section_regex', '联系');
  const fieldRe = behaviorRegex(
    config,
    'contact_field_regex',
    '^(地址|地點|地点|总部地址|官网|官網|网址|網址|网站|網站|电话|電話|联系电话|联系方式|邮箱|郵箱|邮件|手机|手機|微信|公众号|公眾號|传真|傳真|Tel|Phone|Email|E-?mail|Add|Address|Web|Website)\\s*[：:]'
  );

  for (let h = 0; h < hPos.length; h++) {
    const secIdx = hPos[h];
    const endIdx = hPos[h + 1] || out.length;
    const sectionText = elemText(out[secIdx]).trim();
    sectionRe.lastIndex = 0;
    if (!sectionRe.test(sectionText)) continue;

    let inInfoBlock = false;
    for (let i = secIdx + 1; i < endIdx; i++) {
      const e = out[i];
      if (!e || e.k !== 'p') continue;
      const text = elemText(e).trim();
      if (!text) continue;
      if (isContactBoundaryText(text)) break;
      fieldRe.lastIndex = 0;
      const startsInfo = fieldRe.test(text) || (text.length <= 80 && allVisibleRunsBold(e.runs));
      if (startsInfo) {
        inInfoBlock = true;
        markContactInfo(e, text);
        continue;
      }
      if (!inInfoBlock) continue;
      if (text.length > 80) {
        inInfoBlock = false;
        continue;
      }
      markContactInfo(e, text);
    }
  }
  return out;
}
