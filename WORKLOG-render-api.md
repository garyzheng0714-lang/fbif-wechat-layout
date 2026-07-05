# WORKLOG — 程序化渲染 API（/api/render + /api/html-to-blocks）

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
- **规则配置**：渲染时从 Go API `GET /api/rules/active?account=fbif` 拉当前生效预设（60s 缓存），
  拉不到降级 `DEFAULT_RULE_CONFIG`——与网页端 loadActiveRulePreset 的降级行为一致。

## 改动清单

- `render-service/render.mjs` — globals 桩 + import 生产渲染模块 + renderArticle()（新增）
- `render-service/server.mjs` — HTTP 服务（POST /render、GET /healthz，127.0.0.1）（新增）
- `api/render_proxy.go` — `POST /api/render` 代理 + `POST /api/html-to-blocks` + token 校验（新增）
- `api/main.go` — 注册上述路由（一行）
- `tests/render-service.test.mjs` — 冒烟：样例 blocks → 断言内联样式/外链降级/图片保留（新增）
- `.github/workflows/deploy.yml` — 部署 render-service + systemd 单元 + RENDER_TOKEN 写 .env（待做）

## 状态

- [x] render.mjs / server.mjs
- [x] Go 端点 + 路由注册
- [x] 冒烟测试进 npm test
- [ ] 部署（systemd + workflow + 线上 curl 验证）

## 环境变量

| 变量 | 谁用 | 含义 |
|---|---|---|
| `RENDER_TOKEN` | Go + Node | 两个新端点的 Bearer token；缺失时端点拒答（503） |
| `RENDER_SERVICE_URL` | Go | Node 渲染服务地址，默认 `http://127.0.0.1:19091` |
| `RENDER_PORT` | Node | 渲染服务端口，默认 19091 |
| `RULES_API_ORIGIN` | Node | 拉取生效规则预设的 Go API 地址，默认 `http://127.0.0.1:9000` |
| `PUBLIC_DIR` | Node | public/ 目录位置，默认脚本旁 `../public` |
