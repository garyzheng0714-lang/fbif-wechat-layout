// 载体（2026-10-27 用户裁定：「在旁边做一个胶囊，例如 文章 贴图，只有选择公众号的时候才可以切换」
// 「文章是自己的排版规则，贴图是贴图的规则」）。只在公众号编辑器（app.html）出现，排在账号旁边。
//
// 载体决定用哪个模板排：文章 = 当前账号自己的模板与规则；贴图 = 独立的 templates/sticker.js，与账号无关。
// 它是界面偏好，不是排版规则（不走 /api/rules/*），存在 localStorage，切账号、刷新都保留。
// 切换只换模板并按缓存的原文重排（onChange 由页面接到 loadTemplate），过期的切换不提交（latest-wins）。

export const CARRIER_STORAGE_KEY = 'layout_carrier';

// 载体 → 显示名与固定模板；templateId 为 null 表示用账号自己的模板。
const CARRIERS = {
  article: { label: '文章', templateId: null },
  sticker: { label: '贴图', templateId: 'sticker' },
};
const ORDER = Object.keys(CARRIERS);
const DEFAULT_CARRIER = 'article';

export function readStoredCarrier(storage) {
  let stored = null;
  try { stored = storage && storage.getItem(CARRIER_STORAGE_KEY); } catch {}
  return Object.prototype.hasOwnProperty.call(CARRIERS, stored) ? stored : DEFAULT_CARRIER;
}

function writeStoredCarrier(storage, carrier) {
  try { if (storage) storage.setItem(CARRIER_STORAGE_KEY, carrier); } catch {}
}

// onChange({ stale }) → Promise<boolean>：换好模板并重排；stale() 为 true 时不得再提交。
export function createCarrier({ storage, onChange } = {}) {
  let current = readStoredCarrier(storage);
  let committed = current;
  let generation = 0;
  let group = null;

  function render() {
    if (!group) return;
    for (const button of group.querySelectorAll('[data-carrier]')) {
      const checked = button.dataset.carrier === current;
      button.setAttribute('aria-checked', checked ? 'true' : 'false');
      button.tabIndex = checked ? 0 : -1;
    }
  }

  function setCurrent(carrier) {
    current = carrier;
    writeStoredCarrier(storage, carrier);
    render();
  }

  async function select(next) {
    if (!Object.prototype.hasOwnProperty.call(CARRIERS, next) || next === current) return 'noop';
    const mine = ++generation;
    const stale = () => mine !== generation;
    setCurrent(next);
    try {
      const applied = await (onChange ? onChange({ stale }) : true);
      if (stale()) return 'superseded';
      if (applied === false) throw new Error('载体切换未完成');
      committed = next;
      return 'committed';
    } catch (err) {
      if (stale()) return 'superseded';
      setCurrent(committed);
      console.warn('[carrier] switch failed', err);
      return 'failed';
    }
  }

  function onKeydown(event) {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key];
    let index = ORDER.indexOf(current);
    if (step) index = (index + step + ORDER.length) % ORDER.length;
    else if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = ORDER.length - 1;
    else return;
    event.preventDefault();
    const target = ORDER[index];
    select(target);
    const button = group.querySelector('[data-carrier="' + target + '"]');
    if (button) button.focus();
  }

  function mount(container) {
    if (!container) return;
    const doc = container.ownerDocument;
    group = container;
    group.setAttribute('role', 'radiogroup');
    group.setAttribute('aria-label', '排版载体');
    group.replaceChildren(...ORDER.map(id => {
      const button = doc.createElement('button');
      button.type = 'button';
      button.className = 'carrier-option';
      button.setAttribute('role', 'radio');
      button.dataset.carrier = id;
      button.textContent = CARRIERS[id].label;
      button.addEventListener('click', () => { select(id); });
      return button;
    }));
    group.addEventListener('keydown', onKeydown);
    render();
  }

  return {
    get value() { return current; },
    templateFor(accountTemplateId) { return CARRIERS[current].templateId || accountTemplateId; },
    select,
    mount,
  };
}
