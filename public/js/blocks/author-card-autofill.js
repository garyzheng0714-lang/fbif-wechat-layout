// author-card-autofill.js — FoodTalks 系列文章作者名片自动匹配。
//
// 只做确定性匹配：正文作者名 -> ft_series_materials.cards/author_card_aliases。
// 匹配不到就清空作者名片，避免沿用上一篇文章的手动选择。

import { SERIES_MATERIALS_DEFAULT } from '../foodtalks-series-defaults.js';

function isObj(v) { return !!v && typeof v === 'object' && !Array.isArray(v); }

function materialsOf(config) {
  const mods = config && config.modules;
  const raw = isObj(mods) && isObj(mods.ft_series_materials) ? mods.ft_series_materials : {};
  return { ...SERIES_MATERIALS_DEFAULT, ...raw };
}

function normalizeName(value) {
  return String(value || '').replace(/\s+/g, '').toLowerCase();
}

function assetIDPartFromURL(rawURL) {
  const base = String(rawURL || '').split('/').pop().replace(/\.[^.]*$/, '').toLowerCase();
  return base.replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '') || 'asset';
}

function findCardKey(cards, wanted) {
  const target = normalizeName(wanted);
  if (!target || !isObj(cards)) return '';
  for (const key of Object.keys(cards)) {
    if (normalizeName(key) === target) return key;
  }
  return '';
}

function aliasFor(aliases, authorName) {
  const target = normalizeName(authorName);
  if (!target || !isObj(aliases)) return '';
  for (const key of Object.keys(aliases)) {
    if (normalizeName(key) === target) return String(aliases[key] || '').trim();
  }
  return '';
}

export function resolveSeriesAuthorCardRef(authorName, config = {}) {
  const materials = materialsOf(config);
  const cards = isObj(materials.cards) ? materials.cards : {};
  const alias = aliasFor(materials.author_card_aliases, authorName);
  const cardKey = findCardKey(cards, alias || authorName);
  if (!cardKey) return '';
  return '{{asset#ft_series_card_' + assetIDPartFromURL(cards[cardKey]) + '}}';
}

function cloneOverride(override) {
  return isObj(override) ? JSON.parse(JSON.stringify(override)) : {};
}

function pruneEmpty(out) {
  const blocks = isObj(out.overrides) ? out.overrides : {};
  for (const id of Object.keys(blocks)) {
    const block = blocks[id];
    if (isObj(block.fields) && !Object.keys(block.fields).length) delete block.fields;
    if (!block.fields) delete blocks[id];
  }
  if (out.overrides && !Object.keys(out.overrides).length) delete out.overrides;
  return out;
}

export function applySeriesAuthorCardAutofill({ result, config, accountId, override } = {}) {
  const current = cloneOverride(override);
  if (accountId !== 'foodtalks-series') return { override: current, changed: false, ref: '', authorName: '' };

  const authorName = String(result && result.authorName || '').trim();
  const ref = resolveSeriesAuthorCardRef(authorName, config);
  const before = JSON.stringify(current);

  if (!isObj(current.overrides)) current.overrides = {};
  if (!isObj(current.overrides.author_card)) current.overrides.author_card = {};
  if (!isObj(current.overrides.author_card.fields)) current.overrides.author_card.fields = {};

  if (ref) current.overrides.author_card.fields.image = ref;
  else delete current.overrides.author_card.fields.image;

  pruneEmpty(current);
  return {
    override: current,
    changed: JSON.stringify(current) !== before,
    ref,
    authorName,
  };
}
