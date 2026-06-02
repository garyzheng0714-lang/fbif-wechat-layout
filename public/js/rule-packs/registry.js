// rule-packs/registry.js —公众号排版包注册表。
// 账号包负责账号 chrome、footer 和模板入口；规则绑定由规则后台维护。

import { parsePath } from './url.js';

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
    templateId: 'foodtalks-series',
    accountName: 'FoodTalks什么是系列',
    previewAccountName: 'FoodTalks什么是系列',
    behaviorAccountName: 'FoodTalks什么是系列',
    avatarUrl: '/fbif-avatar.webp',
    // 空串 = 不追加 FBIF 公众号页脚（更多文章 / 案例库 QR / 关注引导）。
    // 「什么是」系列的尾部（作者名片 + Eileen + 群二维码）由模板自身在正文末渲染，
    // 与网站渠道 ft-default 同理（mergeIntoFooter 对空 base 直接返回空，故不再注入更多文章）。
    footerUrl: '',
    // 声明该包用「什么是」系列侧栏尾部选择器（名片/群）；app.html 据此显隐，
    // 取代硬编码 id 比较（对齐模板 transferMmbiz 这类「声明能力」范式）。
    footerPicker: 'ft-series',
    location: '上海',
    channel: 'mp',
  }),
  wowfood: Object.freeze({
    id: 'wowfood',
    templateId: 'fbif', // 复用公众号渲染器（DOCX/表格/表格内图片底层能力共用）
    accountName: 'WowFood爆品超市',
    previewAccountName: 'WowFood爆品超市',
    behaviorAccountName: 'WowFood爆品超市',
    avatarUrl: '/fbif-avatar.webp', // TODO: 待 WowFood 头像资源就位后替换
    // 空串 = 不追加 FBIF 公众号页脚；WowFood 自己的尾部规则走它独立的 preset（规则后台可配）。
    footerUrl: '',
    location: '上海',
    channel: 'mp',
  }),
  'ft-default': Object.freeze({
    id: 'ft-default',
    templateId: 'foodtalks-site',
    accountName: '网站默认',
    // 预览作者行显示名：对齐 foodtalks.cn 发布页（基石 62285 作者行为
    // 「FBIF食品饮料创新」——文章是 FBIF 公众号搬运到网站，署名是 FBIF 而非
    // FoodTalks 平台本身）。此前误配为「FoodTalks」，作者行显示错。
    previewAccountName: 'FBIF食品饮料创新',
    behaviorAccountName: 'FoodTalks',
    avatarUrl: '/fbif-avatar.webp',
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
