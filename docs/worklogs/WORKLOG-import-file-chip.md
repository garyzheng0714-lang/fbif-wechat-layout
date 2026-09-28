> 2026-09-28 起界面设计以 Su Design 的 DESIGN.md 为准，本文里的视觉、组件库与设计稿规则作废。

# 导入区「已选文件条」重做 + CSS 缓存版本化

- 日期：2026-07-05
- 触发：用户多次反馈「DOCX 导入后顶部导入区显示不太对」，附截图（灰色蒙层盖住
  DOC 图标、文件名截断、DOC 字被裁）。
- 状态：**已完成并线上验证**（两层根因均修复，均已部署到生产并真实浏览器复验）

## 两层根因

### 第一层：CSS 卡在旧缓存（已修复并上线）

现象里的「满宽灰块」是**旧版 app-shell.css** 的设计。磁盘/线上其实早已是新版
（140×84 卡片 + 悬停浮层），但浏览器/代理按启发式缓存把旧 CSS 一直复用。

- 病根：每个 JS import 都带 `?v=assetVersion`，部署后必刷新；但 `<head>` 里的
  `<link rel=stylesheet>` 是裸 URL（无 `?v=`），URL 不变 → 旧缓存遮挡。生产 Caddy
  的 `@fbifVersionedAssets` 匹配 `path /js/* /css/*` + `query v=*`，本就为带版本的
  CSS 备了不可变长缓存，只是 `<link>` 从未带版本。
- 修复：`app.html` / `site.html` 在算出 assetVersion 后给同源 stylesheet `<link>`
  追加 `?v=assetVersion`。commit `00f18c7`，已部署。
- **线上实测（https://fbifmp-layout.foodtalks.cn）**：version.json=`00f18c7`，
  app.html 含该逻辑，`app-shell.css?v=00f18c7...` 正常加载，卡片 140×84、
  默认蒙层 opacity=0。**缓存问题已根治。**

### 第二层：文件条设计本身不自解释（本次修）

即便新 CSS 正确加载，用户仍觉得「不对」——因为新版设计本身有问题：

1. 文件名**默认不可见**，要鼠标悬停才浮出深色蒙层显示（违反 DESIGN.md #4 自解释）；
2. 悬停蒙层是深灰半透明，盖住图标，观感与旧 bug 几乎一样 → 用户以为「没变化」；
3. DOC 扩展名在 34px 小图标里**被裁切**（"DOC"→"]OC"，违反 #8 细节考究）。

## 本次改动（文件条重做）

`public/css/app-shell.css` 的 `.dropzone-file*` 一组：从「140px 卡片 + 悬停深色蒙层」
改为**一行始终可见的文件条**：`[DOC 文档徽标] 文件名.docx（省略号截断） [× 移除]`。

- 去掉 `:hover`/`:focus-within` 的 opacity 浮层切换与深色蒙层；overlay 容器改为
  静态 flex（名字 + 移除常显）。
- 图标改 38×46 文档徽标 + dog-ear，`DOC` 用 11px 底部对齐，**不再裁字**。
- 文件名 `--fs-text-1` 深色、`text-overflow: ellipsis`，始终可读。
- DOM 不动（`data-uploaded-file/-name/-file-ext/-remove-upload` 全保留）→
  `engine.js` 零改动。用 DESIGN.md 的 less-but-better / 自解释验收。

## 验证清单

- [x] 第一层缓存修复：线上 curl + 真实浏览器实测通过（见上）
- [x] 文件条重做：本地真实浏览器验证——真实导入玛黑 DOCX，文件条 342×68 横向一行，
      DOC 图标不裁字、文件名 opacity=1 深色始终可见 + 省略号截断、× 移除按钮点击后
      回到空态提示（截图肉眼复核）
- [x] `npm test`：723 pass / 0 fail
- [x] 上线后线上真实浏览器复验：生产 version.json=`77fab4d`，CSS 带 `?v=77fab4d`，
      文件条 342×68 横向一行、DOC 不裁字、文件名 opacity=1 深色始终可见 + 省略号、
      × 移除按钮（截图肉眼复核）
- [x] 文档回写最终状态

## 结论

- commit `00f18c7`（CSS 缓存版本化）+ `77fab4d`（文件条重做），均已部署到
  `https://fbifmp-layout.foodtalks.cn` 并线上验证。
- 用户下次正常刷新即见新文件条；无需清缓存（app.html no-cache + CSS 带版本号）。

## 关键排查记录（避免重复踩坑）

- 生产是 `https://fbifmp-layout.foodtalks.cn`（Caddy/rsync，deploy.yml），host 在
  GitHub Secret `SERVER_HOST`；仓库里查不到，靠 git 历史里的 URL 反查出来。
- 仓库 homepage 指向的 `fbif-wechat-layout.vercel.app` 是 **dev 占位部署**
  （version.json build=dev、CSS 404），**不是**真实生产，勿据它判断。
- 本会话曾出现一次读取工具输出异常；这是历史会话事故，不构成当前维护规则。后续按项目统一文件工具与真实 diff 核验，不沿用这里的临时绕法。
