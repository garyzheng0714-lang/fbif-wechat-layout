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
  heading_color:     '#02396b', // 一级/二级标题、目录标题、超链接：RGB 2 57 107
  text_color:        '#544545', // 正文：RGB 84 69 69
  muted_color:       '#888888', // 推荐阅读/参考来源/图注/原创声明：RGB 136 136 136
  link_color:        '#02396b', // 超链接同主色，带下划线
  footer_note_color: '#7f7f7f', // 文末署名 / 图源说明小字：RGB 127 127 127（成品实测口径）
});

export const SERIES_SIZES = Object.freeze({
  font_size:             15, // 正文
  h1_font_size:          20, // 一级标题文字
  h2_font_size:          16, // 二级标题
  toc_title_size:        16, // 「目录」二字
  caption_font_size:     12, // 图片说明 & 图片来源
  attribution_font_size: 14, // 推荐阅读块说明（斜体）
  footer_note_font_size: 12, // 文末署名 / 图源说明小字（斜体，成品实测口径）
});

// 正文行高 / 两端缩进 / 段后空行间距（与 wx-theme.css 默认一致 = 统一观感）。
export const SERIES_LAYOUT = Object.freeze({
  line_height: 1.75,
  margin_x: 8,    // 两端缩进 8（规范《二·全文设置》）
  image_margin_x: 0, // 图片两端缩进：什么是系列满列；规则 image_margin_x 可改（FoodTalks食品资讯与正文同为 8）
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
  // 参考来源段头（一级标题去掉「六、」前缀后命中；也用于无序号的「参考来源：」段兜底）
  ref_section_regex: '^(参考(来源|文献|资料|链接)|信息来源)\\s*[：:]?\\s*$',
  // 参考来源条目（[1] [2] …）
  ref_item_regex: '^\\s*\\[\\d+\\]',
  // 联系小节标题（含「联系」二字 + 倒数第二个一级标题 → 其下公司信息居中）
  contact_section_regex: '联系',
  // 公司信息行（地址 / 官网 / 电话 …）→ 居中；描述性正文不匹配 → 保持左对齐
  contact_field_regex: '^(地址|地點|地点|官网|官網|网址|網址|网站|網站|电话|電話|联系电话|联系方式|邮箱|郵箱|邮件|手机|手機|微信|公众号|公眾號|传真|傳真|Tel|Phone|Email|E-?mail|Add|Address|Web|Website)\\s*[：:]',
  // 文末「提示：」标签行（成品：15px 深蓝粗体）
  tip_label_regex: '^提示\\s*[：:]\\s*$',
  // 文末「* …」原创/转载声明条目（成品：15px #888888 斜体）
  tip_item_regex: '^\\s*[*＊·]',
  // 文末署名 / 图源说明（成品：12px #7f7f7f 斜体小字）
  footer_note_regex: '^本文(?:未标注来源图片/数据均来自|作者\\s*[：:]|图片来源)|图片均来自',
  // 商务合作导联行（2026-06-11 飞书反馈：检测到该句式整行替换为 biz_coop_replacement 文案）
  biz_coop_replace_regex: '^\\s*商务合作对接请点击',
});

// ── 前置物料剥离默认（红线下放：behavior.params.content_start_signal / front_matter_scan_limit）──
// 「什么是」系列文章无「作者：」行，正文以头图为锚点：头图之前的标题段 + 单格副标题表格
// 都是编辑前置物料（进微信后台标题/摘要字段），不进正文。须与 api/rule_defaults.go 默认一致。
export const SERIES_FRONTMATTER = Object.freeze({
  content_start_signal: 'firstImage', // 'firstImage' = 头图为正文起点；'authorLine' = 旧公众号语义
  front_matter_scan_limit: 25,        // 找头图的最大非空块扫描数，超出则不剥离（不吞内容）
});

// ── 行为文案默认（红线下放：behavior.templates.<key>）──
// 推荐阅读块下方补充说明（规范《二·七·推荐阅读》：14px 灰斜体）。须与 api/rule_defaults.go 一致。
export const SERIES_TEMPLATES = Object.freeze({
  recommended_reading_note: '查看更多推荐阅读，请拉至文末点击“阅读原文”访问FoodTalks网站',
  // 商务合作导联行替换文案；{contact_section} 自动取文中「联系XX」标题（2026-06-11 飞书反馈）。
  biz_coop_replacement: '如有合作意向，请移至“{contact_section}”获取联系方式',
});

// ── 「FoodTalks食品资讯」公众号排 FoodTalks 网站稿（behavior.params.site_article_layout=true 才用，
// 2026-10-27；见 rule-behavior-foodtalks-series.js adaptSiteArticle）。须与 api/rule_defaults.go
// defaultFoodTalksMpRuleBehavior() 一致。
export const SITE_ARTICLE_REGEX = Object.freeze({
  // 署名行（网站开头的 作者：/编辑：…，编辑手排时逐行挪到文末）
  credit_line_regex: '^(本文来源|来源|出处|出品|作者|撰文|撰稿|文|编辑|责编|责任编辑|微信编辑|新媒体编辑|编者|审核|审校|复核|校对|翻译|编译|采访|设计|视觉|整理|统筹|策划)\\s*[：:｜|]\\s*\\S',
  // 网站转载稿开头一句「本文为X原创，作者：Y，转载请联系出处。」
  site_byline_regex: '^本文为(.{1,40}?)原创',
  // 网站文末起点：提示 / 投稿 / 加群 / 关注公众号 / FoodTalks 标语
  site_footer_start_regex: '^(提示\\s*[：:]\\s*$|[*＊]\\s*资讯投稿与商务合作|加入微信群\\s*$|关注公众号\\s*$|点击获取更多全球食品资讯)',
  // 网站文末声明「*本文为X（ID：…）原创《原文标题》，不代表FoodTalks立场…」
  site_tip_source_regex: '^[*＊]?\\s*本文为(.{1,40}?)原创(?:《(.{1,120}?)》)?',
  // 自家来源 = FoodTalks 原创
  own_source_regex: '(?i)^foodtalks',
});

export const SITE_ARTICLE_TEMPLATES = Object.freeze({
  source_credit: '来源：{source}{source_id}',
  repost_tip: '*本文为{source}原创{author}，转载请联系出处。{original_title}',
  original_tip: '*本文为FoodTalks原创，转载请底部留言。',
  contact_tip: '*投稿与商务合作请在后台回复“商务合作”。',
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
  '食品供需对接': '/assets/foodtalks-series/groups/supply-demand.png',
  '烘焙':   '/assets/foodtalks-series/groups/bakery.png',
  '功能性食品': '/assets/foodtalks-series/groups/functional.png',
  '新茶饮': '/assets/foodtalks-series/groups/tea.png',
  '冰淇淋': '/assets/foodtalks-series/groups/icecream.png',
  '配料':   '/assets/foodtalks-series/groups/ingredient.png',
  '营销':   '/assets/foodtalks-series/groups/marketing.png',
  '渠道':   '/assets/foodtalks-series/groups/channel.png',
  '代工':   '/assets/foodtalks-series/groups/oem.png',
  '酒类':   '/assets/foodtalks-series/groups/alcohol.png',
  '食品资讯': '/assets/foodtalks-series/groups/news.png',
  '方便速食': '/assets/foodtalks-series/groups/convenience.png',
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

// 作者署名别名：文章里写 Ian，素材库沿用既有 Lan 名片。
export const SERIES_AUTHOR_CARD_ALIASES = Object.freeze({
  Ian: 'Lan',
});

// 尾部默认组合（规则后台 footer_default 可改；侧栏每篇可临时覆盖）。
export const SERIES_FOOTER_DEFAULT = Object.freeze({
  author_card: '',     // 默认不预选作者名片（侧栏每篇手选）
  group: '包装',        // 本系列首篇（安姆科）默认品类
  bd_card: 'Eileen',   // 默认 BD 名片（复用 cards 名片库，规则后台 footer_default.bd_card 可改）
});

// 完整素材模块默认（镜像服务端 config.modules.ft_series_materials）。
export const SERIES_MATERIALS_DEFAULT = Object.freeze({
  sections: SERIES_SECTIONS,
  groups: SERIES_GROUPS,
  cards: SERIES_CARDS,
  author_card_aliases: SERIES_AUTHOR_CARD_ALIASES,
  footer_default: SERIES_FOOTER_DEFAULT,
});
