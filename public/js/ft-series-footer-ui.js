// ft-series-footer-ui.js — FoodTalks「什么是」系列侧栏尾部选择器。
// 每篇文章手选：作者名片（15 选 1）+ 群二维码（14 选 1）+ Eileen 名片（固定带上，可关）。
// 选择写 window._ftSeriesFooter + localStorage(ft.series.footer)，触发 window._engineRerender()。
// 仅在 foodtalks-series 渠道挂载（app.html 按包显隐）；不影响 FBIF。

import { SERIES_CARDS, SERIES_GROUPS, SERIES_FOOTER_DEFAULT } from './foodtalks-series-defaults.js';

const LS_KEY = 'ft.series.footer';

// 从服务端 config.modules.ft_series_materials 读名片/群清单，缺省降级 defaults。
function materialsFromConfig() {
  const m = (window._activeConfig && window._activeConfig.config
    && window._activeConfig.config.modules
    && window._activeConfig.config.modules.ft_series_materials) || null;
  return {
    cards: (m && m.cards) || SERIES_CARDS,
    groups: (m && m.groups) || SERIES_GROUPS,
    footerDefault: (m && m.footer_default) || SERIES_FOOTER_DEFAULT,
  };
}

function loadSelection(footerDefault) {
  try {
    const raw = localStorage.getItem(LS_KEY);
    if (raw) {
      const v = JSON.parse(raw);
      return {
        author_card: typeof v.author_card === 'string' ? v.author_card : (footerDefault.author_card || ''),
        group: typeof v.group === 'string' ? v.group : (footerDefault.group || ''),
        include_eileen: v.include_eileen !== false,
      };
    }
  } catch (e) { /* ignore */ }
  return {
    author_card: footerDefault.author_card || '',
    group: footerDefault.group || '',
    include_eileen: footerDefault.include_eileen !== false,
  };
}

function saveSelection(sel) {
  try { localStorage.setItem(LS_KEY, JSON.stringify(sel)); } catch (e) { /* ignore */ }
  window._ftSeriesFooter = sel;
}

function option(value, label, selected) {
  const o = document.createElement('option');
  o.value = value;
  o.textContent = label;
  if (selected) o.selected = true;
  return o;
}

export function initFtSeriesFooter() {
  const section = document.getElementById('ftSeriesFooterSection');
  if (!section) return;
  const authorSel = document.getElementById('ftSeriesAuthorCard');
  const groupSel = document.getElementById('ftSeriesGroup');
  const eileenChk = document.getElementById('ftSeriesEileen');
  if (!authorSel || !groupSel || !eileenChk) return;

  const { cards, groups, footerDefault } = materialsFromConfig();
  const sel = loadSelection(footerDefault);
  // 初始即落地，保证渲染器（getSeriesFooterSelection）首屏就读到。
  window._ftSeriesFooter = sel;

  // 作者名片下拉：(不选) + 15 张
  authorSel.innerHTML = '';
  authorSel.appendChild(option('', '（不选作者名片）', !sel.author_card));
  for (const name of Object.keys(cards)) {
    authorSel.appendChild(option(name, name, sel.author_card === name));
  }
  // 群下拉：14 个品类
  groupSel.innerHTML = '';
  groupSel.appendChild(option('', '（不选群）', !sel.group));
  for (const cat of Object.keys(groups)) {
    groupSel.appendChild(option(cat, cat + '群', sel.group === cat));
  }
  eileenChk.checked = sel.include_eileen !== false;

  const apply = () => {
    saveSelection({
      author_card: authorSel.value || '',
      group: groupSel.value || '',
      include_eileen: !!eileenChk.checked,
    });
    if (typeof window._engineRerender === 'function') window._engineRerender();
  };
  authorSel.addEventListener('change', apply);
  groupSel.addEventListener('change', apply);
  eileenChk.addEventListener('change', apply);
}
