const DEFAULT_CROP = Object.freeze({ x: 0.5, y: 0.5, scale: 1 });

export const SYSTEM_DEFAULT_RECOMMENDED_CARDS = Object.freeze([
  Object.freeze({
    href: 'https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652909589&idx=1&sn=d0bdeacee2b1ba7f03e48b38f91bbefa&scene=21',
    imgurl: 'https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTRHibhuMnzJTuvhqr6Neq7ib00icPLXRyof5H9ARUnw9eWIshgyHibyfVXtgSRkWmOP2MWZjHLLbFec9VAFSYH6ZsarKf2LUC4ONEkE/0?wx_fmt=jpeg',
    title: '没想到！英国人把豆腐做到英国第一，还像卖衣服一样出XXL超大号！',
  }),
  Object.freeze({
    href: 'https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652916388&idx=1&sn=b5605734c9c27ce2f8d54a6534b6543d&scene=21',
    imgurl: 'https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTREtc1dUPY9VokWQTtQsdevBIcfoMMavrQNSVFoce8gDOtTqlWhciadMP6VJwzErl7HnzECBECMMILEXt653GCeub3nDZ8VrvTsM/0?wx_fmt=jpeg',
    title: '做了20年糖，她把豆腐棒放在鸡胸肉旁边，5年卖出1亿根',
  }),
  Object.freeze({
    href: 'https://mp.weixin.qq.com/s?__biz=MzA3ODEzMjg5Mg==&mid=2652906407&idx=1&sn=7ede62b74d3a5750c2c95ca40c216290&scene=21',
    imgurl: 'https://mmbiz.qpic.cn/sz_mmbiz_jpg/6JHhlvvjTRGEHDwCvfSmEXO9VBXpibZHE3ZqLYicsIyhfiajfq9MNduKq03KbDeqU0iaj3WEQeBEY2j0mSCetzD1Jdic1iciaL7M1KZjHu8x5TLdmM/0?wx_fmt=jpeg',
    title: '日本最牛包子铺，单店年销2329万，怎么做到的？',
  }),
]);

function normalizeUrl(value) {
  const url = String(value || '').trim();
  if (url.startsWith('//')) return `https:${url}`;
  return url;
}

function normalizeImageUrls(values) {
  if (!Array.isArray(values)) return [];
  const seen = new Set();
  const result = [];
  for (const value of values) {
    const url = normalizeUrl(value);
    if (!url || seen.has(url)) continue;
    seen.add(url);
    result.push(url);
  }
  return result;
}

function normalizeCrop(value) {
  if (!value || typeof value !== 'object') return { ...DEFAULT_CROP };
  return {
    x: Number.isFinite(Number(value.x)) ? Number(value.x) : DEFAULT_CROP.x,
    y: Number.isFinite(Number(value.y)) ? Number(value.y) : DEFAULT_CROP.y,
    scale: Number.isFinite(Number(value.scale)) ? Number(value.scale) : DEFAULT_CROP.scale,
  };
}

export function emptyMoreArticleCard() {
  return {
    href: '',
    title: '',
    imgurl: '',
    image_urls: [],
    cover_data_url: null,
    crop: { ...DEFAULT_CROP },
  };
}

export function normalizeMoreArticleDefaultCard(raw) {
  const source = raw && typeof raw === 'object' ? raw : {};
  return {
    href: normalizeUrl(source.href),
    title: String(source.title || '').trim(),
    imgurl: normalizeUrl(source.imgurl),
    image_urls: normalizeImageUrls(source.image_urls),
    cover_data_url: null,
    crop: normalizeCrop(source.crop),
  };
}

export function hasMoreArticleCardContent(card) {
  if (!card || typeof card !== 'object') return false;
  return Boolean(
    String(card.href || '').trim()
    || String(card.title || '').trim()
    || String(card.imgurl || '').trim()
    || String(card.cover_data_url || '').trim()
    || normalizeImageUrls(card.image_urls).length
  );
}

export function normalizeMoreArticleDefaultCards(
  rawCards,
  fallback = SYSTEM_DEFAULT_RECOMMENDED_CARDS
) {
  const cards = Array.isArray(rawCards) ? rawCards : fallback;
  return cards
    .map(normalizeMoreArticleDefaultCard)
    .filter(hasMoreArticleCardContent);
}

export function moveMoreArticleDefaultCard(cards, index, delta) {
  if (!Array.isArray(cards)) return [];
  const next = cards.map(card => ({ ...card }));
  const from = Number(index);
  const offset = Number(delta);
  if (!Number.isInteger(from) || !Number.isInteger(offset) || !next[from] || offset === 0) {
    return next;
  }
  const to = Math.max(0, Math.min(next.length - 1, from + offset));
  if (to === from) return next;
  const [card] = next.splice(from, 1);
  next.splice(to, 0, card);
  return next;
}

export function moreArticleCardsForRule(config = {}) {
  if (config.more_articles_enabled === false) return [];
  return normalizeMoreArticleDefaultCards(config.more_article_default_cards);
}
