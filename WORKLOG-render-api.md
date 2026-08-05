# WORKLOG — 程序化渲染 API（/api/render + /api/html-to-blocks）

> 状态：**已实现并部署**。本文按时间追加，前文出现的“待做”是早期阶段快照；当前能力与限制以 README、现行 OpenSpec 和代码为准。

## 目标

让外部系统（FBIF 资讯订阅的一键发布流水线）能程序化调用本工具的排版能力：
输入中立 `Block[]`，输出与人工「复制全文」同源的内联样式微信 HTML。
需求与设计文档在资讯订阅仓库：`openspec/changes/agent-publish-pipeline/`（design.md D2、specs/layout-render）。

## 为什么这么做（决策记录）

- **渲染逻辑不做第二套实现**：样式唯一真理源是 `public/js/templates/fbif.js` + `css-inline.js`。
  `tests/fbif-template.test.mjs` 已证明渲染器只需桩 3 个 globals 即可在 Node headless 运行，
  所以做一个薄 Node 常驻服务（`render-service/`）直接 import 生产模块，人工排版改样式=机器排版同步生效。
- **对外仍单一域名**：Go API 加 `POST /api/render` 薄代理到本机 Node 服务（`RENDER_SERVICE_URL`），
  Caddy 零改动。`POST /api/html-to-blocks` 直接复用 Go 侧现成 `extractArticleBlocks`（无 Node 参与）。
- **被否掉的方案**：Go 移植渲染逻辑（必然漂移）；无头浏览器驱动网页版（重、脆）。
- **渲染流程与 engine.js copyContent 对齐**：processBlocks →（外链降纯文本）→ processForCopy →
  normalizeWechatRichHtml。不加 copyContent 末尾的零高 `<p>` 占位（那是编辑器粘贴光标 hack，
  API 建草稿不需要）。不渲染底部页脚区块（bottom_blocks 需素材库+DOM shim，发布流水线本期不带页脚；
  编辑确认时可停在草稿去后台补）。
- **外链降纯文本**：微信草稿会过滤非 mp.weixin.qq.com 的 `<a>`，与其被微信破坏版式，不如渲染时
  就把外链降为正文样式纯文本（`mp.weixin.qq.com` 链接保留）；原文链接由调用方走草稿的
  `content_source_url` 字段承载。
- **鉴权**：两个新端点都要求 `Authorization: Bearer $RENDER_TOKEN`（env，缺失=fail loud 拒答，
  沿用 ADMIN_PASSWORD 的「缺配置拒绝服务」哲学）。Node 服务只监听 127.0.0.1，双层校验。
- **规则配置**：渲染时每次都从 Go API `GET /api/rules/active?account=fbif` 拉当前生效预设，
  不做进程内缓存；拉取失败显式失败，不使用默认样式伪装成功。运营保存并激活规则后，下一次
  `/api/render` 请求立即生效，无需通知调用方或重启服务。

## 改动清单

- `render-service/render.mjs` — globals 桩 + import 生产渲染模块 + renderArticle()（新增）
- `render-service/server.mjs` — HTTP 服务（POST /render、GET /healthz，127.0.0.1）（新增）
- `api/render_proxy.go` — `POST /api/render` 代理 + `POST /api/html-to-blocks` + token 校验（新增）
- `api/main.go` — 注册上述路由（一行）
- `tests/render-service.test.mjs` — 冒烟：样例 blocks → 断言内联样式/外链降级/图片保留（新增）
- `.github/workflows/deploy.yml` — 部署 render-service + systemd 单元 + RENDER_TOKEN 写 `.env`（当时待做，已于 2026-07-05 完成）

## 状态

- [x] render.mjs / server.mjs
- [x] Go 端点 + 路由注册
- [x] 冒烟测试进 npm test（727 全绿）
- [x] 部署完成（2026-07-05）：systemd `fbif-render`（127.0.0.1:19096，MemoryMax=512M）已 enable；
  deploy.yml 接管 rsync/env/Caddy `@fbifRenderApi` 路由/重启；GitHub secret `RENDER_TOKEN` 已设；
  线上验证：无 token 401 ✓、带 token /api/render 出内联 HTML ✓、/api/html-to-blocks 分块 ✓

## 2026-07-05 补：bottom_blocks 页脚渲染（用户验收反馈）

用户看过第一版草稿后裁决：格式必须与人工排版完整一致，**包括底部拖尾区**——
第一版只渲染正文是刻意的 MVP 裁剪，现补齐：

- `/render` 默认带页脚（`withFooter=false` 可关）：`template.renderBottomFooter({articleMeta,
  isRepost, footerVisibilityOverride})`，转载自动走生产规则 `bottom_blocks` 的 **repost 预设**
  （提示声明「本文为转载文章」+ Bobo 名片）——与网页版同一数据源、同一渲染器。
- 页脚数据源：拉 `/api/rules/active` 后 `applyActiveRuleConfig` 灌进 `window._activeConfig`，
  fbif.js 的 `rawActiveModules()` 才能读到 bottom_blocks/asset_library。
- DOM shim：`globalThis.DOMParser = linkedom.DOMParser`（mp-blocks 富文本块 parseDoc 缺省路径）。
  render-service 因此有了自己的 `package.json`（唯一依赖 linkedom），部署时
  `npm install --omit=dev`，rsync 加 `--exclude=node_modules`。
- 素材相对路径（`/assets/...`）绝对化到 `ASSET_ORIGIN`（默认线上域名），
  后续由发布方逐图转存微信图床。
- **「更多文章」块本期隐藏**（`footerVisibilityOverride: {blocks:{more_articles:false}}`，
  注意块级显隐要嵌在 `.blocks` 下）：卡片依赖编辑挑选往期文章 + canvas 合成封面
  （headless 无 canvas），自动选卡列为下一期。

## 2026-07-10 补：网站渠道 packId=ft-default（content-publish-pipeline 任务 3.1–3.5）

发布流水线要把公众号文章同步排版到 FoodTalks 网站，渲染 API 补第二个渠道：

- **渠道差异全部收敛为 CHANNELS 参数表**（render.mjs），零 `if(packId===...)` 散落分支
  （依据 docs/adr/2026-07-10-share-vs-isolate-boundary.md：能力代码共用，差异只以参数进入）：

  | 参数 | fbif（公众号） | ft-default（网站） | 为什么 |
  |---|---|---|---|
  | templatePath | templates/fbif.js | templates/foodtalks-site.js | L2 渲染器物理隔离（红线 #19） |
  | rulesAccount | fbif | ft-default | L3 规则按账号 |
  | configViaOpts | 否（fbif.js 读 window._activeConfig，模板契约） | 是（opts.config，engine.js renderArticleInput 同款） | 两模板既有读法不同 |
  | footerViaTemplate | 是（renderBottomFooter 单独拼） | 否（render() 内嵌 bottom_blocks 主路径，红线 #20/#22） | 页脚出口不同 |
  | stripExternalAnchors | 是 | **否**（网站文章保留外链是正常形态） | 微信草稿过滤外链，CMS 不会 |
  | normalizeWechat | 是 | **否**（engine.js isWebsiteTemplate 同语义——归一化会误居中网站内联 logo） | 粘贴目标不同 |

- **网站 config 装配 = site.html syncEngineConfig 的 headless 等价物**（siteEngineConfig）：
  flat 参数 + behavior + 原始结构化 modules（normalizeRuleConfig 会丢 bottom_blocks 等，须走 raw）；
  mctoc 锚点强制关（与 site.html 同口径，且 genMctocId 随机、开了输出不可复现）；
  无 localStorage 单篇覆盖 → 服务端默认即最终态。
- **规则读取改为逐请求实时加载**；window._activeConfig 每请求在同步段重灌，拉取失败直接
  中止渲染（不留上一账号规则让 fbif.js 错读别家页脚，也不降级到默认版式）。交错渲染互不
  污染与“相邻两次请求立即读到新 revision”都有测试守。
- **表格 CMS 存活开关（红线 #24）确认沿 headless 路径生效**：outerBorder/cmsSurvival 写死在
  foodtalks-site.js renderTable 调用处（渠道属性，非运行时开关），API 路径自然携带；
  输出实测 table border/cellpadding + td align/bgcolor + 表头 strong 齐全。
- **fbif 逐字节不变的证明**：重构前先用旧代码 + 规则 fixture 生成 golden
  （tests/golden/render-service-fbif.html），重构后 renderArticle 同输入输出逐字节相等
  （成功/降级两条路径都比对过）；public/ 目录零改动（git diff 为空）。
- 测试：tests/render-service-site.test.mjs（8 项：网站特征/外链保留/不归一化/bottom_blocks
  页脚/withFooter=false/CMS 清洗模拟/fbif golden/交错防污染/未知 packId 400）。
  规则 fixture 由本仓库 Go API 默认种子 dump（tests/fixtures/render-service-rules-*.json）；
  样例 blocks 单一真相在 tests/fixtures/render-service-sample-blocks.mjs。npm test 792 全绿。
- server.mjs 白名单与 CHANNELS 表同源（SUPPORTED_PACK_IDS），未知 packId 400；
  本地 curl 冒烟：healthz 200 / 无 token 401 / 未知 packId 400 / 两渠道 200。
- 当时遗留的 3.6–3.8 已在后续批次关闭：真实文章同源验证通过，部署版本 `7fad2fc`，
  线上重放结果与本地同长；完整证据见 `WORKLOG-pipeline-phase1-phase2-2026-07-10.md`。

## 环境变量

| 变量 | 谁用 | 含义 |
|---|---|---|
| `RENDER_TOKEN` | Go + Node | 两个新端点的 Bearer token；缺失时端点拒答（503） |
| `RENDER_SERVICE_URL` | Go | Node 渲染服务地址，默认 `http://127.0.0.1:19091` |
| `RENDER_PORT` | Node | 渲染服务端口，默认 19091 |
| `RULES_API_ORIGIN` | Node | 拉取生效规则预设的 Go API 地址，默认 `http://127.0.0.1:9000` |
| `PUBLIC_DIR` | Node | public/ 目录位置，默认脚本旁 `../public` |

## 2026-07-24 补：FoodTalks 正文结构、HTML 复制与同稿重排

本轮针对 2026-07-23 临时文章对照中暴露的正文退化，修复的是结构数据和最终 HTML
链路，不用视觉补丁掩盖：

- `Block` 的显式标题层级进入持久载荷：`h1/h2 → 2`、`h3 → 3`、`h4-h6 → 4`；
  FoodTalks 网站渲染器按 `h2/h3/h4` 输出。纯视觉推断出的标题不伪造来源层级。
- 正文 run 保留加粗和链接；标题启发式会把链接文字计入可见文本，避免“加粗开头 +
  普通链接正文”被误升为整段标题。
- 图片使用 FoodTalks 编辑器原生
  `figure.image > img.mc-img + figcaption` 结构；图片显示宽高继续来自输入块，不再因为
  生成 HTML 而丢失。网站复制与 headless 渲染在 CSS 内联后保留这两个 CMS 原生 class；
  公众号复制仍沿用原有去 class 口径。
- 网站顶部“复制全文”改为分裂按钮。主按钮仍执行富文本复制，独立箭头展开菜单，
  菜单项为“复制 HTML”；HTML 选项复制当前规则、当前文章和当前页脚组成的最终内联
  HTML 源码。规则未确认、恢复会话缺少安全复制产物、正文为空或网站图片仍待转存时
  均 fail closed。
- 自动发布支持在安全状态下显式传 `rerender_latest: true`。相同永久 `source_key`
  复用原任务和原 FoodTalks 临时文章 ID，递增 `content_version`、清理旧渲染/审核证据，
  再按最新激活规则更新原临时文章；已发布、已拒绝、处理中或外部结果不确定的任务拒绝
  重排，避免重复稿和越过人工审核。

本轮没有恢复 TOC：项目当前规则明确不要目录，FoodTalks headless 仍关闭 `mctoc`。
也没有把旧的 7274/7276 直接判成通过；需要用上述显式重排入口生成新的临时预览后，
再按正文评测集逐项验收。
