# layout-render-site — 排版工具网站模板程序化渲染（实现在本仓库）

## ADDED Requirements

### Requirement: render API 支持网站模板 packId=ft-default
`POST /api/render` MUST 接受 `packId: "ft-default"`，用生产渲染器 `public/js/templates/foodtalks-site.js` 将中立 `Block[]` 渲染为 FoodTalks 网站 HTML；渲染器 MUST 与人工排版页面 `/ft/` 渠道共用同一模块，SHALL NOT 出现第二套网站样式实现。

#### Scenario: 网站模板渲染成功
- **WHEN** 携带合法 `RENDER_TOKEN`，POST `{ title, blocks, packId: "ft-default" }`
- **THEN** 返回 200 与 `{ html }`，html 为 foodtalks-site 渲染器输出的内联样式网站 HTML

#### Scenario: 与人工排版同源
- **WHEN** 同一篇公众号文章分别经 API 渲染与人工在 `/ft/` 页面排版复制
- **THEN** 两者正文 HTML 结构与样式一致（允许仅复制层包装差异）

### Requirement: 网站输出遵守 CMS 存活契约
`packId=ft-default` 的输出 MUST 启用 `shared/table-renderer.js` 的 `outerBorder`+`cmsSurvival` 开关（td 属性与 inline style 双写），表格内图片 MUST 按 `wpx` 输出 `width` 属性+style 双写，保证经 FoodTalks CMS 粘贴清洗后版式存活（红线 #24）。

#### Scenario: 表格经 CMS 清洗后存活
- **WHEN** 渲染含表格的文章并经模拟 CMS 清洗（剥除 table/td 内联样式，保留 HTML 属性）
- **THEN** 表格边框、对齐、底色、列宽与图片宽度仍然正确

### Requirement: 网站页脚走服务端 bottom_blocks 规则
`packId=ft-default` 且 `withFooter` 未显式关闭时，输出 MUST 按 `GET /api/rules/active?account=ft-default` 的 `modules.bottom_blocks` 渲染网站页脚（红线 #20/#22）；规则拉取失败 MUST 降级默认配置并继续渲染。

#### Scenario: 页脚随服务端规则输出
- **WHEN** ft-default 规则配置了页脚区块并调用 render
- **THEN** 输出 HTML 末尾包含与网页端一致的页脚区块

### Requirement: 微信链路字节级不受影响
新增网站模板支持 SHALL NOT 改变 `packId=fbif` 的输出字节与 `public/js/templates/fbif.js` 的内容（红线 #19、#23）。

#### Scenario: fbif 回归
- **WHEN** 网站模板接入后运行 `npm test`
- **THEN** `wechat-draft-survival`、`site-footer-parity`、`fbif-template` 等既有测试全部通过，fbif 渲染输出与改动前逐字节一致

### Requirement: 未支持的 packId 显式失败
render-service MUST 维护 packId 白名单（`fbif`、`ft-default`），收到白名单外的 packId 时 MUST 返回 400 与人话错误信息，SHALL NOT 静默回退到任何默认模板。

#### Scenario: 未知 packId 被拒绝
- **WHEN** POST `packId: "ft-series"`（未接入）
- **THEN** 返回 400，错误信息指明支持的 packId 列表
