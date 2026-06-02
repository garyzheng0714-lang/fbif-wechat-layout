// foodtalks-series-defaults.js — FoodTalks「什么是」系列排版【降级常量】。
//
// 真相以服务端 /api/rules 的 foodtalks-series 账号 config / config.behavior /
// config.modules.ft_series_materials 为准（规则后台可编辑——红线：规则不硬编码）；
// 这里只是离线 / 接口异常 / 字段缺失时的 fallback，须与后端 api/rule_defaults.go 的
// defaultFoodTalksSeriesRuleConfig() / defaultFoodTalksSeriesRuleBehavior() 保持一致。
//
// 与 FBIF（wx-theme.css / fbif.js）和 FoodTalks 网站（foodtalks-site.js）物理隔离：
// 本文件不被那两侧 import，也不 import 它们。

// ── 视觉默认（《策划文公众号排版标准》）──
export const SERIES_COLORS = Object.freeze({
  heading_color: '#02396b', // 一级/二级标题、目录标题、超链接：RGB 2 57 107
  text_color:    '#544545', // 正文：RGB 84 69 69
  muted_color:   '#888888', // 推荐阅读/参考来源/图注/作者说明：RGB 136 136 136
  link_color:    '#02396b', // 超链接同主色，带下划线
});

export const SERIES_SIZES = Object.freeze({
  font_size:             15, // 正文
  h1_font_size:          20, // 一级标题文字
  h2_font_size:          16, // 二级标题
  toc_title_size:        16, // 「目录」二字
  caption_font_size:     12, // 图片说明 & 图片来源
  attribution_font_size: 14, // 作者、文末图源说明（斜体）
});

// 正文行高 / 两端缩进 / 段后空行间距（与 wx-theme.css 默认一致 = 统一观感）。
export const SERIES_LAYOUT = Object.freeze({
  line_height: 1.75,
  margin_x: 8,    // 两端缩进 8（规范《二·全文设置》）
  gap: 20,        // 标题前后 / 块间空行
  font_stack: "mp-quote, 'PingFang SC', system-ui, -apple-system, BlinkMacSystemFont, 'Helvetica Neue', 'Hiragino Sans GB', 'Microsoft YaHei UI', 'Microsoft YaHei', Arial, sans-serif",
});

// ── 识别正则默认（红线下放：behavior.params.<key>.default，缺省降级到此）──
// 注意：客户端用 new RegExp 编译；服务端可用 (?i) 前缀表达大小写不敏感（compileSeriesRegex 适配）。
export const SERIES_REGEX = Object.freeze({
  // 一级标题：中文序号 + 顿号/空白（一、 二、 …）
  level1_regex: '^[一二三四五六七八九十百零]+\\s*[、.．]\\s*',
  // 二级标题：括号中文序号（（一）(一) …）
  level2_regex: '^[（(]\\s*[一二三四五六七八九十百零]+\\s*[)）]',
  // 目录标题行
  toc_marker_regex: '^目录\\s*$',
  // 推荐阅读 / 相关阅读 段头
  recommended_reading_regex: '^\\s*(相关阅读|推荐阅读)\\s*[：:]\\s*$',
  // 参考来源段头（一级标题去掉「六、」前缀后命中）
  ref_section_regex: '^(参考(来源|文献|资料|链接)|信息来源)\\s*[：:]?\\s*$',
  // 参考来源条目（[1] [2] …）
  ref_item_regex: '^\\s*\\[\\d+\\]',
});

// 图注关键词（紧跟图片、或含来源前缀的短段 → 12px 灰居中图注）。
export const SERIES_CAPTION_KEYWORDS = Object.freeze([
  '图片来源', '资料来源', '数据来源', '图源', '来源',
  '制图', '截图', '图注', '注：', 'Source', 'Photo', 'Image', 'Credit',
]);

// ── 素材图（站内相对路径，渲染器按 location.origin 解析为绝对地址，复制时自动上传微信 CDN）──
// 一级标题序号图：第 N 个一级标题 → sections[N]（01.png … 12.png，与 Go 端 fmt.Sprintf 同构）。
export const SERIES_SECTIONS = Object.freeze(Object.fromEntries(
  Array.from({ length: 12 }, (_, i) => [i + 1, `/assets/foodtalks-series/sections/${String(i + 1).padStart(2, '0')}.png`])
));

// 尾部群二维码：按文章品类选一个（key = 品类中文短名，显示给侧栏选择器）。
export const SERIES_GROUPS = Object.freeze({
  '调味品': '/assets/foodtalks-series/groups/seasoning.png',
  '乳品':   '/assets/foodtalks-series/groups/dairy.png',
  '零食':   '/assets/foodtalks-series/groups/snack.png',
  '饮料':   '/assets/foodtalks-series/groups/beverage.png',
  '包装':   '/assets/foodtalks-series/groups/packaging.png',
  '奶酪':   '/assets/foodtalks-series/groups/cheese.png',
  '零售':   '/assets/foodtalks-series/groups/retail.png',
  '食品供需对接': '/assets/foodtalks-series/groups/supply-demand.png',
  '烘焙':   '/assets/foodtalks-series/groups/bakery.png',
  '功能性食品': '/assets/foodtalks-series/groups/functional.png',
  '新茶饮': '/assets/foodtalks-series/groups/tea.png',
  '冰淇淋': '/assets/foodtalks-series/groups/icecream.png',
  '配料':   '/assets/foodtalks-series/groups/ingredient.png',
  '营销':   '/assets/foodtalks-series/groups/marketing.png',
});

// 尾部作者名片：每篇文章选本文作者的名片（key = 名片人名）。
export const SERIES_CARDS = Object.freeze({
  Aimee:   '/assets/foodtalks-series/cards/aimee.jpg',
  Charlie: '/assets/foodtalks-series/cards/charlie.jpg',
  Eileen:  '/assets/foodtalks-series/cards/eileen.jpg',
  Erica:   '/assets/foodtalks-series/cards/erica.jpg',
  Estelle: '/assets/foodtalks-series/cards/estelle.jpg',
  Isaac:   '/assets/foodtalks-series/cards/isaac.jpg',
  Justine: '/assets/foodtalks-series/cards/justine.jpg',
  Kee:     '/assets/foodtalks-series/cards/kee.jpg',
  Lan:     '/assets/foodtalks-series/cards/lan.jpg',
  Rain:    '/assets/foodtalks-series/cards/rain.jpg',
  Renee:   '/assets/foodtalks-series/cards/renee.jpg',
  Soso:    '/assets/foodtalks-series/cards/soso.jpg',
  Tiki:    '/assets/foodtalks-series/cards/tiki.jpg',
  Tina:    '/assets/foodtalks-series/cards/tina.jpg',
  Yisa:    '/assets/foodtalks-series/cards/yisa.jpg',
});

// 固定带上的 Eileen 销售名片（规范《三·尾部》：作者名片 + Eileen 名片）。
export const SERIES_EILEEN_CARD = '/assets/foodtalks-series/cards/eileen.jpg';

// 尾部默认组合（规则后台 footer_default 可改；侧栏每篇可临时覆盖）。
export const SERIES_FOOTER_DEFAULT = Object.freeze({
  author_card: '',      // 默认不预选作者名片（侧栏每篇手选）
  group: '包装',         // 本系列首篇（安姆科）默认品类
  include_eileen: true, // 固定带 Eileen 名片
});

// 完整素材模块默认（镜像服务端 config.modules.ft_series_materials）。
export const SERIES_MATERIALS_DEFAULT = Object.freeze({
  sections: SERIES_SECTIONS,
  groups: SERIES_GROUPS,
  cards: SERIES_CARDS,
  eileen_card_url: SERIES_EILEEN_CARD,
  footer_default: SERIES_FOOTER_DEFAULT,
});
