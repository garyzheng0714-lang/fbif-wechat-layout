// asset-library-defaults.js — 前端离线素材库降级常量。
// 真相以服务端 /api/rules 的 config.modules.asset_library 为准。

function asset(id, usage, label, url, scope, tags = [], size = {}) {
  const out = { id, type: 'image', usage, label, url, scope, tags };
  if (size.width) out.width = size.width;
  if (size.height) out.height = size.height;
  return out;
}

const SERIES_SECTION_ASSETS = Array.from({ length: 12 }, (_, i) => {
  const n = String(i + 1).padStart(2, '0');
  return asset(
    `ft_series_section_${n}`,
    'custom',
    `什么是系列序号图 ${n}`,
    `/assets/foodtalks-series/sections/${n}.png`,
    'foodtalks-series',
    ['什么是系列', '序号图']
  );
});

const SERIES_GROUPS = [
  ['seasoning', '调味品'],
  ['dairy', '乳品'],
  ['snack', '零食'],
  ['beverage', '饮料'],
  ['packaging', '包装'],
  ['cheese', '奶酪'],
  ['supply-demand', '食品供需对接'],
  ['bakery', '烘焙'],
  ['functional', '功能性食品'],
  ['tea', '新茶饮'],
  ['icecream', '冰淇淋'],
  ['ingredient', '配料'],
  ['marketing', '营销'],
  ['channel', '渠道'],
  ['oem', '代工'],
  ['alcohol', '酒类'],
  ['news', '食品资讯'],
  ['convenience', '方便速食'],
];

const SERIES_GROUP_ASSETS = SERIES_GROUPS.map(([slug, label]) => asset(
  `ft_series_group_${slug.replace(/[^a-z0-9]+/g, '_')}`,
  'custom',
  `什么是系列${label}群二维码`,
  `/assets/foodtalks-series/groups/${slug}.png`,
  'foodtalks-series',
  ['什么是系列', '群二维码', label]
));

const SERIES_CARDS = ['Aimee', 'Charlie', 'Eileen', 'Erica', 'Estelle', 'Isaac', 'Justine', 'Kee', 'Lan', 'Rain', 'Renee', 'Soso', 'Tiki', 'Tina', 'Yisa'];
const SERIES_CARD_ASSETS = SERIES_CARDS.map(name => asset(
  `ft_series_card_${name.toLowerCase()}`,
  'avatar',
  `什么是系列${name}名片`,
  `/assets/foodtalks-series/cards/${name.toLowerCase()}.jpg`,
  'foodtalks-series',
  ['什么是系列', '名片', name]
));

// 「FoodTalks食品资讯」公众号（scope=foodtalks-mp）：饼干序号图、Bobo 名片、食品资讯群码，
// 外加什么是系列那 18 张品类群码（换群时可选）。与 api/rule_foodtalks_mp.go 一致。
const FT_MP_SCOPE = 'foodtalks-mp';
const FT_MP_ASSETS = [
  ...Array.from({ length: 11 }, (_, i) => {
    const n = String(i + 1).padStart(2, '0');
    return asset(`ft_mp_section_${n}`, 'custom', `FoodTalks食品资讯序号图 ${n}`, `/assets/foodtalks-mp/sections/${n}.png`, FT_MP_SCOPE, ['FoodTalks食品资讯', '序号图']);
  }),
  asset('ft_mp_bobo_card', 'avatar', 'Bobo 名片（新媒体总监）', '/assets/foodtalks-mp/bobo-card.webp', FT_MP_SCOPE, ['FoodTalks食品资讯', '名片']),
  asset('ft_mp_group_assistant', 'qr_group', '食品资讯群二维码（线上助手）', '/assets/foodtalks-mp/group-news.webp', FT_MP_SCOPE, ['FoodTalks食品资讯', '群二维码']),
  ...SERIES_GROUPS.map(([slug, label]) => asset(
    `ft_mp_group_${slug.replace(/[^a-z0-9]+/g, '_')}`,
    'custom',
    `${label}群二维码`,
    `/assets/foodtalks-series/groups/${slug}.png`,
    FT_MP_SCOPE,
    ['FoodTalks食品资讯', '群二维码', label]
  )),
];

export const ASSET_LIBRARY_DEFAULT = {
  schema_version: 1,
  assets: [
    asset(
      'ft_footer_banner',
      'banner',
      'FoodTalks 页脚装饰图',
      'https://static.foodtalks.cn/image/post/07079a66940e1ff57e71b140e7e36357.webp',
      'global',
      ['FoodTalks', '页脚'],
      { width: 600 }
    ),
    asset(
      'ft_footer_group_qr',
      'qr_group',
      'FoodTalks 加群二维码',
      'https://static.foodtalks.cn/image/news/6e309987660da9af009b89be08c4aa74ed78.png',
      'global',
      ['二维码', '页脚'],
      { width: 600, height: 267 }
    ),
    asset(
      'ft_footer_account_qr',
      'qr_account',
      'FoodTalks 公众号二维码',
      'https://static.foodtalks.cn/image/post/b5b448f324a538209eb53379bef8dea9a7bf.jpg',
      'global',
      ['二维码', '页脚'],
      { width: 600, height: 221 }
    ),
    asset(
      'ft_footer_logo',
      'logo',
      'FoodTalks logo',
      'https://static.foodtalks.cn/uploads/images/202007/e155b7852c10a72f7b73157fdf9c4bf0.png',
      'global',
      ['logo', '页脚'],
      { width: 130, height: 27 }
    ),
    asset(
      'ft_footer_bobo_card',
      'avatar',
      'FoodTalks Bobo 名片',
      '/assets/fbif/bobo-card.webp',
      'ft-default',
      ['FoodTalks', '名片', '商务合作'],
      { width: 600 }
    ),
    // FBIF 公众号底部素材（footer.html 内联 base64 提取为站内静态文件；复制时自动上传微信 CDN）。
    // 不设 width/height：保留原 <img> 仅 style 控宽，避免补属性破坏字节级保真。
    asset('fbif_bobo_card', 'avatar', 'FBIF 波波名片', '/assets/fbif/bobo-card.webp', 'fbif', ['FBIF', '名片']),
    asset('fbif_promo_1000', 'decor', 'FBIF 1000个食品创新案例引流图', '/assets/fbif/promo-1000.webp', 'fbif', ['FBIF', '引流图']),
    ...SERIES_SECTION_ASSETS,
    ...SERIES_GROUP_ASSETS,
    ...SERIES_CARD_ASSETS,
    ...FT_MP_ASSETS,
  ],
};
