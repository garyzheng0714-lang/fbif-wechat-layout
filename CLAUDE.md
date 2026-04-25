
## Skill routing

When the user's request matches an available skill, ALWAYS invoke it using the Skill
tool as your FIRST action. Do NOT answer directly, do NOT use other tools first.
The skill has specialized workflows that produce better results than ad-hoc answers.

Key routing rules:
- Product ideas, "is this worth building", brainstorming → invoke office-hours
- Bugs, errors, "why is this broken", 500 errors → invoke investigate
- Ship, deploy, push, create PR → invoke ship
- QA, test the site, find bugs → invoke qa
- Code review, check my diff → invoke review
- Update docs after shipping → invoke document-release
- Weekly retro → invoke retro
- Design system, brand → invoke design-consultation
- Visual audit, design polish → invoke design-review
- Architecture review → invoke plan-eng-review
- Save progress, checkpoint, resume → invoke checkpoint
- Code quality, health check → invoke health

## Design System
Always read DESIGN.md before making any visual or UI decisions.
All font choices, colors, spacing, and aesthetic direction are defined there.
Do not deviate without explicit user approval.
In QA mode, flag any code that doesn't match DESIGN.md.

## Critical Design Rules (DO NOT VIOLATE)
These have been explicitly confirmed by the user. Breaking any of these is unacceptable:

1. **预览页背景必须全白** — 不要加任何灰色背景，不要 #F7F7F7，不要 #EDEDED
2. **仅保留 FBIF 公众号排版** — Mote 模板已移除，不再做模板切换
3. **规则服务端保存 + 本地降级** — 参数预设以服务器 `/api/rules/*` 为准，localStorage 只作为兼容与离线降级；不要再新增 `/api/config/*` 这类平行规则入口
4. **所有按钮在顶部工具栏** — 底部不放按钮
5. **不要 TOC/目录** — 已删除
6. **底部栏复刻微信** — 白底，分隔线不贯穿（max-width 680px 居中），图标颜色 #7F7F7F
7. **文章标题左对齐** — 在文章正上方，27px 粗体
8. **复制后不自动打开微信后台** — 只显示复制成功提示，保留手动打开入口
9. **表单只填数字** — 不要 px/em 后缀，颜色用 color picker
10. **不要 phone-frame 边框** — 文章直接在白底上流动

## Server
- Go API server 同时提供静态文件（../public）和 API
- 图片上传需要 WECHAT_UPLOAD_ENDPOINT 环境变量
- 启动：`cd api && go run . -port 19090`
