# WORKLOG:表格 CMS 粘贴存活性系统修复(不居中 / 图片大小不一 / 样式丢失)

- 日期:2026-07-06
- 状态:已完成(代码+测试+TinyMCE 沙盒真实粘贴+真实编辑器端到端全过);待用户后台粘贴终验
- 背景:用户批评"创可贴式修复"——上轮只修了外框虚线,没有审计整条「工具输出 → CMS 粘贴清洗 → 发布」链路。本轮以发布成品为真值做全链路审计,一次修完。

## 真值审计(发布成品 foodtalks.cn/news/62475,2026-07-03 发布,表格 idx=3 即用户截图那个)

逐元素比对工具输出 vs 发布成品:

| 层 | 我们输出 | 发布成品 | 结论 |
|---|---|---|---|
| table style | border-collapse+width+fixed | `border-color:#01396a; border-style:dotted; height:1514px` | **编辑手工在 CMS 表格属性里补救过**(dotted 蓝框即用户见到的"虚线") |
| colgroup | 有(Word gridCol 比例) | **被剥光** | 列宽丢失,编辑手拖 resize(td 出现小数 height) |
| td style | border/padding/font-size/color/text-align/word-break | 只剩 `border:1px solid #cccccc` + 部分 text-align + height | **td 内联样式大面积死亡**;23/57 格有 text-align(疑编辑手工补) |
| 表头蓝底白字 | td background+color+font-weight | **全丢**,文字被人包 `<strong><span font-size:18px>` | 编辑手工重做表头 |
| 图片格居中 | td text-align + img margin:0 auto | 图片格 td 无 text-align | **"不居中"根因**(td 样式死亡的连带) |
| img style | display:block;margin:0 auto;max-width:100%;height:auto | **完整幸存**,另被加 width/height 属性,CMS 转存 96x96 缩略图(1 张 1080 漏网) | **内容层 style/标签能存活,td 层会死**;"图片大小不一"=工具丢 Word 尺寸 + CMS 转存不一致 |

关键规律:**HTML 属性(border/cellpadding/width/height)和内容层(span/strong/img style)存活;td 层 style 大面积死亡**。编辑器自建表格的形态就是属性化的(border="1" cellpadding="4")。

## 工具侧确定缺陷(与 CMS 无关,预览里就错)

1. **表格图片丢 Word 显示尺寸**:parser 只给 `w`(相对页宽百分比),三渠道表格 image renderer 全都不用——图片按原始像素渲染,原图尺寸不一 → 预览/输出大小不一。Word 里编辑统一设置的显示大小被无视。
2. 系列模板 tableRunRenderers 无 image renderer(表格内图片渲染为空)——本轮暴露不修(系列现无表格图片场景,改动需过系列 golden 口径)。

## 修复方案:「属性 + 内容层 + style」三层防御(仅网站渠道开启,公众号字节不变)

1. parser:img run 新增 `wpx`(EMU/9525 → CSS px,Word 所见显示宽度)。
2. shared renderer 新增 `opts.cmsSurvival`:
   - table 补 `cellpadding="<pad>"`(padding 兜底)
   - 每个 td 补 `align="center"`(+系列 `valign="middle"`);有列宽时 td 补 `width="N%"`(colgroup 被剥后的兜底)
   - 表头/banner 格补 `bgcolor="<headerBg>"`;headerBold 时表头文字包 `<strong>`(内容层加粗,已证存活)
   - 不开启时输出与现状字节一致(公众号锁定)
3. site image renderer:有 wpx 时输出 `width="<wpx>"` 属性 + style `width:<wpx>px`(修图片大小不一;无 wpx 时字节不变)。
4. site/series 传 `cmsSurvival: true`。
5. 暂不做:正文格文字下沉 span 字号/颜色(白字若孤活于 span 而蓝底 bgcolor 被剥会白底白字,风险>收益;若真实后台粘贴实测仍丢再上)。

## 改动清单

- [x] parser.js:img run + wpx(EMU/9525;真实 DOCX 解析验证 wpx=96)
- [x] shared/table-renderer.js:cmsSurvival 三层防御(td align/valign/bgcolor/width 属性、table cellpadding、表头 strong;不开启字节不变)
- [x] foodtalks-site.js:cmsSurvival + image wpx(width 属性+style 双写;无 wpx 字节不变)
- [x] foodtalks-series.js:cmsSurvival
- [x] 测试:+6 契约测试(存活属性/banner/公众号零泄漏/调用面/wpx 端到端/td align 端到端),737/737 全绿 + go test ok
- [x] TinyMCE 6 默认配置沙盒**真实复制粘贴**(⌘C→⌘V):TinyMCE 剥光全部 inline style(比 FoodTalks 更狠——FT 连 img style 都保留),防御层全部存活:
  `TABLE border="1" cellpadding="8"` / `HEADER TD align bgcolor width` + `<strong>` / `BODY TD align width` / `IMG width="96"` / colgroup width。
  粘贴后编辑器内肉眼可见:蓝底表头、全格居中、红蓝图同宽、列宽正确
- [x] 真实 /ft 编辑器端到端:两张原始尺寸不同(2x2/8x8)、Word 显示宽度相同(cx=914400)的图,上传带 tblLook firstRow 的 DOCX → 预览表头蓝底白字加粗、两图同宽 96px 且居中、文字格居中、列宽正确

## 验证备注

- 公众号 fbif.js 零改动;不传 cmsSurvival/wpx 时 shared 输出与改动前字节一致(测试锁定"防御层零泄漏")
- parser 表头判定契约:Word 表格需 `tblLook firstRow="1"`(Word"标题行"复选框,默认开)才渲染蓝底表头
- 已知未修(显式暴露):① fbif 公众号表格图片同样丢 wpx(微信侧未报问题,不动红线);② 系列模板表格无 image renderer(系列现无表格图片场景);③ 若真实后台实测仍丢字号,再上内容层 span 方案(td font-size 属性无 HTML 等价物)

## 部署

- commit 9ef8065,deploy run 28771321945 success(2026-07-06);
  生产 table-renderer.js 含 cmsSurvival、parser.js 含 wpx 已确认

## 待用户

- 真实 FoodTalks 后台粘贴一次验收(无账号无法代验);若仍丢字号/底色,启用内容层 span 方案
- 注意:表头蓝底需要 Word 表格开着"标题行"复选框(tblLook firstRow,Word 默认开)
