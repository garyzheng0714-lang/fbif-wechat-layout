# 头条号（分发说明书）

> 由 `api/yunsuo_platform_profiles.go` 和外发排版代码生成，别手改：改了规则跑 `cd api && SPEC_UPDATE=1 go test -run TestDistributionPlatformSpec .`。块是什么见 `docs/article-blocks.md`，各平台共用的规则在 `api/yunsuo_distribution_rules.go`。

## 稿子怎么送进平台

经自己的分发服务（FBIF-多平台分发工具，在 B 机），只存草稿、不发布。编辑器每一步怎么点、找不到按钮时模型兜底的边界，见分发工具的 `docs/platforms/toutiao.md`。存完草稿当场读回核对标题、小标题、加粗、图片、图注、封面。

## 稿子要求

| 项 | 要求 |
| --- | --- |
| 标题 | 原样送，2–30 字，英文字母、数字两个算一个字；超了记为待修改，在卡片或侧边栏改 |
| 摘要 | 不查 |
| 封面 | 用公众号封面（和 FoodTalks 一样），分发服务在编辑器的封面弹窗里单独上传，不进正文。最小 452×352；差一点（放大到 1.5 倍以内能达标）自动放大一份转存，差得多记为待修改，在卡片里选正文大图或在侧边栏上传 |
| 每天上限 | 5 篇（按账号计数，人工按钮发的也算） |
| 文章链接 | 只认 `toutiao.com` `www.toutiao.com` `m.toutiao.com`；草稿发出去后人工登记；登记时可让系统经头条搜索加 TikHub 找（作者是自家号、标题对上、发文时间在存稿之后） |
| 核对 | 分发服务存完草稿当场读回，对不上的逐条记下、在审核卡上写灰字 |
| 后台内容管理页 | https://mp.toutiao.com/profile_v4/manage/content/all |

## 正文怎么写

每种块写成什么（块的意思见 `docs/article-blocks.md`）。段与段之间不插空行，平台自带段距。

| 原文 | 写成 |
| --- | --- |
| 小标题（标题1，level 0–2） | `<h1>小标题</h1>` |
| 二级标题（level 3、4） | `<p><strong>二级标题</strong></p>` |
| 整段加粗的段落 | `<p><strong>整段加粗的一句话</strong></p>` |
| 段落（含局部加粗） | `<p>段落里<strong>局部加粗</strong>的字。</p>` |
| 图片带图注 | `<div class="pgc-img"><img src="https://img.example.com/photo.jpg" alt=""><p class="pgc-img-caption">图注</p></div>` |
| 图片不带图注 | `<p><img src="https://img.example.com/photo.jpg" alt=""></p>` |
| 引用 | `<blockquote>引用的话</blockquote>` |
| 列表 | `<ul><li>第一项</li><li>第二项</li></ul>` |
| 表格 | `<table border="1" cellspacing="0" cellpadding="6"><tbody><tr><th>表头</th></tr><tr><td>内容</td></tr></tbody></table>` |
| 视频 | `<p><video controls src="https://img.example.com/clip.mp4" poster="https://img.example.com/photo.jpg"></video></p>` |

## 各平台一样的规则，在这个平台上的样子

### 署名挪到文末声明

开头不放署名。开头几块和正文最后的署名行（作者、编辑、编译稿的英文来源、中文编辑……）按原文顺序写进文末「本文为FBIF食品饮料创新原创，作者：X，编辑：Y，转载请联系授权。」

原文：

- 段落：作者：Sage
- 段落：编辑：Bobo
- 段落：正文第一段。

写成：

```html
<p>正文第一段。</p><p>本文为FBIF食品饮料创新原创，作者：Sage，编辑：Bobo，转载请联系授权。</p>
```

### 小标题编号

两个以上的标题1 编「一、二、」（结语也编）；原文已经编了号、以及参考来源，不动。

原文：

- 小标题（level 2）：市场变了
- 段落：正文。
- 小标题（level 2）：新品牌冒头
- 段落：正文。

写成：

```html
<h1>一、市场变了</h1><p>正文。</p><h1>二、新品牌冒头</h1><p>正文。</p><p>本文为FBIF食品饮料创新原创，转载请联系授权。</p>
```

### 图注与图片来源并成一条

图下面的说明和「图片来源」用「；」并成一条（前一句以！？收尾就不加），不截短；首图留在原位带图注。

原文：

- 图片
- 图注：▲ 苦味气泡水
- 图注：图片来源：品牌官网
- 段落：正文。

写成：

```html
<div class="pgc-img"><img src="https://img.example.com/photo.jpg" alt=""><p class="pgc-img-caption">▲ 苦味气泡水；图片来源：品牌官网</p></div><p>正文。</p><p>本文为FBIF食品饮料创新原创，转载请联系授权。</p>
```

### 正文不带链接

链接只留字，裸网址删掉。

原文：

- 段落：详见品牌官网（链接 https://example.com/news），或访问https://example.com/news。

写成：

```html
<p>详见品牌官网，或访问。</p><p>本文为FBIF食品饮料创新原创，转载请联系授权。</p>
```

### 去掉作者名片

作者名片（参考来源上方或之后的无图注图；没有参考来源时，文末无图注的宽条图）不放：平台会提示「图片含二维码」并限制推荐。

原文：

- 段落：正文最后一段。
- 图片：作者名片（宽条图，无图注）

写成：

```html
<p>正文最后一段。</p><p>本文为FBIF食品饮料创新原创，转载请联系授权。</p>
```

## 标准答案

编辑在头条号手发的稿是标准答案。样稿考卷 `api/testdata/distribution-golden/` 收了 #286 #287 #289 #290 #292 #299，每篇用现在的规则走一遍真实外发组装，和编辑稿还差哪几处记在 `<编号>.toutiao.txt`，差别一变 `TestDistributionGolden` 就失败。
