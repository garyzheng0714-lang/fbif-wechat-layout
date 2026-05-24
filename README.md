# fbif-wechat-layout

![类型](https://img.shields.io/badge/%E7%B1%BB%E5%9E%8B-%E5%BE%AE%E4%BF%A1%E5%B7%A5%E5%85%B7-2563eb)
![技术栈](https://img.shields.io/badge/%E6%8A%80%E6%9C%AF%E6%A0%88-Go%20%2B%20Vanilla%20JS-0f766e)
![状态](https://img.shields.io/badge/%E7%8A%B6%E6%80%81-%E7%94%9F%E4%BA%A7%E4%BD%BF%E7%94%A8%E4%B8%AD-16a34a)
![README](https://img.shields.io/badge/README-%E4%B8%AD%E6%96%87-brightgreen)

FBIF 微信公众号排版工具，用于把 DOCX、Markdown 或文章链接转换成可复制到微信公众号编辑器的 FBIF 风格 HTML。

## 仓库定位

- **分类**：微信工具 / FBIF 内容排版工具。
- **服务对象**：FBIF 公众号编辑、内容运营和需要快速统一文章样式的协作人员。
- **服务宿主**：输出内容面向微信公众号编辑器；本仓库不是浏览器插件，也不是飞书多维表格插件。

## 最新状态（2026-05-24）

- 规则配置已从单一预设扩展为模块化规则库：前端以 `public/js/rule-presets.js` 作为 schema 和本地兜底，服务端以 `api/rule_presets.go` 兼容旧版 preset 文档和 v2 rule store。
- 密码保护的规则后台支持选择、新建、编辑、JSON 校验和应用规则；规则写入前会在前后端同时校验字段、颜色、数字范围、布尔值和模块名。
- `public/js/rule-packs/registry.js` 已收拢账号 chrome、页脚和模板入口，目前默认只注册 FBIF 账号包，后续新增账号包应先扩展该注册表。
- “更多文章”URL 输入框已支持移动端粘贴只触发 `input` 事件的场景：合法微信文章链接会自动抓取元信息，同一链接不会重复触发抓取。
- DOCX golden 输出、规则后台、规则 schema、CSS 内联和规则包注册都有 Node / Go 测试保护。

## 功能

- 解析 DOCX / Markdown，保留标题、正文、图片、超链接、列表、引用和参考来源等结构。
- 粘贴微信公众号文章或网页链接后，由 Go API 抓取并转换为结构化内容；失败时回退到备用读取路径。
- URL 导入会保留微信文章的图片说明组：`图片 -> 图注 -> 图片来源` 必须连续渲染为浅灰图注，不得把 `图片来源：` 当成正文或文章来源。
- 使用 `public/js/templates/fbif.js` 和 `public/css/wx-theme.css` 生成 FBIF 公众号样式。
- 区分预览层与复制层：预览可使用原图；复制按钮点击时立即写入当前可粘贴 HTML，CSS 内联、微信兼容结构和已准备好的图片结果会进入复制层。
- 支持图片代理、裁剪编辑、格式检测、上传重试和“更多文章”卡片合成；“更多文章”链接会抓取标题、头图和正文媒体图，选图弹窗优先使用当前卡片自己的媒体库；图片与“更多文章”合成结果在后台准备，不能阻塞复制按钮。
- “更多文章”URL 粘贴支持移动端 input-only 行为；输入合法微信文章链接后会自动抓取，重复输入同一链接不闪烁状态。
- 支持旧版 `.doc` 经 LibreOffice 服务或 CloudConvert 转换为 `.docx` 后继续解析。
- 支持多文件队列处理、品牌页脚开关和“更多文章”组合页脚。
- 支持服务端规则预设保存与切换；规则库 v2 文档可按模块组织 `theme`、`supporting`、`more_articles`、`workflow` 等配置，本地设置保留为离线降级和兼容兜底。
- 原创 DOCX 以首个 `作者：` 行作为正文起点；该行之前的编辑前置材料不进入编辑器预览。
- DOCX 连续正文段落会保留紧凑排版；只有原文空段分隔的位置才恢复标准段后间距。
- URL 导入的文章级 `来源：`、`作者：`、`编辑：` 会收集到正文末尾，并在标准 FBIF `提示：` 页脚前只渲染一次。
- 正文最后一段（包括参考来源末行）与标准 FBIF `提示：` 页脚之间保留标准段落间距，避免正文尾部贴住页脚。
- 作者为 `Mote莫特` 或 `Mote 莫特` 时，会在“参考来源”上方自动插入固定署名图片。

## 技术栈

- 前端：原生 HTML / CSS / JavaScript ES modules
- 后端：Go 1.26，标准库 `net/http`
- 测试：Node.js `node --test`、Go test
- 部署：GitHub Actions、rsync、systemd API 服务

## 项目结构

```text
.
├── public/
│   ├── index.html                  # 入口，重定向到 app.html
│   ├── app.html                    # 主工具页面
│   ├── app.css                     # 页面样式
│   ├── footer.html                 # 页脚模板
│   ├── css/wx-theme.css            # 微信内容样式
│   ├── js/
│   │   ├── engine.js               # UI 协调、批处理、页脚、复制流程
│   │   ├── parser.js               # DOCX / Markdown 解析
│   │   ├── uploader.js             # 图片上传与重试
│   │   ├── clipboard.js            # 剪贴板复制
│   │   ├── css-inline.js           # 复制前 CSS 内联
│   │   ├── crop-editor.js          # 图片裁剪编辑
│   │   ├── more-articles*.js       # “更多文章”配置与合成
│   │   ├── rule-presets.js         # 规则参数 schema 和 API 封装
│   │   ├── rule-packs/registry.js  # 账号包、页脚和模板注册表
│   │   ├── admin-panel.js          # 密码保护的规则后台 UI
│   │   └── templates/fbif.js       # FBIF 模板
│   ├── fonts/                      # Noto Sans Hans 字体文件和许可证
│   └── version.json                # 部署版本信息
├── api/
│   ├── main.go                     # API + 静态文件服务
│   ├── html2md.go                  # HTML 转 Markdown 辅助
│   └── wechat_blocks.go            # 微信文章块提取
├── tests/                          # 前端模块测试
├── DESIGN.md                       # 设计系统和交互约束
├── package.json
└── README.md
```

## 快速开始

### 仅运行静态页面

适合查看界面和测试不依赖后端 API 的流程：

```bash
cd public
python3 -m http.server 8080
```

访问：

```text
http://localhost:8080/app.html
```

### 运行 Go API 与静态文件服务

适合测试文章抓取、图片代理、旧版 `.doc` 转换等后端能力：

```bash
cd api
go run . -port 9000
```

健康检查：

```bash
curl http://127.0.0.1:9000/api/health
```

## 使用流程

1. 上传 DOCX / Markdown，或粘贴文章链接。
2. 检查预览层中的正文、图片、引用和“更多文章”卡片。
3. 原创 DOCX 默认读取首个 `作者：` 行作为作者信息；作者行会显示为蓝色、非斜体，不要在正文中重复保留。
4. URL 导入文章时，图片来源应显示为浅灰小字号图注；文章级来源、作者、编辑应在提示页脚前只出现一次。
5. 按需裁剪图片、调整排版偏好或切换页脚。“更多文章”的“替换图片”会从该卡片对应文章的媒体库里选图；旧卡片缺少媒体库时，点击替换会自动按卡片链接补抓一次。
6. 点击复制按钮后立即切换到微信公众号编辑器粘贴；图片上传和“更多文章”合成会提前或后台刷新复制层，不应让复制按钮等待。
7. 在微信公众号后台做最后预览和发布前检查。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | 健康检查 |
| `POST` | `/api/fetch-article` | 抓取文章正文并返回结构化内容或 Markdown |
| `POST` | `/api/fetch-article-meta` | 获取文章标题、封面、规范链接和正文媒体图列表，用于“更多文章”卡片与选图弹窗 |
| `GET` | `/api/image-proxy` | 代理图片，绕过浏览器跨域限制 |
| `POST` | `/api/oss-upload` | 图片上传入口；未配置上游时返回原 base64 |
| `POST` | `/api/wechat-upload` | 兼容旧名称的图片上传入口 |
| `POST` | `/api/doc-to-docx` | 旧版 `.doc` 转 `.docx` |
| `GET` | `/api/doc-cache/{hash}/{filename}` | 读取转换过程中缓存的图片 |
| `GET` | `/api/rules/active` | 读取当前服务端规则预设；前端失败时降级到本地设置 |
| `POST` | `/api/rules/admin/login` | 校验规则后台密码；成功返回 `{ "ok": true }` |
| `GET` | `/api/rules/admin/presets` | 列出规则预设，需 `X-Admin-Password` |
| `POST` | `/api/rules/admin/presets` | 保存规则预设，需 `X-Admin-Password` |
| `PUT` | `/api/rules/admin/active` | 切换当前规则预设，需 `X-Admin-Password` |
| `DELETE` | `/api/rules/admin/presets/{id}` | 删除规则预设，需 `X-Admin-Password` |

## 配置

后端按需读取以下环境变量：

| 变量 | 说明 |
| --- | --- |
| `WECHAT_UPLOAD_ENDPOINT` | 图片上传代理上游；未设置时返回原始 base64，预览仍可用 |
| `LIBREOFFICE_URL` | `.doc` 转 `.docx` 的 LibreOffice 转换服务地址 |
| `LIBREOFFICE_TOKEN` | LibreOffice 转换服务鉴权 token |
| `CLOUDCONVERT_API_KEY` | LibreOffice 不可用时的 CloudConvert fallback |
| `ADMIN_PASSWORD` | 规则后台密码；未设置时登录返回 503，后台写操作不可用 |
| `RULES_STORE_PATH` | 规则预设 JSON 文件路径；未设置时使用 `data/rule-presets.json` |

部署工作流还会使用 `SERVER_SSH_KEY`、`SERVER_HOST`、`SERVER_USER`、`DEPLOY_PATH`、`ADMIN_PASSWORD` 等 GitHub Secrets。生产规则后台密码只存放在 GitHub Secret 和本地忽略提交的 `.env` 中；不要把微信、LibreOffice、CloudConvert、服务器密钥或后台密码提交到仓库。

## 脚本与测试

前端模块测试：

```bash
npm test
```

Go API 测试：

```bash
cd api
go test ./...
```

## 快捷键

| 快捷键 | 操作 |
| --- | --- |
| `⌘ + Enter` | 复制到公众号编辑器 |
| `⌘V` | 在上传页粘贴 URL 时自动抓取 |
| `Esc` | 从预览返回上传页 |

## 部署

推送到 `main` 会触发 `.github/workflows/deploy.yml`：

1. 使用 Go 1.26 构建 `api/fbif-api`。
2. 写入 `public/version.json`，用于浏览器端检测新版并刷新缓存。
3. 通过 rsync 同步 `public/` 到服务器静态目录。
4. 同步 API 二进制到服务器 API 目录。
5. 写入 API 服务使用的 `.env` 文件；`ADMIN_PASSWORD` 缺失时部署会失败，避免正式端出现“规则后台未配置密码”。
6. 确保 Caddy 对 `/`、`/index.html`、`/app.html`、`/version.json` 返回 `Cache-Control: no-cache`，避免正式端停留在旧前端入口。
7. 重启 `fbif-api` systemd 服务。

生产规则后台密码变更流程：

1. 更新仓库的 GitHub Actions Secret：`ADMIN_PASSWORD`。
2. 同步更新本地 `.env`，便于本机冒烟验证；`.env` 已被 `.gitignore` 忽略。
3. 重跑 `Deploy to server` 工作流或推送 `main` 触发部署。
4. 用 `/api/rules/admin/login` 验证新密码返回 `200`，并用旧密码确认返回 `401`。

## 维护说明

- `DESIGN.md` 记录界面和公众号预览页的设计约束。
- 当前主模板为 `fbif`；功能描述应以 `public/app.html`、`public/js/engine.js` 和 `public/js/templates/fbif.js` 为准。
- 规则配置的字段 schema、默认值、前端校验和服务端 API 封装集中在 `public/js/rule-presets.js`；服务端持久化、v1/v2 兼容和写入校验集中在 `api/rule_presets.go`、`api/rule_config_validation.go`。
- 账号包入口集中在 `public/js/rule-packs/registry.js`；新增账号包时应同时确认页脚、头像、账号名、模板 ID 和测试覆盖。
- 复制到微信前的兼容处理集中在 `engine.js`、`clipboard.js`、`css-inline.js`、`uploader.js` 和 `more-articles.js`。`copyContent()` 不应等待图片上传或“更多文章”合成；后台准备完成后刷新复制层，下一次复制自然使用新结果。
- “更多文章”选图链路由 `api/main.go` 的 `/api/fetch-article-meta` 返回 `image_urls`，`public/js/more-articles.js` 持久化到卡片状态，`public/js/more-articles-ui.js` 合并为选图候选并处理 URL 输入自动抓取。候选顺序为自定义封面、抓取头图、当前卡片正文媒体图、主文章图片兜底。
- DOCX 作者规则集中在 `public/js/templates/fbif.js`：首个 `作者：` 行之前内容默认隐藏；`Mote莫特` / `Mote 莫特` 会触发参考来源上方的固定署名图片。
- DOCX 段距规则也在 `public/js/templates/fbif.js`：连续非空正文段落在 Word 段距较小时保持紧凑；如果中间出现空段，则下一段前恢复标准 `wx-gap`。
- URL 导入结构化块由 `api/wechat_blocks.go` 输出，`public/js/templates/fbif.js` 渲染。图片说明组必须保留为 `cap`；文章级来源、作者、编辑必须在正文尾部、标准页脚前只渲染一次；正文尾段即使没有后续正文块，也必须带 `wx-gap`，给标准 `提示：` 页脚留出段落间距。
- 排版契约需要测试保护：`api/wechat_blocks_test.go` 覆盖连续图注和图片来源，`tests/fbif-template.test.mjs` 覆盖图注样式、作者来源去重、页脚位置和 DOCX 连续段落紧凑规则。
