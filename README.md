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

## 功能

- 解析 DOCX / Markdown，保留标题、正文、图片、超链接、列表、引用和参考来源等结构。
- 粘贴微信公众号文章或网页链接后，由 Go API 抓取并转换为结构化内容；失败时回退到备用读取路径。
- 使用 `public/js/templates/fbif.js` 和 `public/css/wx-theme.css` 生成 FBIF 公众号样式。
- 区分预览层与复制层：预览可使用原图，复制前可处理图片上传、CSS 内联和微信兼容结构。
- 支持图片代理、裁剪编辑、格式检测、上传重试和“更多文章”卡片合成。
- 支持旧版 `.doc` 经 LibreOffice 服务或 CloudConvert 转换为 `.docx` 后继续解析。
- 支持多文件队列处理、品牌页脚开关和“更多文章”组合页脚。
- 支持服务端规则预设保存与切换；本地设置保留为离线降级和兼容兜底。

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
3. 按需裁剪图片、调整排版偏好或切换页脚。
4. 复制生成结果到微信公众号编辑器。
5. 在微信公众号后台做最后预览和发布前检查。

## API

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
| `GET` | `/api/rules/active` | 读取当前服务端规则预设；前端失败时降级到本地设置 |
| `POST` | `/api/rules/admin/login` | 校验规则后台密码 |
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
| `ADMIN_PASSWORD` | 规则后台密码；未设置时后台写操作不可用 |
| `RULES_STORE_PATH` | 规则预设 JSON 文件路径；未设置时使用 `data/rule-presets.json` |

部署工作流还会使用 `SERVER_SSH_KEY`、`SERVER_HOST`、`SERVER_USER`、`DEPLOY_PATH` 等 GitHub Secrets。不要把微信、LibreOffice、CloudConvert 或服务器密钥提交到仓库。

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
5. 写入 API 服务使用的 `.env` 文件。
6. 重启 `fbif-api` systemd 服务。

## 维护说明

- `DESIGN.md` 记录界面和公众号预览页的设计约束。
- 当前主模板为 `fbif`；功能描述应以 `public/app.html`、`public/js/engine.js` 和 `public/js/templates/fbif.js` 为准。
- 复制到微信前的兼容处理集中在 `clipboard.js`、`css-inline.js` 和上传相关模块中。
