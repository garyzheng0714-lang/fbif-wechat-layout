// rule-packs/registry.js —公众号排版包注册表。
// 第一阶段只注册 FBIF，先把“账号 chrome / footer / template”从页面散点常量里收拢。

export const DEFAULT_RULE_PACK_ID = 'fbif';

export const RULE_PACKS = Object.freeze({
  fbif: Object.freeze({
    id: 'fbif',
    templateId: 'fbif',
    accountName: 'FBIF食品饮料创新',
    avatarUrl: '/fbif-avatar.webp',
    footerUrl: '/footer.html',
    location: '上海',
  }),
});

export function getRulePack(id = DEFAULT_RULE_PACK_ID) {
  return RULE_PACKS[id] || RULE_PACKS[DEFAULT_RULE_PACK_ID];
}

export function getRulePackIDFromLocation(locationLike = globalThis.location) {
  try {
    const params = new URLSearchParams(locationLike && locationLike.search || '');
    return params.get('account') || params.get('rule_pack') || DEFAULT_RULE_PACK_ID;
  } catch {
    return DEFAULT_RULE_PACK_ID;
  }
}
