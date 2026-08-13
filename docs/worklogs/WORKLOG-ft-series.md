# 工作文档 · 网站渠道「什么是系列」排版包（ft-series）

> 开始：2026-06-08 ｜ 状态：**已提交并部署到生产**（commit `1713214`，部署 run 27119761494 全步骤 ✓）
>
> 已落地：注册 / 样式 / 商务合作底部 / BD 素材（本地浏览器验证 + go test + npm 537 全绿）。
> 搁置：**视频 UI 实测**（代码已随包上线，`extractVideo` 默认 false 对公众号/ft-default 零影响，golden 守，待真实带视频文章验证）。
> 遗留：商务合作「相关合作」3 行表、BD 切换器（默认 Eileen，换 soso/tiki 侧栏选择器待接）。
> 以上“搁置 / 遗留 / 生产终验缺口”是 2026-06-08 的历史快照，不自动成为当前需求。后续全渠道已经统一到 `modules.bottom_blocks` + `mountBlockPanel`；当前账号、页脚和验收边界以 README、CLAUDE.md 与现行源码为准，不能照本文件恢复旧 `fbif_footer` 主路径。

## 目标
在 **FoodTalks 网站渠道（channel `ft`）** 新增一个「什么是系列」排版规则包 `ft-series`，
与现有 `ft-default`（网站默认/普通文章）并列。让工具能按官方《Foodtalks【什么是系列】排版要求》
产出与 8 篇真实 `static.foodtalks.cn` 成品一致的网站排版。

## 关键前提（一开始踩过的坑，务必记住）
- **公众号与 FoodTalks 网站是两个物理隔离的渠道（红线 #19）**。
- 公众号侧已有 `foodtalks-series`（channel `mp`，序号图标题/深蓝#02396b，给微信后台粘贴）——**这是另一回事，本任务完全不碰它**。
- 被否方案：❌「拿网站成品去校准公众号 foodtalks-series 的颜色」。那不是色差，是两个渠道本就不同。
- 正确落点：✅ 网站渠道新增 `ft-series` 包，与 `ft-default` 并列；`ft-default`（搬运文）保持 `#888888` 不变（红线 #19），系列走自己原生口径 `#7f7f7f`。

## 真值依据（8 篇成品逐元素核对，2026-06-08）
| 项 | 真值 | 出处 |
|---|---|---|
| 正文色 | `#333333` | 成品 color 243 处 |
| 灰字（图注/参考/署名） | `#7f7f7f` | 成品 168 处（**非** ft-default 的 #888888） |
| 表格框线 | `#7f7f7f` | border-color 91 处 |
| 表头底色 | `#01396a` | background 13 处（已是网站 default） |
| 参考来源字号 | 16px | docx 规范 |
| 二维码下方说明 | 14px | docx 规范 |
| 图注 | 12px 灰居中 | docx + 成品 |
| 一级标题 | 标准 `<h2>` 文字（**非**公众号序号图） | 成品全是 heading-2/3/4 |
| 视频 | `<video width:100% max-height:480px border-radius:8px controls>` + 来源 12px #7f7f7f | 安姆科成品 |
| 商务合作 | 标题#01396a + 创意/公司/产品内容策划文案 + 合作方案表 + BD名片 + 群二维码 + 关注公众号 + logo + 更多 | 成品尾部 |

固定素材 URL（成品高频）：logo `static.foodtalks.cn/uploads/images/202007/e155b78...png`；
关注公众号二维码 `static.foodtalks.cn/image/post/c6ce3f3...png`；群二维码按品类变。
BD 名片：用户给的 Eileen.png/soso.jpg/tiki.png（满宽 banner）。

## 架构决策
- **复用网站渲染器 `foodtalks-site.js`，不新建渲染器**。成品标题是标准 h2/h3 文字、骨架与现有网站渲染器一致，差异只在 config + 视频/商务合作两处。新建会大量重复（违背简单原则）。
- 差异表达：①颜色/字号 → config；②视频块 → 共享层 `docx-article-model.js` + `foodtalks-site.js` 加 video 分支（config 开关，`ft-default` 默认不变）；③商务合作 → 网站页脚库 `fbif_footer`/`bottom_blocks` segments+presets。
- 注册机制：`account_catalog.go` 加 accountMeta（放 `ft-default` 之后保证 ft 渠道默认仍是 ft-default，红线 #21）→ `defaultRuleAccounts` 自动派生账号 → `rule_defaults.go` 加 preset → `npm run gen:registry` 生成 `registry.js`。

## 进度
- [x] **第 1 块·注册 ft-series 包**（✅ 已验证）
  - `rule_presets.go`：加常量 `foodTalksSeriesSiteRuleAccountID="ft-series"` / `...DefaultRulePresetID="ft-series-v1"`
  - `account_catalog.go`：加 accountMeta（channel ft，templateId 复用 foodtalks-site）
  - `rule_defaults.go`：加 `ft-series-v1` preset 登记 + `defaultFoodTalksSeriesSiteRuleConfig/Behavior`；`currentBuiltinRuleVersion` 7→8
  - ✅ `gen:registry`（registry.js 含 ft-series）+ 重启（PID 64890，本地 `ADMIN_PASSWORD=devlocal`，main 不读 .env，一致性校验通过）
  - ✅ `curl /api/rules/active?account=ft-series` 返回 text_color#333333 / muted#7f7f7f / border#7f7f7f / header#01396a / caption12 / tablefs15
  - ⚠️ **发现**：`ref_font_size=16` 被服务端 config **白名单清洗**掉（返回 None）→ 第 2 块需另寻参考字号落地方式（注册白名单键 or 复用已有键）
- [x] **第 2 块·样式校准**（✅ 已浏览器验证）
  - 关键认知：系列成品是**极简结构化 HTML（裸标签）**，样式靠 FoodTalks 后台 CSS；只有图注/视频来源/表格/商务合作才内联。8 篇成品 justify=0、正文裸 `<p>`。
  - 加 config `justify_paragraphs`（默认 true=ft-default 口径不变；ft-series=false 裸标签）：四处链路 = 服务端白名单(rule_config_validation.go) + 服务端默认(rule_defaults.go) + 前端 schema(rule-document.js FIELD_BY_KEY) + 渲染器出口清理(foodtalks-site.js)。
  - 撤销误加的 `ref_font_size`（成品正文/参考是裸 p，非内联；该键已被服务端白名单清洗）。
  - ✅ 浏览器验证：正文/标题/参考 inline justify 全部去除（bodyTA=start），标题 #01396a，参考灰色，无破坏。
  - **踩坑（重要）**：①前端 config 白名单 `rule-document.js` FIELD_BY_KEY 需同步加新键，否则被过滤、渲染器读不到；②**模块缓存**——`version.json` build 固定 `"dev"` → 模块 `?v=dev` 强缓存，改前端 JS 后必须 bump `version.json` 的 build 才能让浏览器加载新模块（否则验证看到的是旧 JS）。
- [~] **第 3 块·视频块**（代码完成 + 编译/语法过；浏览器验证待真实带视频输入）
  - 后端 `wechat_blocks.go`：`<video src poster>`（含子 `<source>`）→ `Block{K:"video"}`（URL 路径）。
  - 前端 `foodtalks-site.js`：`renderVideo`（成品口径 `width:100% max-height:480px border-radius:8px controls`）+ render video 分支 + classifyBlocks(URL) + adaptNeutralToSite(docx)；视频来源说明复用 cap（12px #7f7f7f）。
  - 共享层 `docx-article-model.js`：`extractVideo` 开关（默认 false=公众号字节不变，红线#19）；网站 `buildDocxOpts` 传 true，「（插入视频）」超链接→video elem（href 作 src、前图作 poster）。
  - ⚠️ **验证缺口**：URL 路径需真实带视频的 FoodTalks 文章 URL；docx 路径需真实含视频源 docx 确认超链接 href 结构。渲染输出按安姆科成品真值写、由 golden 测试守。
- [x] **第 4 块·商务合作底部版块**（✅ 已浏览器验证：内容/图片/默认输出全对）
  - 当时实现：`rule_ft_series_footer.go` 的 `defaultFtSeriesBizCoopFooterModule` 先落到 `fbif_footer` 兼容结构；后续已由统一 `bottom_blocks` 合同接管，不能把这里的过渡实现当现行架构。
  - 当时 site.html 从 `fbif_footer.enabled` 初始化；当前状态与默认值以服务端 `modules.bottom_blocks` 和统一面板为准。
  - ✅ 浏览器验证：清 localStorage 后商务合作仍默认输出，标题/创意策划文案/创意名片图/BD名片Eileen/群二维码/公众号二维码banner/logo/更多 全部渲染，图片 src 全解析。
  - **遗留**：①「相关合作」3 行表暂未纳入（标注待表格 HTML 精修）②BD 切换器（默认 Eileen，换 soso/tiki 的侧栏选择器待接）③侧栏页脚面板 active_segments 兜底含 FBIF 的 tip（无害，segment 内容是 ft-series；面板 label 为 ft-default 设计）。
- [x] **第 3 块·视频块** 见上（代码完成+编译/语法过；UI 验证待真实带视频输入）
- 进度补记 ↓
  - 成品结构（「对话神探伍伍」尾部，逐元素真值）：
    1. 标题「什么是系列商务合作」18px #01396a
    2. 创意内容策划文案 2 段（含「合作方案」→/news/30894 &「合作案例」→/news/32980 链接）
    3. 创意名片图 `static.foodtalks.cn/image/post/78cbc7b7c8bd705db8f43f057c548506d823.png`
    4.「相关合作」表（3 行：公司策划 /news/30894+/news/32980 · 视频策划 · 食品雷达 /news/30826）
    5.「更多合作，请点击」→/news/31065
    6.「加入FBIF功能性食品群」+ 群二维码 `…/image/news/4ae6499ea33bf7bf68435dad193e9c01002c.png` +「添加请备注：姓名 公司 职位」
    7.「关注FoodTalks什么是系列公众号」+ 二维码 `…/image/post/c6ce3f375f1399792325ccd09105b8c52986.png`
    8. FoodTalks logo `…/uploads/images/202007/e155b7852c10a72f7b73157fdf9c4bf0.png`
    9.「点击获取更多」全球食品资讯 →foodtalks.cn
  - **BD 名片（Eileen/soso/tiki）**：本篇成品**无** BD 名片；docx 规范「此外还有BD选择」=可选项。**待用户确认**：每篇是否放 BD 名片、放哪张、插在版块何处、不同 BD 文案差异。
  - 实现方案：仿 `defaultFBIFFooterModule` 给 ft-series 做商务合作 footer segments（biz_coop_intro 大固定块 + coop_table + bd_card 可选 + join_group + follow_account + logo + more_info），可勾选。素材用成品 static.foodtalks.cn URL（网站 transferMmbiz 只转 mmbiz、foodtalks.cn 图保留）。
- [x] 第 5 块·BD 名片等素材已落地；当前服务端素材与页脚校验覆盖 Eileen / Soso / Tiki 和独立“相关合作”表
- [x] 第 6 块·golden / fidelity 测试与当时浏览器验收已完成；后续回归以当前测试和统一 `bottom_blocks` 契约为准

## 待解风险
- `foodtalks-site.js` 已 699 行（上限 900），加视频/参考字号后若超需拆分。
- 正文 `#333333` 是否要逐 `<p>` 内联（成品靠 lark 容器给色）——需浏览器实测粘贴目标。
- 商务合作完整固定文案需从成品尾部完整抽取（第 4 块）。
- behavior 暂复用网站基线（tip 文案是 FBIF 口径），系列 tip 文案待按成品调。
