
## Skill routing

When the user's request matches an available skill, ALWAYS invoke it using the Skill
tool as your FIRST action. Do NOT answer directly, do NOT use other tools first.
The skill has specialized workflows that produce better results than ad-hoc answers.

Key routing rules:
- Product ideas, "is this worth building", brainstorming → invoke office-hours
- Bugs, errors, "why is this broken", 500 errors → invoke investigate
- Ship, deploy, push, create PR → invoke ship
- QA, test the site, find bugs → invoke qa
- Code review, check my diff → invoke review
- Update docs after shipping → invoke document-release
- Weekly retro → invoke retro
- Design system, brand → invoke design-consultation
- Visual audit, design polish → invoke design-review
- Architecture review → invoke plan-eng-review
- Save progress, checkpoint, resume → invoke checkpoint
- Code quality, health check → invoke health

## Design System
Always read DESIGN.md before making any visual or UI decisions.
All font choices, colors, spacing, and aesthetic direction are defined there.
Do not deviate without explicit user approval.
In QA mode, flag any code that doesn't match DESIGN.md.

## Current Project Rules
These are current guardrails for this repo. If a user gives a newer instruction
in the active thread, the newer instruction wins. Do not promote old product
requests into this list unless the user explicitly reconfirms them as current.

1. **预览页背景必须全白** — 不要加任何灰色背景，不要 #F7F7F7，不要 #EDEDED
2. **多账号绑定，统一顶栏账号菜单（单层）** — 公众号是规则应用对象，规则是可复用资产；新增账号必须先进入 `public/js/rule-packs/registry.js`（唯一入口），并在 `api/rule_defaults.go` 成对登记账号/preset。账号切换走单层顶栏头像菜单（`public/js/account-menu.js` 的 `mountAccountMenu`，挂载点 `#acctMenu`），渠道（公众号 / 网站）降为菜单内分组标签。同模板账号原地切换，跨模板账号整页 reload。**2026-06-02 用户裁定「不要两层」，旧的「圆角渠道胶囊 + 方角规则包分段」两层切换器已删除，不要恢复。**
3. **规则服务端保存 + 本地降级** — 参数预设和 `behavior` 行为配置以服务器 `/api/rules/*` 为准，localStorage 只作为兼容与离线降级；不要再新增 `/api/config/*` 这类平行规则入口
4. **所有按钮在顶部工具栏** — 底部不放按钮
5. **不要 TOC/目录** — 已删除
6. **底部栏复刻微信** — 白底，分隔线不贯穿（max-width 680px 居中），图标颜色 #7F7F7F
7. **文章标题左对齐** — 在文章正上方，27px 粗体
8. **复制后不自动打开外部网站** — 复制按钮只复制内容；微信公众号后台只能通过显式链接手动打开
9. **复制按钮必须立即反馈** — `copyContent()` 不得等待图片上传或“更多文章”合成；这些准备工作只能提前或后台刷新复制层，点击后应立刻写剪贴板并显示复制结果
10. **表单只填数字** — 不要 px/em 后缀，颜色用 color picker
11. **不要 phone-frame 边框** — 文章直接在白底上流动
12. **原创 DOCX 作者行规则** — 首个 `作者：` 行是文章正文起点；它之前的编辑前置材料默认不显示在编辑器里
13. **Mote 作者特殊排版** — `Mote莫特` / `Mote 莫特` 触发参考来源上方固定署名图；原创作者行颜色为 `#396FBC` 且不斜体
14. **更多文章是可选规则模块** — `modules.more_articles.enabled=false` 时新文章不渲染底部更多文章；`modules.more_articles.default_cards` 保存该规则的新文章默认推荐列表。当前文章仍可在侧边栏临时替换、删除、排序和裁剪卡片。
15. **更多文章选图使用当前卡片媒体库** — `/api/fetch-article-meta` 必须返回 `image_urls`；“替换图片”候选顺序为当前卡片自定义封面、抓取头图、当前卡片正文媒体图，主文章图片只做兜底。旧卡片缺少 `image_urls` 时点击替换要按卡片链接补抓，不要显示“本文暂无图片”。
16. **URL 导入图片说明组不可拆散** — `图片 -> 图注 -> 图片来源` 必须连续归为图注；`图片来源：` / `图源：` 不能被当成正文或文章级来源
17. **URL 导入作者来源固定在提示前** — 文章级 `来源：` / `作者：` / `编辑：` 只渲染一次，位置固定在标准 `提示：` 页脚前
18. **DOCX 连续段落尊重空段语义** — 连续非空正文段落在 Word 段距较小时保持紧凑；只有原文中间有空段时，下一段前才恢复标准 `wx-gap`
19. **FoodTalks 网站规则与公众号物理隔离** — 网站渲染器（`public/js/templates/foodtalks-site.js`）、行为模板（`public/js/rule-behavior-foodtalks.js`）、页脚、署名颜色全套与公众号分离，两侧渲染器互不 import。公众号 Mote `#396FBC` 蓝色斜体不复用到网站；网站署名走 `#888888` 14px 斜体。改任一侧规则不得波及另一侧（`public/js/templates/fbif.js` 在网站任务中字节级不动）。
20. **页脚库（footer library）— 完整输出 + 区块面板交互**（2026-07-02 用户裁定交互统一，推翻旧"6 片段复选框"形态）— FoodTalks 网站文章底部页脚由本工具**完整输出**，6 片段（`tip` / `biz_coop` / `join_group` / `follow_account` / `logo` / `more_info`）。渲染主路径是 `modules.bottom_blocks`（rich_html 区块），`fbif_footer`+`buildFooterHtml` 只是兼容回退，双路径字节一致由 `tests/site-footer-parity.test.mjs` golden 守。面板单篇状态走 `public/js/site-footer-state.js`（bottom_blocks override 单合同；旧 `ft.footer.*` 键一次性迁移；服务端规则未就绪时读取零副作用）。图片片段支持单篇"替换图片/素材库选图"（`overrides[seg].fields`，保留 `{{asset#id}}` 引用形态）。
21. **网站渠道默认走 ft-default 规则包** — site.html 用 `getRulePackByPath(location.pathname)` 做渠道感知 fallback：`/ft/<任意 id>` 都落到该渠道默认包 `ft-default`。**禁止**裸用 `parsed.packId`——`/ft/default` 里的 `default` 在 registry 不存在，裸用会误 fallback 到公众号 `fbif`。
22. **所有账号（含网站渠道）共用同一 `bottom_blocks` 底部结构与交互语言，差异只在服务端默认与各渠道渲染器**（2026-07-02 扩展到网站渠道：结构与交互全渠道统一、视觉口径按渠道隔离，红线 #19 的物理隔离保留在渲染层）— FBIF / WowFood / FoodTalks系列走统一 `mountBlockPanel`，每账号在 `api/rule_defaults.go` 显式覆盖 `modules.bottom_blocks`（不覆盖则经 `mergeRuleDefaults` 继承 FBIF 基线→错渲）。拖拽排序统一用 vendor 的 SortableJS（`public/vendor/Sortable.min.js`）。
23. **微信复制安全契约（保存草稿存活）** — 更多文章卡片 `<a linktype="image">` 的 `imgurl` 恒用原始 mmbiz 封面（非微信图床会被保存草稿整卡清除）；公众号 `<img src>` 的 base64 位图安全、SVG data: 必丢（空卡占位图必须是 PNG data:）；复制层 `_footerCopy` 只允许权威重建路径回写（`_footerCopyGen` 代数守卫）。守护：`tests/wechat-draft-survival.test.mjs`，改复制层/卡片 HTML 前先跑。详见 CLAUDE.md #23 与 `WORKLOG-draft-save-regression.md`。
24. **网站渠道表格 CMS 存活契约**（2026-07-06 真值审计 foodtalks.cn/news/62475 + TinyMCE 沙盒真实粘贴实测）— FoodTalks CMS 粘贴会剥光 table/td 的内联样式，只保留 HTML 属性（border/cellpadding/align/bgcolor/width）与内容层（strong、img 的属性和 style）。site/series 表格必须走 `shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival` 开关（td 的 align/valign/bgcolor/width 属性、table cellpadding、表头文字包 strong——与 inline style 双写，预览视觉零变化）；表格内图片按 parser 的 `wpx`（Word 显示宽度）输出 `width` 属性+style 双写。公众号 fbif 不传这些开关，输出字节不变（红线 #19）。守护：`tests/table-renderer-shared.test.mjs`、`tests/foodtalks-site-docx.test.mjs`；改表格输出前先读 `WORKLOG-table-cms-survival.md`。

## Server
- Go API server 同时提供静态文件（../public）和 API
- 图片上传需要 `WECHAT_UPLOAD_ENDPOINT` 环境变量
- 规则后台需要 `ADMIN_PASSWORD` 环境变量；生产值只能放 GitHub Secret 和本地忽略提交的 `.env`
- 规则预设默认写入 `data/rule-presets.json`；可用 `RULES_STORE_PATH` 覆盖
- 启动：`cd api && go run . -port 19090`

## Deploy
- 推送 `main` 触发 `.github/workflows/deploy.yml`
- 部署会把 `LIBREOFFICE_URL`、`LIBREOFFICE_TOKEN`、`CLOUDCONVERT_API_KEY`、`ADMIN_PASSWORD` 写入服务器 `/var/www/api/.env`
- `ADMIN_PASSWORD` 缺失时部署必须失败，不能让正式端进入“规则后台未配置密码”状态
- 部署工作流必须确保 `/`、`/index.html`、`/app.html`、`/version.json` 响应头为 `Cache-Control: no-cache`
- 修改正式后台密码时，更新 GitHub Secret 后重跑部署，并用 `/api/rules/admin/login` 做 200/401 双向验证

## Rule Hygiene
- Rules in this file must describe current behavior, not historical requests.
- Mutable product preferences need source/date/status before being treated as stable.
- `behavior` 是受限规则文档，不是任意 JS 执行入口；只用于作者/来源行、Mote 署名图、原创/转载页脚提示等轻量业务规则。
- Copy behavior must not navigate away from the editor unless the current user request explicitly asks for it.
- Copy speed is a product contract: do not hide background work behind the copy button or change the button text to upload/preparation states.
- URL 导入、DOCX 段距和复制链路的排版契约必须有测试保护；新增解析规则时至少跑 `cd api && go test ./...`、`npm test`，并用真实浏览器验证用户给出的样例文章。
