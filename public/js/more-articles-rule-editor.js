import {
  moveMoreArticleDefaultCard,
  normalizeMoreArticleDefaultCard,
} from './more-articles-config.js';
import {
  fetchMoreArticleMetaCached,
  isValidMoreArticleUrl,
} from './more-articles-meta.js';

function esc(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function cardsFromConfig(config) {
  const cards = Array.isArray(config && config.more_articles_default_cards)
    ? config.more_articles_default_cards
    : [];
  return cards.map(normalizeMoreArticleDefaultCard);
}

function configWithCards(config, cards) {
  return {
    ...(config || {}),
    more_articles_default_cards: cards.map(normalizeMoreArticleDefaultCard),
  };
}

function moreArticlesPatch(config) {
  return {
    more_articles_enabled: config && config.more_articles_enabled === false ? false : true,
    more_articles_default_cards: cardsFromConfig(config),
  };
}

function draftMoreArticleCard() {
  return normalizeMoreArticleDefaultCard({
    title: '未命名文章',
  });
}

export function sameMoreArticleCardSnapshot(a, b) {
  return JSON.stringify(normalizeMoreArticleDefaultCard(a)) ===
    JSON.stringify(normalizeMoreArticleDefaultCard(b));
}

export function toggleMoreArticlesEnabled(config, enabled) {
  return {
    ...configWithCards(config, cardsFromConfig(config)),
    more_articles_enabled: enabled === true,
  };
}

export function addDefaultMoreArticleCard(config) {
  return configWithCards(config, cardsFromConfig(config).concat([draftMoreArticleCard()]));
}

export function removeDefaultMoreArticleCard(config, index) {
  const cards = cardsFromConfig(config);
  const next = cards.filter((_, i) => i !== Number(index));
  return configWithCards(config, next);
}

export function replaceDefaultMoreArticleCard(config, index, card) {
  const cards = cardsFromConfig(config);
  const i = Number(index);
  if (!Number.isInteger(i) || !cards[i]) return configWithCards(config, cards);
  const next = cards.slice();
  next[i] = normalizeMoreArticleDefaultCard(card);
  return configWithCards(config, next);
}

function moveDefaultMoreArticleCard(config, index, delta) {
  return configWithCards(config, moveMoreArticleDefaultCard(cardsFromConfig(config), index, delta));
}

function cardFromItem(item, card) {
  const imageUrls = String(item.querySelector('[data-role="ma-rule-image-urls"]')?.value || '')
    .split('\n')
    .map(url => url.trim())
    .filter(Boolean);
  return normalizeMoreArticleDefaultCard({
    ...card,
    href: item.querySelector('[data-role="ma-rule-url"]')?.value || '',
    title: item.querySelector('[data-role="ma-rule-title"]')?.value || '',
    imgurl: item.querySelector('[data-role="ma-rule-cover"]')?.value || '',
    image_urls: imageUrls,
  });
}

function renderCard(card, index, total) {
  return `
    <div class="ma-rule-card" data-index="${index}">
      <label class="admin-field ma-rule-field">
        <span>公众号文章链接</span>
        <input type="url" data-role="ma-rule-url" value="${esc(card.href)}">
      </label>
      <label class="admin-field ma-rule-field">
        <span>标题</span>
        <input type="text" data-role="ma-rule-title" value="${esc(card.title)}">
      </label>
      <label class="admin-field ma-rule-field">
        <span>封面图链接</span>
        <input type="url" data-role="ma-rule-cover" value="${esc(card.imgurl)}">
      </label>
      <label class="admin-field ma-rule-field">
        <span>候选图片链接（每行一个）</span>
        <textarea data-role="ma-rule-image-urls" spellcheck="false">${esc((card.image_urls || []).join('\n'))}</textarea>
      </label>
      <div class="admin-json-actions">
        <button type="button" class="admin-secondary" data-action="fetch-meta">抓取标题和图片</button>
        <button type="button" class="admin-secondary" data-action="move-up" ${index === 0 ? 'disabled' : ''}>上移</button>
        <button type="button" class="admin-secondary" data-action="move-down" ${index >= total - 1 ? 'disabled' : ''}>下移</button>
        <button type="button" class="admin-secondary danger" data-action="remove-card">删除</button>
      </div>
      <div class="admin-status" data-role="ma-rule-status"></div>
    </div>`;
}

export function createMoreArticlesRuleEditor({ config, onChange } = {}) {
  const root = document.createElement('div');
  let currentConfig = configWithCards(config || {}, cardsFromConfig(config || {}));

  function emit(nextConfig, shouldRender = true) {
    currentConfig = nextConfig;
    if (typeof onChange === 'function') onChange(moreArticlesPatch(nextConfig));
    if (shouldRender) render();
  }

  function render() {
    const cards = cardsFromConfig(currentConfig);
    root.innerHTML = `
      <div class="ma-rule-editor" data-role="more-articles-rule-card-editor">
        <h3>默认推荐文章</h3>
        <p>保存后，新导入文章会默认使用这组推荐文章；之后仍可在编辑器里单篇调整。</p>
        <label class="admin-field ma-rule-field ma-rule-toggle-row">
          <span>启用底部更多文章</span>
          <button type="button" class="toggle-switch admin-toggle ${currentConfig.more_articles_enabled === false ? '' : 'on'}" data-action="toggle-enabled" aria-pressed="${currentConfig.more_articles_enabled === false ? 'false' : 'true'}">
            <span class="toggle-knob"></span>
          </button>
        </label>
        <div data-role="ma-rule-list">
          ${cards.length ? cards.map((card, index) => renderCard(card, index, cards.length)).join('') : '<div class="admin-rule-ok">还没有默认文章。可以先添加一篇，再粘贴公众号链接。</div>'}
        </div>
        <button type="button" class="admin-secondary" data-action="add-card">添加文章</button>
      </div>`;
  }

  root.addEventListener('input', e => {
    const item = e.target.closest('[data-index]');
    if (!item) return;
    const index = Number(item.dataset.index);
    const cards = cardsFromConfig(currentConfig);
    emit(replaceDefaultMoreArticleCard(currentConfig, index, cardFromItem(item, cards[index])), false);
  });

  root.addEventListener('click', async e => {
    const action = e.target.closest('[data-action]')?.dataset.action;
    if (!action) return;
    if (action === 'toggle-enabled') {
      emit(toggleMoreArticlesEnabled(currentConfig, currentConfig.more_articles_enabled === false));
      return;
    }
    if (action === 'add-card') {
      emit(addDefaultMoreArticleCard(currentConfig));
      return;
    }

    const item = e.target.closest('[data-index]');
    const index = Number(item && item.dataset.index);
    if (!item || !Number.isInteger(index)) return;
    if (action === 'remove-card') {
      emit(removeDefaultMoreArticleCard(currentConfig, index));
      return;
    }
    if (action === 'move-up') {
      emit(moveDefaultMoreArticleCard(currentConfig, index, -1));
      return;
    }
    if (action === 'move-down') {
      emit(moveDefaultMoreArticleCard(currentConfig, index, 1));
      return;
    }
    if (action !== 'fetch-meta') return;

    const status = item.querySelector('[data-role="ma-rule-status"]');
    const href = item.querySelector('[data-role="ma-rule-url"]')?.value || '';
    const requestHref = normalizeMoreArticleDefaultCard({ href }).href;
    if (!isValidMoreArticleUrl(requestHref)) {
      if (status) status.textContent = '请粘贴完整的微信公众号文章链接';
      return;
    }
    const requestCards = cardsFromConfig(currentConfig);
    if (!requestCards[index]) return;
    const requestCard = normalizeMoreArticleDefaultCard({
      ...requestCards[index],
      href: requestHref,
    });
    if (status) status.textContent = '正在抓取...';
    try {
      const meta = await fetchMoreArticleMetaCached(requestHref);
      const cards = cardsFromConfig(currentConfig);
      if (!cards[index] || !sameMoreArticleCardSnapshot(cards[index], requestCard)) return;
      emit(replaceDefaultMoreArticleCard(currentConfig, index, {
        ...cards[index],
        href: requestHref,
        title: meta.title || cards[index].title,
        imgurl: meta.cover_url || meta.imgurl || cards[index].imgurl,
        image_urls: meta.image_urls || cards[index].image_urls,
      }));
    } catch (err) {
      if (status) status.textContent = err.message || '抓取失败，请检查链接';
    }
  });

  render();
  return root;
}
