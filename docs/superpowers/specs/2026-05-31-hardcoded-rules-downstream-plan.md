# 红线落地：硬编码规则数据下放服务端 · 盘点 + 分批计划（2026-05-31）

> 红线（[[project_rules_no_hardcode_redline]]）：所有规则数据不允许硬编码，必须 `/api/rules` 规则后台可配。
> 边界（用户裁定）：**数据/参数下放服务端，行为算法留引擎**（但开关/参数尽量可配）。
> 完整逐条清单见 `2026-05-31-hardcoded-rules-inventory.json`（86 项，is_rule_data=true 约 60 条）。

## 已下放（不重复做）
- FoodTalks 网站识别正则 `author/tip/ref_header/ref_item_regex` → `behavior.*`（**但代码侧仍有 fallback 字符串待清理**）
- 页脚库 `FOOTER_SEGMENTS/PRESETS` → `config.modules.fbif_footer`（编辑器已上线）

## 贯穿所有批次的【铁律】
1. **必须保留 const 作为离线兜底**：每条规则下放后，代码侧保留原值作 fallback，「优先服务端、读不到降级 const」。否则服务端字段缺失会让排版**彻底损坏**（作者行/颜色/页脚全失效）。这是盘点识别的最高风险。
2. **物理隔离（规则 19）**：`fbif.js`(公众号) 与 `foodtalks-site.js`(网站) 不互相 import、不共享 config key。下放分别写两侧各自 behavior 字段。颜色口径强制区分：公众号署名 `#396FBC`、网站署名 `#888888`、网站提示正文 `#7f7f7f`（三者独立，非别名）。改 `fbif.js` 属公众号任务（允许），但不得波及网站侧。
3. **每批必须真实验证**：`go test ./...` + `npm test` + claude chrome 真实原创/转载各 1 篇（公众号 + 网站）。

## 分批计划（按价值/风险/工作量）

### Batch 1 — 高价值·最高风险（M, 3-4天）
- Mote 署名图 URL：`rule-presets.json` 写入 `behavior.params.mote_ref_image_url.default`；`api/rule_behavior.go` + `rule-behavior.js` 常量改空串兜底
- `engine.js` footer fallback：`stockOriginal` 字面量删除，`footerBehaviorText` 改动态构造 `'*本文为'+account.name+'原创…'`
- `footerUrl` fallback、`WECHAT_TRAILER_MARKERS`/`WECHAT_TIP_SIGNATURES` → `behavior.wechat_trailer_markers`/`wechat_tip_signatures`（数组）
- 文件：rule_behavior.go, rule-presets.json, rule-behavior.js, engine.js, registry.js, rule-behavior-foodtalks.js, fbif.js
- 风险：engine.js footer fallback 影响公众号页脚转载逻辑，删字面量后服务端未配会损坏页脚

### Batch 2 — 高价值·中风险（M, 3-4天）
- `fbif.js` `RE_AUTHOR/RE_SOURCE/RE_EDITOR` + classifyDocx 参考节正则(4处) → 公众号 `behavior.params.author_line_regex/source_line_regex/editor_line_regex/ref_section_regex`
- 网站 `buildSiteFooterTipElem` prefix/suffix + `buildAuthorLineFromSource` 句式 → `behavior.templates.site_tip_prefix_fbif/site_tip_suffix/author_line_repost_prefix/author_line_repost_suffix`
- 文件：fbif.js, rule-behavior-foodtalks.js, foodtalks-site.js, handlers.go
- 风险：RE_* 下放后服务端空值不兜底 → 公众号作者/来源识别全失败

### Batch 3 — 中价值·低风险（S-M, 2-3天）
- 网站颜色：`foodtalks-site.js` 4 处 `||'#888888'` 清除（依赖服务端 `config.muted_color` 默认）；`#01396a`→`config.tip_title_color`；`#7f7f7f`→`behavior.params.tip_body_color`
- 字号：图注 12px/作者 14px/提示 14px → `config.caption_font_size/author_font_size/behavior.params.tip_font_size`；图宽 400/600 → `config.modules.image_width.portrait_px/landscape_px`
- 文案：`提示：`/`参考来源：` → `behavior.params.tip_title_default/ref_header_label`
- 图注识别词 `IMAGE_CAPTION_KEYWORDS` → `behavior.params.image_caption_keywords`
- heading 阈值 → `config.modules.heading_detect.*`
- 文件：foodtalks-site.js, rule-behavior-foodtalks.js, heading-detect.js, image-width.js, engine.js, registry.js
- 风险：muted_color 回退删除后服务端缺失 → 颜色全失效（Go config 合并须保证默认）

### Batch 4 — 低价值·可选（S, 1-2天）
- `fbif.js` DOCX 专用正则 → `behavior.params.docx_preamble_author_regex/md_metadata_skip_regex`
- 机构名 `'FBIF'` fallback → registry 各包显式 `behaviorAccountName`
- DOCX 阈值 `docx_compact_twips_threshold=160`/`docx_frontmatter_scan_limit=25` → `behavior.params.*`

## by_category 速览
| 类别 | 数量 | 渠道 |
|---|---|---|
| 识别正则 | 23 | 公众号12 / 网站8 / 共用3 |
| 句式模板 | 10 | 公众号6 / 网站4 |
| 数值参数/阈值 | 9 | 共用4 / 网站4 / 公众号1 |
| 颜色 | 7 | 网站7 |
| 字号 | 7 | 网站5 / 共用2 |
| 识别关键词 | 6 | 公众号3 / 网站1 / 共用2 |
| 机构名 | 2 | 公众号2 |
| 图片URL | 1 | 公众号(Mote署名图) |
| 链接 | 1 | 公众号(footerUrl) |

## 实施建议
逐批做、每批独立部署 + chrome 验证。**强烈建议每批新开会话**（上下文干净）。先 Batch 3（低风险、纯参数化，练手 + 建立「服务端默认 + const 兜底」范式），再 Batch 1/2（高风险核心识别/页脚），最后 Batch 4。
