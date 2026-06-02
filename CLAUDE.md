
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

## Critical Design Rules (DO NOT VIOLATE)
These have been explicitly confirmed by the user. Breaking any of these is unacceptable:

1. **预览页背景必须全白** — 不要加任何灰色背景，不要 #F7F7F7，不要 #EDEDED
2. **多账号绑定，统一顶栏账号菜单（单层）** — 公众号是规则应用对象，规则是可复用资产，新增账号必须先进入 `public/js/rule-packs/registry.js`（唯一入口），并在 `api/rule_defaults.go` 成对登记账号/preset（id 字节一致）。账号切换走**单层顶栏头像菜单**（`public/js/account-menu.js` 的 `mountAccountMenu`，挂载点 `#acctMenu`）：列出全部账号，渠道（公众号 / 网站）降为菜单内的**分组标签**，不再是独立控件。点击同模板账号（如 `FBIF公众号`↔`WowFood爆品超市`，templateId 都是 `fbif`）原地切换；跨模板账号（`FoodTalks什么是系列` / `网站默认`）整页 reload 到其 URL（红线 #19 隔离）。跨渠道编辑器快照由 `session-restore.js` 的 `beforeunload` 兜底保存（无条件注册，不依赖旧 `.channel-switcher`）。**2026-06-02 用户裁定「不要两层」，推翻旧"圆角渠道胶囊 + 方角规则包分段 两层分离（方角 vs 圆角）"契约**——旧的两层切换器（顶部 `.channel-switcher` 圆胶囊、侧栏 `.account-switcher` 方角分段）已删除，不要恢复
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
13. **Mote 作者特殊排版** — 默认 `behavior` 下，`Mote莫特` / `Mote 莫特` 触发参考来源上方固定署名图；原创作者行颜色为 `#396FBC` 且不斜体

14. **更多文章是可选规则模块** — `modules.more_articles.enabled=false` 时新文章不渲染底部更多文章；`modules.more_articles.default_cards` 保存该规则的新文章默认推荐列表。当前文章仍可在侧边栏临时替换、删除、排序和裁剪卡片。
15. **更多文章选图使用当前卡片媒体库** — `/api/fetch-article-meta` 必须返回 `image_urls`；“替换图片”候选顺序为当前卡片自定义封面、抓取头图、当前卡片正文媒体图，主文章图片只做兜底。旧卡片缺少 `image_urls` 时点击替换要按卡片链接补抓，不要显示“本文暂无图片”。
16. **URL 导入图片说明组不可拆散** — `图片 -> 图注 -> 图片来源` 必须连续归为图注；`图片来源：` / `图源：` 不能被当成正文或文章级来源
17. **URL 导入作者来源固定在提示前** — 文章级 `来源：` / `作者：` / `编辑：` 只渲染一次，位置固定在标准 `提示：` 页脚前
18. **DOCX 连续段落尊重空段语义** — 连续非空正文段落在 Word 段距较小时保持紧凑；只有原文中间有空段时，下一段前才恢复标准 `wx-gap`

19. **FoodTalks 网站规则与公众号物理隔离** — 渲染器（`public/js/templates/foodtalks-site.js`）、行为模板（`public/js/rule-behavior-foodtalks.js`）、页脚模块、署名颜色全套分离。公众号 Mote `#396FBC` 蓝色斜体不复用到网站；网站署名走 `#888888` 14px 斜体（2026-05-28 经 5 篇真实原生文章复核修正：`#7f7f7f` 是公众号搬运文痕迹色，非 FoodTalks 编辑部原生口径，依据见 `docs/superpowers/specs/2026-05-28-unified-rule-engine-design.md` §1.2）；公众号渲染器与网站渲染器之间不互相 import。修改任一侧规则不能波及另一侧——`public/js/templates/fbif.js` 在网站任务中必须字节级不动。

20. **页脚库（footer library）— 完整输出 + 可勾选片段**（2026-05-31 用户裁定，**推翻**旧"系统自动追加、永不输出关注公众号段"契约——经 6 篇真实基石复核，FoodTalks 后台源码本就带完整页脚，需由本工具复刻输出）。网站文章底部页脚由本工具**完整输出**，拆为 6 个可独立勾选片段：`tip`(提示声明) / `biz_coop`(商务合作) / `join_group`(加入微信群+二维码) / `follow_account`(关注公众号+二维码) / `logo`(FoodTalks logo) / `more_info`(点击获取更多)。固定资源 URL（6 篇基石核对 100% 一致）定义在 `public/js/rule-behavior-foodtalks.js` 的 `FOOTER_SEGMENTS`，由 `buildFooterHtml(segments)` 按开关拼接（tip 走正文流、其余在文末）。由 `config.modules.fbif_footer.segments.<key>` 控制；4 套预设 `FOOTER_PRESETS`(fbif-original/repost-simple/tip-only/none) + 左侧栏「页脚」面板勾选，不同文章选不同组合。状态写 `localStorage` key=`ft.footer.enabled`/`ft.footer.segments`（旧 `ft.outputFbifFooter` 自动迁移）。依据见 `docs/superpowers/specs/2026-05-31-ft-footer-library-and-fidelity.md`。

21. **网站渠道默认走 ft-default 规则包** — site.html 通过 `getRulePackByPath(location.pathname)` 做渠道感知 fallback：`/ft/<任意 id>` 都会落到该渠道默认包 `ft-default`（templateId 是 `foodtalks-site`）。**禁止**裸用 `parsed.packId`——`/ft/default` 这种链接里的 `default` 在 registry 不存在，裸用会 fallback 到公众号 `fbif`，整套网站模板失效。

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

## Agent skills

### Issue tracker

Issues are tracked in this repo's GitHub Issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Five canonical triage roles, each labeled with its default name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
