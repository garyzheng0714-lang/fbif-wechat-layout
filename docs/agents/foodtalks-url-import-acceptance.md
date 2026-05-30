# FoodTalks 网站渠道 · 公众号 URL 导入验收标准

> 本文件是 FoodTalks 网站渠道「微信公众号链接 → 渲染 → 复制到 FoodTalks 后台」这条链路的**长期验收基准**。
> 新会话接手此任务时，先读本文件再动手。背景由用户于 2026-05-30 注入。

## 1. 用户要的是什么（任务本质）

在 FoodTalks 网站渠道（线上 `https://fbifmp-layout.foodtalks.cn/ft/default`）里：

1. 用户粘贴一个微信公众号文章链接（URL 导入）。
2. 工具抓取正文 + 图片，按 FoodTalks 网站规则渲染出预览页。
3. 用户点「复制」，把内容粘贴到 FoodTalks 网站后台，得到**与正式发布版几乎一致的排版/格式**。

核心诉求是**通配（举一反三）**：不是针对某一篇文章打补丁，而是让任意公众号文章粘贴进来都能正确渲染并套用规则。

## 2. 基准样例（以终为始）

- 公众号原文链接：https://mp.weixin.qq.com/s/XfVOPxhsyMYZLysmJR_-Iw
- 正式发布后的网站文章：https://www.foodtalks.cn/news/62285
- 作者：Mote莫特，茶叶系列第 12 篇（Teavana / 茶瓦纳）
- **后台源代码以正式发布版为准**（完整 HTML 见 git 历史 / 用户原始消息）。这是"对"的基石，一切以它为参照。

## 3. 「一模一样」的准确含义（关键判断）

用户给的后台源码不是 100% 照搬目标，里面混了三类不该由我们渲染器输出的内容：

1. **尾部系统段落不输出**（规则 20）：源码末尾的「加入微信群 / 关注公众号 / FoodTalks logo / 点击获取更多全球食品资讯」整段，是 FoodTalks 系统发布后自动追加的。我们渲染器**永远不输出这段**，否则会和系统页脚重复。
   - 例外：仅当侧栏「输出 FBIF 公众号原创页脚」开关手动打开（`localStorage` key=`ft.outputFbifFooter='1'`）时才输出 FBIF 原创页脚段。默认关。
2. **微信导出垃圾残留不复刻**：源码里几个空的 `<div id="js_content" class="rich_media_content ...">` 是微信导出残留，无意义，不该出现在我们的输出里。
3. **颜色口径统一**（规则 19）：网站署名/图注/参考来源走 FoodTalks 原生 `#888888`（14px 斜体署名；12px 图注）。源码里参考来源/封面来源/提示页脚出现的 `#7f7f7f` 是公众号搬运痕迹色，**网站渲染不复用**，应归一到 `#888888`。公众号 Mote 的 `#396FBC` 蓝色斜体更不复用到网站。

因此验收基准是：**我们渲染器输出的"正文主体"与后台源码对齐**——
署名行 → 头图 + 图注 → 正文段落 → `h2` 小标题 → 段中图 + 图注 → 参考来源块 → 本文封面图来源 → `提示：` 原创声明页脚（到此为止）。
系统会自己追加的尾部段落我们不碰。

## 4. 正文主体逐项格式契约（来自基准源码）

- **作者署名**：`作者：Mote莫特`，14px 斜体，`#888888`，`text-align: justify`。
- **图片**：`display:block; margin:auto`，居中，`width="600"`（小图如人物可 400），保留 `data-*` 比例属性即可。**图片要绕过微信防盗链**（走我们服务器代理 / 转存，见规则 15 与近期 commit `7d127f4`）。
- **图注**：紧跟图片，12px，`#888888`，居中（`text-align:center`）。
- **图片说明组不可拆散**（规则 16）：`图片 → 图注 → 图片来源/拍摄` 连续归为图注，`图片来源：`/`图源：`/`拍摄：` 不能当正文或文章级来源。
- **正文段落**：`text-align: justify`，段间用空段（`<br/>`）分隔，对应微信原文空段语义（规则 18）。
- **小标题**：`<h2>`，`text-align: justify`。
- **站内链接**：正文里指向其他 FoodTalks 文章的链接保留为 `https://www.foodtalks.cn/news/xxxxx`（基准源码里 Tazo→62236、豆腐→62058、Tea Forté→62231、Lupicia→62186、O'Sulloc→62177 等都是站内链接形式）。
- **作者/来源固定在提示前**（规则 17）：文章级 `来源：`/`作者：`/`编辑：` 只渲染一次，位置固定在标准 `提示：` 页脚前。
- **参考来源块**：标题 `参考来源：` + 编号条目，全部 `#888888`。
- **本文封面图来源**：`本文封面图来源：Starbucks官网`，14px 斜体，归一到 `#888888`。
- **提示页脚**：`提示：` + `*本文为FBIF食品饮料创新（ID：FoodInnovation）原创《标题》，不代表FoodTalks立场，转载请联系出处。` —— 这是正文主体的**终点**。其中标题带回原文链接。

## 5. 测试纪律（强制 · 真实环境）

> 这条是用户反复强调的硬约束，违反等于没看本文件。

- **不准只在本地测**。图片抓取/转存依赖我们的服务器（绕过微信防盗链），本地拿不到真实结果。
- **必须部署到服务器后**，用 Claude-in-Chrome 连上线上 `https://fbifmp-layout.foodtalks.cn/ft/default` 做真实测试。
- 测试动作：在线上工具里粘贴公众号链接 → 等渲染 → 整页截图扫描 → 点复制 → 把复制出的 HTML 与基准后台源码逐项 diff（按第 3、4 节口径）。
- **"CSS 下发成功"≠ 完成；"build 通过"≠ 完成；"tsc 通过"≠ 完成**。只有真实浏览器上眼睛看过、复制出的 HTML 与基准对齐才算完成。
- 不做"亡羊补牢"式单篇打补丁；发现的问题要从渲染/解析规则层面修，保证通配。

## 6. 物理隔离约束（不可越界）

- 网站渲染器 `public/js/templates/foodtalks-site.js`、行为模板 `public/js/rule-behavior-foodtalks.js` 与公众号 `public/js/templates/fbif.js` **互不 import**。
- 修网站规则**不能波及公众号**；`fbif.js` 在网站任务中必须字节级不动（规则 19）。
- 渠道默认走 `ft-default` 规则包（templateId `foodtalks-site`），`/ft/<任意 id>` 都 fallback 到它；禁止裸用 `parsed.packId`（规则 21）。

## 7. 完成定义（Definition of Done）

- [x] 线上 `/ft/default` 粘贴基准链接，预览页正文主体与 https://www.foodtalks.cn/news/62285 后台源码逐项对齐（第 3、4 节口径）。
- [x] 所有图片成功绕过微信防盗链显示（不是裂图）。
- [x] 颜色全部归一到 `#888888`（网站口径），无 `#7f7f7f`/`#396FBC` 泄漏。
- [x] 不输出系统尾部段落、不输出空 `js_content` div。
- [ ] 至少再换 1 篇其他公众号文章验证通配性。（待用户提供第 2 篇链接）
- [x] 相关解析/渲染规则有测试保护（`cd api && go test ./...`、`npm test`）。

## 8. 修复记录（2026-05-30，build ffc2c66）

诊断（真实环境 `/ft/default` + 后端 `/api/fetch-article`）发现三类根因，全部修复并真机验证：

1. **图片仍是 mmbiz 原图床（致命裂图）** —— `uploader.js` 原逻辑刻意跳过 mmbiz
   （公众号场景对：mp.weixin.qq.com 内同源可用、保 GIF 动画）。网站渠道粘进
   foodtalks.cn 会触发微信防盗链占位图。
   修复：`uploadNonCdnImages` 加 `transferMmbiz` 选项，网站渠道
   （`template.id==='foodtalks-site'`）下经 `/api/image-proxy` 取回 mmbiz →
   base64 → `/api/oss-upload` 转存到 CDN；公众号渠道保持直用。
   （`uploader.js` / `engine.js`）

2. **尾部混公众号导流 + 公众号口径页脚** —— 公众号原文尾部自带「FBIF 原创页脚
   （欢迎转发朋友圈）+ 更多文章 + 1000个案例 + 预览时标签不可点」，后端原样抓入
   blocks。网站发布版把这堆删掉、换成网站口径声明。
   修复：`rule-behavior-foodtalks.js` 新增 `stripWechatTrailer`（isRepost 才切）
   + `buildSiteFooterTipElem`（按标题生成网站口径提示页脚）。
   决策：切公众号导流 + 自动生成网站页脚；封面来源能抓就抓、抓不到留空。

3. **footerUrl 空串被 fallback 成公众号页脚（真正的根）** —— `engine.js` 用
   `footerUrl || '/footer.html'`，ft-default 的 `footerUrl=''` 是 falsy，被
   fallback 到公众号 `/footer.html`，整段公众号 footer（转载声明 + 更多文章 +
   案例库）拼进复制内容。
   修复：改 `footerUrl != null ? footerUrl : '/footer.html'`，区分未声明
   （默认）与显式空串（无 footer）；空时不 fetch。（`engine.js`）

真机验收结果（复制 HTML 逐项 diff）：12 张图全 CDN、mmbiz_remaining=0、颜色
20×#888888+1×#01396a 零泄漏、作者署名/参考来源/网站口径提示页脚齐备、三类公众号
泄漏（页脚/更多文章/加群）全 false、提示页脚后无任何残留。视觉头尾两端截图核对
与基准 news/62285 一致。

测试：新增 `foodtalks-site-behavior`（切尾/补页脚/非 repost 不切/正文偶现"提示："
不误切）、`uploader`（mmbiz 转存/去重/公众号渠道不动）、`rule-pack`（ft-default
footerUrl='' 契约）共多项；`npm test` 264 全过、`go test ./...` 全过。

### 已知遗留（非阻塞）

- **mmbiz 转存是串行**（逐张 await image-proxy + upload），17 张约 40 秒。复制
  按钮本身不被阻塞（规则 9：点击即写剪贴板），但"全部图片就绪"前复制会带未转存
  的 mmbiz。后续可改并发（带并发上限）提速。
- 通配性第 2 篇验证待用户提供链接。
