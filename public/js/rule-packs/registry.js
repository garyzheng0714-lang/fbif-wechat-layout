// rule-packs/registry.js —公众号排版包注册表。
// 【生成文件】由 `go run . -emit-registry`（npm run gen:registry）从 api/account_catalog.go 生成，请勿手改。
// 账号元数据唯一真相在 accountCatalog()；改账号后跑 npm run gen:registry，启动期一致性校验兜底。

import { parsePath } from './url.js';

export const DEFAULT_RULE_PACK_ID = 'fbif';

export const RULE_PACKS = Object.freeze({
  fbif: Object.freeze({
    id: 'fbif',
    templateId: 'fbif',
    accountName: 'FBIF食品饮料创新',
    previewAccountName: 'FBIF食品饮料创新',
    behaviorAccountName: 'FBIF',
    avatarUrl: '/fbif-avatar.webp',
    footerUrl: '/footer.html',
    location: '上海',
    channel: 'mp',
  }),
  // 独立渲染器；尾部由模板自身渲染，footerUrl 空串；footerPicker=ft-series 驱动侧栏系列尾部选择器。
  'foodtalks-series': Object.freeze({
    id: 'foodtalks-series',
    templateId: 'foodtalks-series',
    accountName: 'FoodTalks什么是系列',
    previewAccountName: 'FoodTalks什么是系列',
    behaviorAccountName: 'FoodTalks什么是系列',
    avatarUrl: '/fbif-avatar.webp',
    footerUrl: '',
    footerPicker: 'ft-series',
    location: '上海',
    channel: 'mp',
  }),
  // 复用公众号渲染器（templateId=fbif）；footerUrl 空串不追加 FBIF 页脚；avatar 待替换。
  wowfood: Object.freeze({
    id: 'wowfood',
    templateId: 'fbif',
    accountName: 'WowFood爆品超市',
    previewAccountName: 'WowFood爆品超市',
    behaviorAccountName: 'WowFood爆品超市',
    avatarUrl: '/fbif-avatar.webp',
    footerUrl: '',
    location: '上海',
    channel: 'mp',
  }),
  // 网站渲染器，与公众号物理隔离（红线 #19）；previewAccountName 是基石 62285 的 FBIF 署名而非 FoodTalks。
  'ft-default': Object.freeze({
    id: 'ft-default',
    templateId: 'foodtalks-site',
    accountName: '网站默认',
    previewAccountName: 'FBIF食品饮料创新',
    behaviorAccountName: 'FoodTalks',
    avatarUrl: '/foodtalks-logo.svg',
    footerUrl: '',
    location: '',
    channel: 'ft',
  }),
  // 网站渠道「什么是系列」：复用 foodtalks-site 渲染器，颜色#333333/#7f7f7f/#01396a + 商务合作版块差异走 config。放 ft-default 之后保证 ft 渠道默认仍是 ft-default（红线#21）。
  'ft-series': Object.freeze({
    id: 'ft-series',
    templateId: 'foodtalks-site',
    accountName: '网站·什么是系列',
    previewAccountName: 'FoodTalks什么是系列',
    behaviorAccountName: 'FoodTalks',
    avatarUrl: '/foodtalks-logo.svg',
    footerUrl: '',
    location: '',
    channel: 'ft',
  }),
});

export function listRulePacks() {
  return Object.values(RULE_PACKS);
}

export function getRulePacksByChannel(channel) {
  return Object.values(RULE_PACKS).filter(pack => pack.channel === channel);
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

const CHANNEL_DEFAULTS = Object.freeze({
  mp: 'fbif',
  ft: 'ft-default',
});

export function getRulePackByPath(pathname) {
  const parsed = parsePath(pathname);
  if (!parsed) return getRulePack(DEFAULT_RULE_PACK_ID);
  const { channel, packId } = parsed;
  if (packId && RULE_PACKS[packId] && RULE_PACKS[packId].channel === channel) {
    return RULE_PACKS[packId];
  }
  const fallbackId = CHANNEL_DEFAULTS[channel];
  if (fallbackId && RULE_PACKS[fallbackId]) {
    return RULE_PACKS[fallbackId];
  }
  return getRulePack(DEFAULT_RULE_PACK_ID);
}
