# FBIF 微信公众号排版工具

上传 Word 文档（.docx）或粘贴文章链接，一键生成符合微信公众号排版规范的 HTML，直接复制粘贴到公众号编辑器。

## 功能

- **DOCX / Markdown 解析** — 纯前端解析 Word 文档和 Markdown，提取标题、段落、图片、超链接、列表等
- **URL 转载** — 粘贴微信文章或网页链接，自动抓取内容并重新排版
- **双模板** — FBIF 公众号规范 / Mote 莫特专用，顶部 pill 按钮切换
- **双层渲染** — 预览层使用原始图片，复制层自动上传至微信 CDN 并替换 URL
- **一键复制** — 生成的 HTML 可直接粘贴到微信公众号编辑器（⌘+Enter 快捷键）
- **批量处理** — 支持多文件同时上传，队列式逐个处理
- **页脚模板** — 可开关的品牌页脚区块
- **偏好记忆** — 自动记住模板选择、页脚开关、缩放比例

## 键盘快捷键

| 快捷键 | 操作 |
|--------|------|
| `⌘ + Enter` | 复制到公众号 |
| `⌘V` (上传页) | 自动检测 URL 并抓取 |
| `Esc` | 从预览返回上传 |

## 目录结构

```
public/
  index.html          # 入口（重定向到 app.html）
  app.html            # 排版工具主页面（上传 + 预览）
  footer.html         # 页脚 HTML 模板
  js/
    engine.js          # 共享引擎（DOCX 解析、图片上传、Markdown 解析、UI）
    parser.js          # 文档解析模块
    uploader.js        # 图片上传模块（微信 CDN）
    clipboard.js       # 剪贴板复制模块
    image-utils.mjs    # 图片格式检测工具
    templates/
      fbif.js          # FBIF 排版模板
      mote.js          # Mote 排版模板
api/                   # Go API 服务器（图片代理、文章抓取、配置管理）
data/
  gold-standard/       # 微信公众号参考样式（HTML + 分析 JSON）
docs/
  wechat-api/          # 微信公众号 API 文档
docx/                  # 测试用 Word 文档
DESIGN.md              # 设计系统（色板、字体、间距、交互规范）
```

## 线上地址

https://fbifmp-layout.garyzheng.com

## 部署

推送到 `main` 分支后，GitHub Actions 自动通过 rsync 将 `public/` 同步到服务器 `/var/www/wechat-layout/`。

服务器使用 Caddy 托管静态文件，API 代理到 `localhost:9000`。

## 本地开发

纯静态项目，用任意 HTTP 服务器托管 `public/` 目录即可：

```bash
cd public && python3 -m http.server 8080
```

## 环境变量

服务器端需要配置微信公众号相关凭据（用于图片上传至微信 CDN）。
