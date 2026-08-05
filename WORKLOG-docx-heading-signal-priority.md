# DOCX 正文加粗被误判为标题——信号优先级根治

- 日期：2026-07-05
- 触发样例：`玛黑兄弟，我最“讨厌”的茶，为什么全世界都在学它？.docx`（Mote 文章）
  正文段「**更残酷的事实是什么呢？**」被升级成 h2 小节标题。
- 状态：**已修复并提交，全量验证通过（含真实浏览器）；commit `a320e39`**

## 现象

DOCX 导入后，一段整段加粗的正文强调句被渲染成章节小标题。用户反馈这类
「规则识别」问题反复出现、每次都靠加排除条件打补丁（上一次同族问题见
`WORKLOG-heading-vs-caption-2026-07-01.md`，那次是 URL 导入 Go 侧）。

## 根因（第一性原理）

真值来自 DOCX 原始 XML（段 470）：

| 段落 | Word 样式 | 大纲级别 | 字号 | 加粗 |
|---|---|---|---|---|
| 「太阳王的密使」等 10 个真小节标题 | **标题2** | 有 | 32 (16pt) | 是 |
| 「更残酷的事实是什么呢？」 | 无 | 无 | 22 (11pt，=正文) | 是 |

链路：`fbif.js classifyDocx` → `promoteDocxBoldHeadings` → `heading-detect.js
isBoldHeading`。该启发式判据（全加粗 + ≤36字 + 非句号结尾 + 非图注）对这句
话全部通过——问号结尾被显式放行（因为疑问句标题真实存在）。

**病根不在判据不够多，而在启发式被无条件运行。** 这篇文档的作者明明用了
真·Word 标题样式（标题2×10），结构信号已经显式存在，代码却仍然对正文段跑
「整段加粗≈标题」的猜测。任何靠"再加一条排除正则"的补丁都治标：下一篇文
档总会有新的加粗强调句撞上判据。

## 修复原则：信号优先级（显式信号在场时，猜测闭嘴）

- 正文元素里存在来自 Word 标题样式/大纲级别的真标题（`k:'h'` 只可能来自
  `hasHeadingStyle || hasOutlineLevel`，见 `docx-article-model.js:77`）
  ⇒ 作者会用标题样式 ⇒ 整段加粗只是强调 ⇒ **加粗/编号启发式整体停用**。
  保真基准 = Word 里看到什么就渲染什么。
- 全文零样式标题（微信作者常见写法：没有真 h2，只有加粗段）
  ⇒ 启发式照旧补位，行为与现状逐字节一致。

判定按"文档自适应"，不是全局开关，之后不需要随新文档打补丁。

## 改动

1. `public/js/heading-detect.js`：新增纯函数 `hasExplicitDocxHeadings(elems)`
   （命名这条原则，两侧模板共用）。
2. `public/js/templates/fbif.js` `classifyDocx`：有样式标题 ⇒ 跳过
   `promoteDocxBoldHeadings`。
3. `public/js/templates/foodtalks-site.js` `classifyDocx`：有样式标题 ⇒ 给
   所有 `k:'p'` 元素打既有的 `skipHeadingPromote` 标记（渲染期
   `promoteHeuristicHeadings` 已支持该标记）。**只在 DOCX 分类口打标**，
   URL 导入 / MD 路径完全不受影响（那是 Go 侧 `wechat_blocks.go` 的领域）。

## 边界核查（改前已验证）

- 金标准 `tests/fixtures/tea-pyramid.docx`：正文小节同样全是「标题2」样式，
  正文内没有会被启发式升级的加粗段/编号段 ⇒ 门控开启但零候选，
  **golden 输出字节不变**（characterization 测试守）。
- 玛黑这篇：正文无「一、」式段落、无关键词标题段 ⇒ 门控只影响 bug 段本身。
- 已接受的取舍：若作者混用（个别小节用样式、其余用加粗），加粗小节按 Word
  实际显示渲染为加粗正文——保真于 Word 是基准，不猜作者"本来想要什么"。

## 验证清单

- [x] 新增单测：heading-detect 3 项（门控函数）+ fbif 2 项 + foodtalks-site-docx 2 项，
      正反两向都锁（有样式⇒不升级；零样式⇒启发式照旧）。
- [x] `npm test` 全量：721 pass / 0 fail（tea-pyramid golden 字节不变 ✓）。
- [x] `cd api && go test ./...`：PASS（未动 Go）。
- [x] 真实浏览器（Chrome + 本地 go 服务 :19090）导入玛黑 DOCX：
      `.wx-th` 标题数 = 10，逐一等于 DOCX 里 10 个「标题2」小节，一个不多不少；
      「更残酷的事实是什么呢？」渲染为 `<section class="wx-p wx-gap">` 内 `wx-bold`
      加粗正文（截图肉眼复核）；真标题「为什么全世界学它？」computed style
      18px/700 标题口径不变。
- [x] 文档回写最终状态。

## 交接

- 改动文件：`public/js/heading-detect.js`（+`hasExplicitDocxHeadings`）、
  `public/js/templates/fbif.js`、`public/js/templates/foodtalks-site.js`、
  三个测试文件、本 WORKLOG。
- 已随 commit `a320e39` 提交；本记录中的验证数字是 2026-07-05 当时快照，后续状态以当前测试和部署证据为准。
- 未动的同族领域：URL 导入的加粗→标题判定在 Go 侧 `api/wechat_blocks.go`
  （输入是微信 HTML，几乎不含真 h2，信号生态不同），本次刻意不碰；若日后
  URL 导入也出现「源页有真 h2 仍误升」再按同一原则处理。
