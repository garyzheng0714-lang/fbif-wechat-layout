# 小标题被误判为图注（cap）根因与修复

- 日期：2026-07-01
- 触发样例：https://mp.weixin.qq.com/s/swJQCw5THd0gJVF2GOoMIQ
  （标题《开了七年，排队七年，为何它成为"餐饮界胖东来"？》）
- 状态：**根因已定位，修复已实现并通过测试**

## 现象

文章 6 个章节小标题被识别成图片图注（cap），而不是小标题（h）：
文化美好 / 产品美好 / 服务美好 / 场景美好 / 经营美好 / 生命共鸣。
用户口径"01/02 小标题被识别成图注"即此类——**夹在两张图之间的居中短标题**。
用户反馈"修了很多次修不好"。

## 根因（第一性原理）

真值来自抓取的真实 DOM（`.context/article.html`）逐元素比对：

| 类型 | 字号 | 颜色 | 对齐 | 加粗 |
|---|---|---|---|---|
| 小标题（应为 h） | **20px**（>正文17px） | 黑 rgba(0,0,0,.9) | center | `<b>` 被内层 span `font-weight:normal` 覆盖 → 实际 **normal** |
| 真图注（合影） | 14px | 灰 #888 | center | normal |
| 作者行（撰文） | 15px | 灰 #888 | center | bold |

微信排版的本质区分维度是**字号相对正文的方向**：正文恒 15–17px、图注 ≤14px，
**只有小标题上探到 18px+**。居中对图注和小标题都成立，是噪音信号。

病根在 `api/wechat_blocks.go` 的 `emit()`：

1. heading 检测 `hasHeadingFontSize(cx) && allBold && ...` 把 **allBold 当必要条件**。
   微信小标题的 leaf `<span>` 内联了 `font-weight:normal`，会覆盖外层 `<b>`/自定义字体
   （`mergeStyle` 里 `font-weight:normal` 覆盖祖先 bold），于是 `allBold=false`
   → **大字号小标题整批漏判 heading**。
2. 漏网后落入 `isCaption`：文本节点继承了行内 `<span style="text-align:center">`，
   被 `isCaption` 末尾 `for s := range cx.textStyles` 的「center + ≤40字」分支命中
   → **误判为 cap（图注）**。

前端 `fbif.js` 的"txt→h 升级"后处理明确跳过 cap（line 251-262），所以一旦后端定成
cap，前端无法挽救——这解释了为何反复修不好：以前的补丁多在图注侧调字数/关键词，
没触及"大字号小标题因 font-weight:normal 漏判 heading"这个源头。

## 修复（最小、外科手术）

只改后端 heading 检测一处：给 heading 判定并列加「大字号 + 居中」路径。

- `emit()` 里：`... && (allBold || isCenteredHeading(cx))`
- 新增 `isCenteredHeading(cx)`：块级 `cx.style.Align` 或任一文本节点 `cx.textStyles[i].Align`
  为 center 即算居中（与 `isCaption` 扫描 textStyles 的方式一致，因为微信把
  text-align 塞在内层 span 上，块级 `<p>` 常无信号）。

修复后这些标题在 heading 分支就被判为 h，根本到不了 caption 分支。

### 为什么不动 caption 侧

非居中的大字（左对齐 callout）本就不含 center 信号，`styleSignalsCaption` 不会命中
→ 自然留 txt。只有"居中大字"会被误收，而它现在被 heading 分支提前拦截。故无需在
caption 加"大字否决"（避免以防万一的冗余实现）。

### 不回归的既有契约

- `TestLargeNonBoldFontIsNotHeading`：左对齐 22px 非粗 = callout → 仍 txt（无 center）。
- `TestLargeBoldFontIsHeading` / `TestWechatNumberTitleHeadingsStaySeparateAndLongBoldBodyStaysTxt`：
  真 bold 走 allBold 路径，不受影响。
- `TestLongBoldLineIsNotHeading`：>80 字被 textLen 闸挡住。
- 真图注（14px 灰）/ 作者行（15px 灰）：字号 <18，进不了 heading 分支，仍 cap。

## 验证（全部已通过）

- `cd api && go test ./...`：PASS（含新增 `TestCenteredLargeNonBoldTitleBetweenImagesIsHeading`）。
- 真实 HTML 探针：6 个小标题 cap→h，真图注/作者行仍 cap（探针已删除）。
- 端到端 `POST /api/fetch-article`：blocks h=6 / cap=4，与探针一致。
- 真实浏览器（browser-act）导入本文：预览 `.wx-th`(标题)=6、`.wx-tc`(图注)=4；
  截图 `.context/preview-heading.png` 确认"产品美好…"等渲染为加粗左对齐小标题，
  非灰色居中图注。画面中的大号"02"是原文的设计图片（正常按 img 渲染），
  被误判的是其后的文字小标题行。
- `npm test`：584 pass / 9 fail，9 个失败在干净基线(git stash 后)完全相同，
  均为本 workspace 分支既有失败，与本次改动无关。

## 未决 / 交接

- 未提交。当前分支 `garyzheng0714-lang/baton-rouge`，等用户决定是否 commit。
- `.context/article.html`（抓取样例）与截图为 gitignored 验证素材，保留。
