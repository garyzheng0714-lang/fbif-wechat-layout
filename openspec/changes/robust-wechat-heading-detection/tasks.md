## 1. 根因定位（已完成）

- [x] 1.1 抓取病样 `nGe2BIRRWeItV1p3O6wxOQ` 真实 HTML，逐字节对比 01/02 两标题
- [x] 1.2 确认唯一真实差异：02 少一层 `<b>`，两者都无冒号；旧判据三分支全落空
- [x] 1.3 用探针实跑 `extractArticleBlocks`：现状 01→h、02→txt，与用户截图一致

## 2. 核心修复（已完成并验证）

- [x] 2.1 `api/wechat_blocks.go`：`isCenteredTitleLikeHeading` → `isCenteredLargeHeading`，
      删除 `ContainsAny("：:")` 冒号闸门，保留字号/居中/字数/来源排除
- [x] 2.2 更新调用点与函数注释，写清「加粗/冒号是巧合信号」的历史教训
- [x] 2.3 `api/wechat_blocks_test.go`：删过拟合负向测试
      `TestCenteredLargeWithoutBoldTagIsNotHeadingNorCaption`，
      改写为真实样例正向测试 `TestCenteredLargeTitleWithoutBoldNorColonIsHeading`

## 3. 对抗性复核 + 加固（已完成）

- [x] 3.1 交 Codex 独立对抗测试，逐条实跑复核（真 bug / 臆想 / 良性代价分类，见 design.md 表）
- [x] 3.2 采纳加固一：`pt` 换算 `*1.333` → `*96/72`（13.5pt 精确=18px），守护 `TestPtFontSizeHeadingPrecision`
- [x] 3.3 采纳加固二：`isCenteredLargeHeading` 加句号/逗号收尾否决（放行 ？！），守护
      `TestCenteredLargeSentenceEndingIsNotHeadingButLabelIs`
- [x] 3.4 清理 Codex 遗留失败测试：删臆想用例、真回归转 PASS、同步 pt 换算断言

## 4. 验证（已完成，全绿）

- [x] 4.1 `cd api && go test .` PASS
- [x] 4.2 `npm test` 737/737 PASS
- [x] 4.3 服务端端到端：`POST /api/fetch-article` 真实抓取，02 块返回 `k="h"`
- [x] 4.4 真实浏览器导入病样 URL，肉眼确认 02「四大品牌差异化竞争策略拆解」渲染为加粗小标题
- [x] 4.5 加固后真实文章探针复核：01/02 仍稳判 `h`，未被句号否决误伤

## 5. 防再犯护栏（待办）

- [ ] 5.1 把决策 3 的「分类器修复协议」补进 `CLAUDE.md`/`AGENTS.md` 的规则区（标题/图注判据改动强制流程）
- [ ] 5.2 （可选，待用户拍板）沉淀标题识别回归语料目录：收集 `nGe2BIRRWeItV1p3O6wxOQ`、
      `swJQCw5THd0gJVF2GOoMIQ` 及 WORKLOG 提到的 5 篇原生文章真实 HTML 为共享 fixture，
      让每次判据改动一次性跑通全部历史病样

## 6. 上线（待用户确认）

- [ ] 6.1 补一份 WORKLOG 记录本次根因与修复（文档先行原则）
- [ ] 6.2 提交并 push main 触发部署（用户可直接 commit main）
- [ ] 6.3 部署后线上再导入一次病样 URL 复核 02 为小标题
