# FBIF 微信公众号排版工具

上传 Word 文档（.docx），一键生成符合微信公众号排版规范的 HTML，直接复制粘贴到公众号编辑器。

## 功能

- **DOCX 解析** — 纯前端解析 Word 文档，提取标题、段落、图片、超链接、列表等
- **双模板** — FBIF 公众号规范 / Mote 莫特专用，通过首页入口切换
- **双层渲染** — 预览层使用原始图片，推送层自动上传至微信 CDN 并替换 URL
- **一键复制** — 生成的 HTML 可直接粘贴到微信公众号编辑器
- **页脚模板** — 可开关的品牌页脚区块

## 目录结构

```
public/
  index.html          # 首页（模板选择入口）
  app.html            # 排版工具主页面
  footer.html         # 页脚 HTML 模板
  js/
    engine.js          # 共享引擎（DOCX 解析、图片上传、Markdown 解析、UI）
    templates/
      fbif.js          # FBIF 排版模板
      mote.js          # Mote 排版模板
data/
  gold-standard/       # 微信公众号参考样式（HTML + 分析 JSON）
docs/
  wechat-api/          # 微信公众号 API 文档
docx/                  # 测试用 Word 文档
```

## 使用方式

纯静态项目，用任意 HTTP 服务器托管 `public/` 目录即可：

```bash
# 例如用 Python
cd public && python3 -m http.server 8080

# 或用 Node
npx serve public
```

打开浏览器访问首页，选择模板，上传 .docx 文件。

## 环境变量

需要在 `.env` 中配置微信公众号相关凭据（用于图片上传至微信 CDN）。
