# WORKLOG:网站表格发布后外缘虚线修复

- 日期:2026-07-06
- 状态:已完成(代码+测试+本地真实 UI 验证全过);待用户在 FoodTalks 后台粘贴一次做生产侧最终确认

## 问题

网站渠道排版的表格粘贴到 FoodTalks 后台发布后,表格**外缘**出现蓝色虚线;内部单元格边框正常(用户截图:几多全供货商清单表)。

## 根因(已诊断)

`public/js/templates/shared/table-renderer.js` 输出的 `<table>` 标签只有
`border-collapse: collapse; width: 100%; table-layout: fixed;`,**没有 `border` 属性、也没有内联 border 样式**;
边框全部内联在每个 `<td>` 上。

主流富文本编辑器(UEditor / TinyMCE 系,国内 CMS 后台常用)对**无 `border` 属性的表格**统一判定为
"无边框表格",添加虚线辅助线以便编辑时可见;FoodTalks 的发布链路把这个辅助样式带到了发布结果。
`<td>` 自带内联边框不受影响 → 正好解释"只有外缘虚线、内格正常"。

旁证:抓取 foodtalks.cn 文章页全部 3 个前台 CSS(chunk-vendors / chunk-common / index),
无任何 table 虚线规则 → 排除读者前台全局样式,锁定后台编辑器辅助线。

## 修复方案

shared renderer 加 `opts.outerBorder` 开关(默认关):
- 开启时 `<table>` 输出 `border="1"` 属性 + 内联 `border: 1px solid <borderColor>`;
- `border-collapse: collapse` 下外壳边框与单元格边框合并,同色 1px → **视觉零变化**;
- 编辑器看到 border 属性即不再判定"无边框表格",不加虚线。

调用侧:`foodtalks-site.js`、`foodtalks-series.js`(两者都粘 FoodTalks CMS)传 `outerBorder: true`;
`fbif.js` 公众号侧**不传**,输出字节不变(红线 #19)。

## 改动清单

- [x] `public/js/templates/shared/table-renderer.js`:outerBorder 开关(border="1" 属性 + 同色内联外框)
- [x] `public/js/templates/foodtalks-site.js`:传 outerBorder: true
- [x] `public/js/templates/foodtalks-series.js`:传 outerBorder: true(系列同样粘 FoodTalks CMS)
- [x] `tests/table-renderer-shared.test.mjs`:新增 2 个守护测试(输出形态 + 调用面:site/series 开、fbif 不开)
- [x] `npm test` 全绿(731/731)+ `cd api && go test ./...` ok
- [x] 浏览器验证(2026-07-06):
  - 生产渲染器(processDocx)产出新旧对比页:干净环境新旧视觉一致;模拟 CMS 虚线规则
    `table:not([border])` 下旧输出复现外缘蓝虚线、新输出免疫
  - 本地起服(:19090)真实 /ft 编辑器上传带表格 DOCX 端到端:预览表格正常、console 零报错,
    预览 DOM 确认 `border="1"` + `border: 1px solid #E5E7EB` 外框、td 边框正常

## 验证备注

- 公众号 fbif.js 未改动;shared renderer 不传 outerBorder 时输出与改动前字节一致(测试锁定)
- FoodTalks 后台无账号,生产侧最终确认需用户发布链路粘贴一次看虚线消失
