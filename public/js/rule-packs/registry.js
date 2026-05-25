// rule-packs/registry.js —公众号排版包注册表。
// 账号包负责账号 chrome、footer 和模板入口；规则绑定由规则后台维护。

export const DEFAULT_RULE_PACK_ID = 'fbif';

export const RULE_PACKS = Object.freeze({
  fbif: Object.freeze({
    id: 'fbif',
    templateId: 'fbif',
    accountName: 'FBIF公众号',
    previewAccountName: 'FBIF食品饮料创新',
    behaviorAccountName: 'FBIF',
    avatarUrl: '/fbif-avatar.webp',
    footerUrl: '/footer.html',
    location: '上海',
    channel: 'mp',
  }),
  'foodtalks-series': Object.freeze({
    id: 'foodtalks-series',
    templateId: 'fbif',
    accountName: 'FoodTalks什么是系列',
    previewAccountName: 'FoodTalks什么是系列',
    behaviorAccountName: 'FoodTalks什么是系列',
    avatarUrl: '/fbif-avatar.webp',
    footerUrl: '/footer.html',
    location: '上海',
    channel: 'mp',
  }),
});

export function listRulePacks() {
  return Object.values(RULE_PACKS);
}

export function getRulePack(id = DEFAULT_RULE_PACK_ID) {
  return RULE_PACKS[id] || RULE_PACKS[DEFAULT_RULE_PACK_ID];
}

export function getRulePackIDFromLocation(locationLike = globalThis.location) {
  try {
    const params = new URLSearchParams(locationLike && locationLike.search || '');
    const id = params.get('account') || params.get('rule_pack') || DEFAULT_RULE_PACK_ID;
    return getRulePack(id).id;
  } catch {
    return DEFAULT_RULE_PACK_ID;
  }
}
