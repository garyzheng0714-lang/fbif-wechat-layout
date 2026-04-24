# FBIF 微信公众号排版工具

FBIF 微信公众号排版工具用于把 Word 文档、Markdown 或文章链接转换成符合 FBIF 公众号样式的 HTML。生成结果可预览、处理图片、组合页脚和“更多文章”卡片，并一键复制到微信公众号编辑器。

## 功能

- DOCX / Markdown 解析：提取标题、正文、图片、超链接、列表、引用、参考来源等结构。
- URL 转载：粘贴微信公众号文章或网页链接后，由 Go API 抓取内容并转换为结构化块；失败时回退到 x-reader 路径。
- FBIF 公众号模板：使用 `public/js/templates/fbif.js` 和 `public/css/wx-theme.css` 输出微信编辑器可粘贴的 HTML。
- 预览与复制分层：预览层可使用原始图片，复制层可处理图片上传、CSS 内联和微信兼容结构。
- 图片处理：支持图片代理、裁剪编辑、格式检测、上传重试和“更多文章”卡片合成。
- 旧版 `.doc` 转换：通过 LibreOffice 服务或 CloudConvert 转换为 `.docx` 后继续解析。
- 批量处理：支持多文件队列式处理。
- 页脚模板：可开关品牌页脚，并可合并“更多文章”卡片。
- 本地偏好：字体、字号、颜色、行高、字距、页脚开关和上传开关保存在 `localStorage`。
- 快捷键：支持复制、粘贴链接抓取和返回上传页。

## 技术栈

- 前端：原生 HTML/CSS/JavaScript ES modules
- 文档解析与渲染：浏览器端 JS 模块
- 后端 API：Go 1.26，标准库 `net/http`
- 测试：Node.js `node --test`、Go test
- 部署：GitHub Actions + rsync + systemd API 服务

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
│   │   ├── more-articles*.js       # “更多文章”卡片配置与合成
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

## 本地开发

### 仅运行静态页面

适合查看前端界面和不依赖 API 的流程：

```bash
cd public
python3 -m http.server 8080
```

访问 `http://localhost:8080/app.html`。

### 运行 Go API 与静态文件服务

适合测试文章抓取、图片代理、旧版 `.doc` 转换等 API：

```bash
cd api
go run . -port 9000
```

服务会同时提供 API 和 `public/` 静态文件。健康检查：

```bash
curl http://127.0.0.1:9000/api/health
```

## API

Go 服务提供以下主要接口：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/health` | 健康检查 |
| `POST` | `/api/fetch-article` | 抓取文章正文并返回结构化内容或 Markdown |
| `POST` | `/api/fetch-article-meta` | 获取文章标题和封面等轻量元信息 |
| `GET` | `/api/image-proxy` | 代理图片，绕过浏览器跨域限制 |
| `POST` | `/api/oss-upload` | 图片上传入口；未配置上游时返回原 base64 |
| `POST` | `/api/wechat-upload` | 兼容旧名称的图片上传入口 |
| `POST` | `/api/doc-to-docx` | 旧版 `.doc` 转 `.docx` |
| `GET` | `/api/doc-cache/{hash}/{filename}` | 读取转换过程中缓存的图片 |

## 环境变量

后端按需读取以下环境变量：

| 变量 | 说明 |
| --- | --- |
| `WECHAT_UPLOAD_ENDPOINT` | 图片上传代理上游；未设置时返回原始 base64，预览仍可用 |
| `LIBREOFFICE_URL` | `.doc` 转 `.docx` 的 LibreOffice 转换服务地址 |
| `LIBREOFFICE_TOKEN` | LibreOffice 转换服务鉴权 token |
| `CLOUDCONVERT_API_KEY` | LibreOffice 不可用时的 CloudConvert fallback |

部署工作流还会使用 `SERVER_SSH_KEY`、`SERVER_HOST`、`SERVER_USER`、`DEPLOY_PATH` 等 GitHub Secrets。

## 测试

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
| `⌘ + Enter` | 复制到公众号编辑器，并按页面逻辑打开微信公众平台 |
| `⌘V` | 在上传页粘贴 URL 时自动抓取 |
| `Esc` | 从预览返回上传页 |

## 部署

推送到 `main` 会触发 `.github/workflows/deploy.yml`：

1. 使用 Go 1.26 构建 `api/fbif-api`。
2. 写入 `public/version.json`，用于浏览器端检测新版并刷新缓存。
3. 通过 rsync 同步 `public/` 到服务器静态目录。
4. 同步 API 二进制到服务器 API 目录。
5. 写入 API 服务使用的 `.env` 文件。
6. 重启 `fbif-api` systemd 服务。

## Notes

- `DESIGN.md` 记录了当前界面和公众号预览页的设计约束。
- 当前代码只加载 `fbif` 模板；README 中描述的功能应以 `public/app.html`、`public/js/engine.js` 和 `public/js/templates/fbif.js` 为准。
- 不要把微信、LibreOffice、CloudConvert 或服务器部署密钥提交到仓库。
