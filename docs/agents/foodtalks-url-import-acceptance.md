# FoodTalks 网站渠道 · 公众号 URL 导入验收标准

> 本文件是 FoodTalks 网站渠道「微信公众号链接 → 渲染 → 复制到 FoodTalks 后台」这条链路的**长期验收基准**。
> 新会话接手此任务时，先读本文件再动手。背景由用户于 2026-05-30 注入。

> **当前状态（2026-07-26）**：链路可用，URL 导入误判原创和新浏览器页脚默认关闭的问题已经修复；但整体验收仍为**不通过 / 未闭合**。图片逐图宽度、与 62285 的完整格式对照，以及 `figure/figcaption` 经 FoodTalks TinyMCE 真实粘贴后的存活情况仍待验证。当前结论以 [正文排版评测集](foodtalks-2026-07-23-body-layout-evaluation-set.md) 顶部和 §15.4 为准。

## 1. 用户要的是什么（任务本质）

在 FoodTalks 网站渠道（线上 `https://fbifmp-layout.foodtalks.cn/ft/ft-default`）里：

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

用户给的后台源码就是 FoodTalks 后台最终可接受的目标形态；当前规则要求排版工具完整输出正文与 FoodTalks 页脚库。这里的"一模一样"不是逐字节复刻编辑器噪声，而是按发布后台的可粘贴结构对齐：

1. **FoodTalks 页脚库完整输出**（规则 20，2026-05-31 用户裁定，2026-07-02 统一交互）：源码末尾的「资讯投稿与商务合作 / 加入微信群 / 关注公众号 / FoodTalks logo / 点击获取更多全球食品资讯」属于 FoodTalks 后台源码中的固定页脚库。当前主路径是 `modules.bottom_blocks` + 统一 `mountBlockPanel` 区块面板；`fbif_footer` 只作兼容回退。`ft-default` 的全新浏览器默认开启完整页脚。
2. **微信导出垃圾残留不复刻**：源码里几个空的 `<div id="js_content" class="rich_media_content ...">` 是微信导出残留，无意义，不该出现在我们的输出里。
3. **正文颜色口径统一**（规则 19）：网站署名/图注/参考来源走 FoodTalks 原生 `#888888`（14px 斜体署名；12px 图注）。公众号 Mote 的 `#396FBC` 蓝色非斜体署名不复用到网站。页脚库片段可保留后台源码中的固定链接色与辅助色。

因此验收基准是：**我们渲染器输出的可粘贴 HTML 与后台源码结构对齐**——
署名行 → 头图 + 图注 → 正文段落 → `h2` 小标题 → 段中图 + 图注 → 参考来源块 → 本文封面图来源 → `提示：` 原创声明 → FoodTalks 页脚库片段。

## 4. 正文主体逐项格式契约（来自基准源码）

- **作者署名**：`作者：Mote莫特`，14px 斜体，`#888888`，`text-align: justify`。
- **图片**：当前复制层输出 `figure.image > img.mc-img + figcaption`，图片 `display:block` 居中，并按规则配置与输入尺寸决定 `width`；**图片要绕过微信防盗链**（读取可走 `/api/image-proxy`，发布转存走 `/api/oss-upload`；接口见根 README“API 接口”，实测契约见 `docs/foodtalks-api-contract.md` §8，历史实现见 commit `7d127f4`）。正式样例里的图片与图注是普通段落，因此 `figure/figcaption` 是否能经 FoodTalks TinyMCE 原样存活仍是待验项，不能提前宣称与基准一致。
- **图注**：紧跟图片，12px，`#888888`，居中（`text-align:center`）。
- **图片说明组不可拆散**（规则 16）：`图片 → 图注 → 图片来源/拍摄` 连续归为图注，`图片来源：`/`图源：`/`拍摄：` 不能当正文或文章级来源。
- **正文段落**：`text-align: justify`，段间用空段（`<br/>`）分隔，对应微信原文空段语义（规则 18）。
- **小标题**：`<h2 style="text-align: justify;">标题</h2>`，**不带 `id`**（基石里的 `id="mctoc_xxx"` 是 FoodTalks 后台生成目录时自动注入的锚点，用户 2026-05-30 确认我们不输出；网站渠道默认 `mctoc.enabled=false`）。公众号原文用「整段加粗独占一段」表达小标题，渲染器靠 `isBoldHeading`（heading-detect.js）升级成 h2，详见 §13。
- **站内链接（内容差异，非格式问题，不强求）**：基石发布版里 Tazo→62236、豆腐→62058 等是**站内**链接，但这是 FoodTalks 编辑在后台手动把公众号互推链接替换成站内文章链接的结果。我们抓取拿到的是原始 `mp.weixin.qq.com` 链接，**没有映射表能知道 Tazo 对应哪篇站内文章**，因此保留原 mp 链接。用户 2026-05-30 明确："微信原文和网站正式版会有内容出入，可忽略，主要看格式"——故链接差异属预期，不臆造站内映射。
- **作者/来源固定在提示前**（规则 17）：文章级 `来源：`/`作者：`/`编辑：` 只渲染一次，位置固定在标准 `提示：` 页脚前。
- **参考来源块**：标题 `参考来源：` + 编号条目，全部 `#888888`。
- **本文封面图来源**：`本文封面图来源：Starbucks官网`，14px 斜体，归一到 `#888888`。
- **提示声明**：`提示：` + `*本文为FBIF食品饮料创新（ID：FoodInnovation）原创《标题》，不代表FoodTalks立场，转载请联系出处。`。其中标题带回原文链接。
- **FoodTalks 页脚库**：提示声明之后继续输出侧栏勾选的固定片段，包括商务合作、加入微信群、关注公众号、FoodTalks logo 和点击获取更多等。

## 5. 测试纪律（强制 · 真实环境）

> 这条是用户反复强调的硬约束，违反等于没看本文件。

- **不准只在本地测**。图片抓取/转存依赖我们的服务器（绕过微信防盗链），本地拿不到真实结果。
- **必须部署到服务器后**，用 Claude-in-Chrome 连上线上 `https://fbifmp-layout.foodtalks.cn/ft/ft-default` 做真实测试。
- 测试动作：在线上工具里粘贴公众号链接 → 等渲染 → 整页截图扫描 → 点复制 → 把复制出的 HTML 与基准后台源码逐项 diff（按第 3、4 节口径）。
- **"CSS 下发成功"≠ 完成；"build 通过"≠ 完成；"tsc 通过"≠ 完成**。只有真实浏览器上眼睛看过、复制出的 HTML 与基准对齐才算完成。
- 不做"亡羊补牢"式单篇打补丁；发现的问题要从渲染/解析规则层面修，保证通配。

## 6. 物理隔离约束（不可越界）

- 网站渲染器 `public/js/templates/foodtalks-site.js`、行为模板 `public/js/rule-behavior-foodtalks.js` 与公众号 `public/js/templates/fbif.js` **互不 import**。
- 修网站规则**不能波及公众号**；`fbif.js` 在网站任务中必须字节级不动（规则 19）。
- 渠道默认走 `ft-default` 规则包（templateId `foodtalks-site`），`/ft/<任意 id>` 都 fallback 到它；禁止裸用 `parsed.packId`（规则 21）。

## 7. 完成定义（Definition of Done）

下面的勾选项记录 **2026-05-30 当时那一轮**的验收结果，不代表 2026-07-26 的完整回归已经通过：

- [x] 线上 `/ft/ft-default` 粘贴基准链接，预览页正文主体与 https://www.foodtalks.cn/news/62285 后台源码逐项对齐（第 3、4 节口径）。
- [x] 所有图片成功绕过微信防盗链显示（不是裂图）。
- [x] 正文主体颜色归一到 `#888888`（网站口径），无公众号 Mote `#396FBC` 泄漏；页脚库可保留固定片段自带颜色。
- [x] 按页脚库配置输出 FoodTalks 固定页脚片段，不输出空 `js_content` div。
- [x] 至少再换 1 篇其他公众号文章验证通配性。（第 2 篇 `15M3-OquiygJ1w2PY2awHw` →
  发布版 `news/62244`，MATCH 碳酸饮料，作者 Pride + 编辑 Bobo，2026-05-30 真机验证）
- [x] 相关解析/渲染规则有测试保护（`cd api && go test ./...`、`npm test`）。

当前回归门禁：

- [x] URL 导入一律按转载处理，微信导流尾被正确移除。
- [x] 全新浏览器不碰开关时，`ft-default` 完整页脚默认输出。
- [ ] 图片显示宽度与真实发布样例逐图对照完成。
- [ ] 第 4 节与 62285 后台源码逐条重新比对完成。
- [ ] `figure.image > figcaption` 经 FoodTalks TinyMCE 真实粘贴并确认结构、图注与样式存活。

## 8. 修复记录（2026-05-30，build ffc2c66）

诊断（真实环境 `/ft/ft-default` + 后端 `/api/fetch-article`）发现三类根因，全部修复并真机验证：

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

## 9. 通配验证（第 2 篇，2026-05-30）

第 2 篇 `15M3-OquiygJ1w2PY2awHw`（MATCH，作者 Pride + 编辑 Bobo）真机导入复制 HTML
逐项 diff `news/62244` 后台源码：

- 图片 14 张全 CDN、mmbiz_remaining=0；颜色 28×#888888 + 1×#01396a 零泄漏；
- **作者 + 编辑两行都在**（验证带编辑行的署名结构通用）；
- **连续图注组**（`MATCH的经典款产品` + `图片来源：大塚食品`）都识别为图注，不拆散（规则 16）；
- 参考来源 [1]~[6] 全保留；网站口径提示页脚带正确标题《一瓶日本高中生…》；
- 公众号尾部（更多文章/加群/关注/投稿合作/空 js_content）全部切除、零泄漏。

**封面来源印证既定决策**：两篇基准 `news/62285`/`news/62244` 的"本文封面图来源"
都是 FoodTalks 编辑在后台手动加的——公众号原文 blocks 里都没有这一段（验证：
后端 fetch-article 返回的 blocks，参考来源后直接接"提示："）。用户给的"文章代码"
是发布版后台源码（带 `data-mce-fragment` TinyMCE 痕迹），非公众号原文。因此抓不到
→ 留空，交编辑手动补，符合「能抓就抓，抓不到留空」。

## 10. 抓取重试 + 错误脱敏（2026-05-30，build 22e08ed，已修复并真机验证）

- **抓取自动重试**（`api/main.go` `fetchDirectBlocks`）：对传输失败/空内容（微信反爬
  中间页）做 3 次带退避重试（0/600ms/1500ms），不再一遇失败就 fallback 到服务器
  未安装的 x-reader。真机：导入 MATCH 不再出现 `x-reader not found in PATH`。
- **错误脱敏**（`api/main.go` + `site.html`）：导入失败后端只记日志、返回中文友好提示
  「文章抓取失败，请稍后重试（公众号可能临时限流）」；前端再脱一层兜底，成功触发时
  清空旧错误文案。真机：`err=(empty)`、内部细节不再泄漏。

## 11. 转存耗时调查结论（重要，前次"可改并发提速"的判断已被实测推翻）

转存 14~17 张图约 40 秒。**逐层实测定位真瓶颈在上游上传服务，前端/代理层无法突破**：

| 阶段 | 实测耗时 | 是否瓶颈 |
|---|---|---|
| mmbiz 取图 `/api/image-proxy`（热缓存） | 每张 2~3ms | 否 |
| base64 上传 `/api/oss-upload` → 上游 | 每张 0.6~3.7s（随图大小） | **是** |

- 前端并发取图：冷启动触发**微信反爬限流**（6 张并发 13.5s vs 单张 0.29s），反而更慢。
- 上传并发实测：6 张串行 14.5s vs 并发 14.3s = **1.01x，无效**。
- 后端 batch（一次 POST 传多图 map）实测：4 张分传 10.1s vs batch 10.25s = **0.99x，无效**。
- 根因：`WECHAT_UPLOAD_ENDPOINT` 指向的上游上传服务**串行写 OSS**（≈165KB/s），
  该服务不在本仓库，前端并发 / 后端 batch 均被它串行化。

~~**结论**：转存提速在本仓库可改范围内做不到。~~（此结论已被 §12 推翻——真因不是
"上游串行"，而是跨地域公网慢，换同地域内网即解。）

## 12. 转存提速已解决：图床迁到杭州内网（2026-05-30，正式方案）

§11 的"上游串行"判断不准。真因是**地理 + 网络**：上传服务在杭州 ECS（cn-hangzhou），
但图床 OSS 桶 `fbif-html-share` 在北京（cn-beijing）且走**公网**——跨地域公网上传仅
~124KB/s，750KB 的图要 3.7s，14 张串行 ≈ 40s。

**解法**：新建**杭州同地域**桶 `fbif-gary-img-hz`（cn-hangzhou，公共读），上传走
**内网** endpoint，实测 **2825KB/s（提速 ~23x）**，单张 400KB 上传从 ~3000ms 降到
**145ms**。真机验证：`/ft/ft-default` 导入 MATCH，14 张图全走
`fbif-gary-img-hz.oss-cn-hangzhou`、公网 HTTP 200 可访问、排版正确性全保持。

### 运维细节（改的是服务器，不在本仓库；重装/接手必看）

- **改动对象**：服务器 `aliyun-prod-real`（121.40.214.5，杭州）上的上传服务
  `/opt/wechat-api/server.py`（"WeChat Draft API"，端口 9000，非 git 仓库，手动部署）。
  Go 后端 `WECHAT_UPLOAD_ENDPOINT=http://localhost:9000/api/wechat-upload` 转发到它。
- **server.py 改动**：`upload_to_oss` 上传 endpoint 与返回 URL 域名分离——
  `OSS_UPLOAD_ENDPOINT`（内网，传）/ `OSS_PUBLIC_HOST`（公网，读）两个新环境变量驱动，
  未设则回退旧公网行为。备份在 `/opt/wechat-api/server.py.bak.before-hz-*`。
- **服务环境变量**：用 systemd drop-in `/etc/systemd/system/wechat-api.service.d/oss-hangzhou.conf`
  注入 `OSS_BUCKET=fbif-gary-img-hz` / `OSS_UPLOAD_ENDPOINT=https://oss-cn-hangzhou-internal.aliyuncs.com`
  / `OSS_PUBLIC_HOST=fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com`。
  **`OSS_REGION=cn-beijing` 保持不变**（公众号微信 CDN 上传 `_handle_upload` 仍依赖它；
  OSS 上传链路已不读 OSS_REGION）。
- **回滚**（如需换回老桶）：删 drop-in 文件 + `systemctl daemon-reload && systemctl restart wechat-api`。
- **桶现状**：新图进杭州新桶；历史已发布文章的图仍在北京老桶 `fbif-html-share`（照常显示，
  用户决定暂不迁移）。两桶共存，互不影响。

### 待办（非本次，需用户处理）

- ⚠️ **密钥轮换**：`wechat-api.service` 内 OSS AccessKey Secret / 微信 AppSecret / API_SECRET
  为明文，且排障过程中在对话里出现过，建议轮换。
- 微信渠道（`/mp`）**确认无需改**：预览用 mmbiz + `no-referrer` 秒显（0.1s），复制用 mmbiz
  直链（公众号后台可显示 + 保 GIF），本就最优，不转存 OSS。

### 前端取图也已提速（2026-05-30，build 24ee252）——后端并发 urls 模式

§12 解决了后端上传慢，但当时前端仍逐张「image-proxy 取图→base64→上传」，整体导入
还有十几秒，瓶颈转到前端取 mmbiz 图（冷缓存被微信限流）。

**调研结论（真机实测）**：
- 后端取图单张 77~1787ms；**后端并发取图不触发微信限流**（并发 16 最快），而**浏览器
  并发取会被限流**（6 张并发 13.5s）。差异因：服务器 IP + 干净 UA + 空 Referer vs
  浏览器同域连接数限制 + 微信对浏览器来源敏感。
- 三方案实测（19 张图）：前端串行 ~20s；前端并发 13.5s（被限流）；后端串行 urls 9.6s；
  **后端并发 urls 1.3s**（最优，无限流）。

**解法**：前端网站渠道不再逐张取图，改为把 mmbiz URL 列表整体发给 `/api/oss-upload`
的 `urls` 模式；后端 `_handle_oss_upload` 的串行 for 改 `ThreadPoolExecutor` 并发
（抄微信渠道 `_handle_upload` 现成模式），服务器内部并发「download_image（空 Referer
绕防盗链）+ 杭州内网传 OSS」，返回 `{mmbiz_url: oss_url}` 映射，前端按映射替换 src。

真机验证：`/ft/ft-default` 导入 MATCH **端到端就绪 5.6s**（改前 ~40s，~7x），后端转存
占 1.3s；14 张图全走新桶、0 mmbiz/0 base64 残留、排版正确性全保持。
（`uploader.js` + 服务器 `server.py`；删除前端 `mmbizUrlToDataUrl` 死代码；
测试更新到 urls 契约 + 失败保留原链接/多图不串号用例。）

实务建议：导入后等侧栏「全部图片已就绪」再点复制；复制按钮本身不阻塞（规则 9），
但就绪前复制会带未转存的 mmbiz（粘进 foodtalks.cn 会裂）。

## 13. 章节小标题塌成正文（2026-05-30，build 17dfc62，已修复并两篇真机验证）

**用户报告**："复制出来跟基准不一样，完全没有排版标准。" 复查发现核心 bug：基准
`news/62285` 正文有 **10 个 `<h2>` 章节标题**（「2个外行，教美国人喝茶」「总结」等），
是撑起整篇骨架的 18px 粗体蓝色标题；而我们复制层把它们全输出成 `<p><strong>普通段</strong></p>`
——字号同正文、挤在段落流里，整篇章节结构塌平。

**根因**：公众号原文里小标题用「整段加粗独占一段」表达（微信编辑器无真 h2）。后端
`/api/fetch-article` 把这些标成普通 `txt` 块（只在 run 上留 `bold:true`），未识别成标题。
前端旧启发式 `detectHeadingLevel` 只认「一、二、三」中文数字编号 + 9 个固定关键词
（背景/简介/总结…）开头——真实文章的加粗标题几乎全漏。

**修复（通配）**：新增 `public/js/heading-detect.js`（无依赖纯函数模块），核心判据
`isBoldHeading`：整段所有 txt run 都 bold + 纯文本 ≤36 字 + **不以句号（。.）结尾** +
非图注 → 升级 h2。`renderHeading` 输出 `<h2 style="text-align: justify;">`，默认不带
mctoc id（`site.html` 网站渠道 `mctoc.enabled=false`）。

**关键校准（两篇真实全加粗段数据驱动，非拍脑袋）**：
- 句末标点判据从「排除所有句末标点」收窄到「仅排除句号」——疑问/感叹句标题真实存在
  （`news/62244`「毕业20年的老粉凭什么"回坑"？」以问号结尾），第一版误排除它导致少 1 个 h2。
- 被排除的「加粗强调正文」（如 MATCH「如今的增长…收窄至高中生。」）都是 43~45 字 + 句号，
  靠句号 + ≤36 字双重拦截，不会误升级。

**真机两篇逐项 diff（复制层 vs 基石）全对齐**：
- Teavana `62285`：h2=10、带 justify、无 id、「拍摄：Mote莫特」识别为图注、正文 strong 保留、
  图 12 张全杭州 CDN、mmbiz/base64=0、提示页脚/作者/参考来源齐、零泄漏、#888888×20 / #7f7f7f=0。
- MATCH `62244`：h2=4（含疑问句标题，逐字对齐基石）、作者+编辑两行、图 14 张全 CDN、#888888×28 / #7f7f7f=0。

**方法论教训（写给未来会话）**：之前几轮「已验收」都在自我验证自己定的指标
（图转 CDN、颜色归一、零泄漏），**从未把复制层与基石 DOM 逐块比对结构**，所以漏了
h2 这个维度。验收必须以 `docs/agents/baselines/62285-foodtalks-backend.html` 金标准
**逐块 diff**，不是只跑自定义指标。

**金标准文件**：`docs/agents/baselines/62285-foodtalks-backend.html`（用户提供的后台源码，
已剥离系统页脚，标注预期差异）。新会话做 diff 以它为准。

**测试**：`tests/heading-detect.test.mjs` 真实行为测试（含两篇真实标题正样本、加粗强调
正文/超长/混排/图注/疑问句边界负样本）；取代原 `foodtalks-site-template.test.mjs` 里
grep 源码字符串的弱契约（规则九）。`npm test` 291 全过、`go test ./...` 全过。

## 14. 第二轮深查（2026-05-30，build f3b2c46）：三源交叉核对 + 4 个真实差异

用户反馈"底部不一致、你没认真改、没反映规则后台"。彻底重查，以**三个真相来源交叉核对**
（不再只信自定义指标）：
1. 基石后台源码 `docs/agents/baselines/62285-foodtalks-backend.html`
2. 参考仓库 github.com/VanilMsou/FoodtalksArticleLayout（layout-tool.html，本轮 WebFetch 读了）
3. 规则后台线上配置 `/api/rules/active?account=ft-default`

### 查证清楚的关键事实（之前靠臆断的）
- **底部「加群/关注/二维码/logo/点击获取更多/商务合作Bobo」当前由排版工具完整输出**。
  2026-05-31 用户裁定推翻旧“平台自动追加、不输出”口径；验收时应检查这些 `bottom_blocks` 是否按当前区块面板状态输出，而不是把它们当泄漏切掉。
- **正文段/链接在基石里不带 color/font-size**（裸 `<p style="text-align: justify;">`），
  #333/#105b89 是平台 CSS 渲染的。→ 复制层正文段不写颜色是对的。
- **规则后台 ft-default**：muted_color=#888888、caption_font_size=12、heading_size=18、
  link_color=#0070C0、text_color=#544545（仅预览用）。署名 14px/图注 12px 是基石源码值
  （**基石 > 规则配置**：以终为始，基石源码是终点）。

### 修的 4 个真实差异（本轮 commit）
| commit | 差异 | 修复 |
|---|---|---|
| 222f375 等 | 加粗小标题塌成 `<p><strong>` | heading-detect.js：整段加粗→h2（见 §13） |
| cbae6cc | h2 带自造 mctoc id | 默认不输出 id（后台自动加） |
| 74d65b5 | 竖图宽度 600（基石 400） | 后端抓 data-h/data-ratio；adaptiveImageWidth 竖图400/横图600；image-width.js |
| 80a2269+f3b2c46 | 参考来源被拆散（前几条误标 cap、后几条带空行） | 后端 inRefSection 强制参考区走 txt（不被图注启发式吞）；前端 refMode 纳入「—说明」延续行 |

### 验证结论（真机 + processBlocks 实测）
基石 vs 复制层逐项对齐：h2=10（justify 无 id）✅；图宽 {400:1,600:11} ✅（新模块
processBlocks 实测真实数据，与基石一致）；参考来源连续无空行 ✅；图全转杭州 CDN
mmbiz=0 ✅；底部营销段不输出 ✅；#7f7f7f=0/#888888 ✅。

### 教训（写给未来会话）
- **缓存陷阱**：长期开着的 tab 运行时用首次加载的模块；改代码后必须用**全新 tab**
  （URL 自动带 `?v=<build>`）验证，或 import('/js/x.js?t='+Date.now()) 实测纯函数。
  曾因此误判图宽修复"没生效"（实际复制层用了旧缓存模块，新模块输出是对的）。
- **频繁提取触发微信反爬限流**：连续多次提取同一文章会被限流（后端抓不到正文，卡"上传"）。
  验证之间留间隔。
- **断言要有证据**：底部"平台自动加"早该用非 FBIF 文章对比证明，而非空口反复说。
