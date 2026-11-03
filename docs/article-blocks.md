# 文章正文块（格式说明书）

> 由 `api/article_blocks.go` 生成，别手改。改了块的定义跑 `cd api && SPEC_UPDATE=1 go test -run TestArticleBlockSpec .`。程序校验用同目录的 `article-blocks.schema.json`。

一篇文章抽出来是一串块，从上到下就是读者看到的顺序。公众号网址、供应商网页、FoodTalks 已发布稿都抽成这样的块，公众号、FoodTalks、头条号、百家号的排版都读它；浏览器里粘贴纯文本也产出同样的块。Word、Markdown 在浏览器里各自分类，不经块。外发第三方平台时先整理成外发通用版正文块（[distribution-blocks.md](distribution-blocks.md)），每种块在各平台写成什么标签见分发服务的 `docs/platforms/`。

## 块的种类

| k | 叫法 | 是什么 | 用到的字段 |
| --- | --- | --- | --- |
| `txt` | 段落 | 正文段落。所有字都加粗的段落是「整段加粗」，各渠道按加粗段写，不当小标题 | `runs` `text` `blankBefore` `compact` `tightBefore` |
| `h` | 小标题 | 文中的小标题，级别看 level。文章标题不在块里，在块外面的 title | `text` `level` `blankBefore` |
| `img` | 图片 | 一张图。紧跟在它后面的 cap 是这张图的说明 | `src` `dataW` `dataH` `dataRatio` `blankBefore` |
| `cap` | 图注 | 图片下面的说明文字，或「图片来源：」「图源：」这类出处行，紧跟在 img 后面，可以连着好几条。两种不另设字段，靠文字开头区分。前面没有图时按普通段落 | `text` `blankBefore` |
| `bq` | 引用 | 原文的引用块（blockquote） | `runs` `blankBefore` `tightBefore` |
| `li` | 列表项 | 列表里的一项，连着的几项是同一个列表。原文是有序还是无序列表不区分 | `runs` `blankBefore` `tightBefore` |
| `table` | 表格 | 一张表格，保留行列和表头格 | `rows` `blankBefore` |
| `video` | 视频 | 一段视频。拿不到视频地址时各渠道用封面图代替 | `src` `poster` `blankBefore` |

## 块的字段

| 字段 | 类型 | 意思 |
| --- | --- | --- |
| `k` | 文字 | 块的种类，见「块的种类」 |
| `level` | 整数 | 小标题级别。2 是标题1（一级），3 是标题2（二级，含「（一）」「1、」这类序号句），4 是更低一级；0 是原文没写标题标签、按字号颜色认出来的，由各渠道按一级处理或自己定级 |
| `text` | 文字 | 纯文字。小标题、图注只用它；段落一般用 runs，没有 runs 时才看它 |
| `runs` | 片段列表 | 行内片段，按顺序拼起来就是这一段；加粗、链接都在片段上 |
| `src` | 文字 | 图片或视频的原地址（公众号图床等），排版时换成转存后的地址 |
| `dataW` | 文字 | 图片原始宽度（像素，字符串），原文写了才有 |
| `dataH` | 文字 | 图片原始高度（像素，字符串），原文写了才有 |
| `dataRatio` | 文字 | 图片高宽比（字符串），原文写了才有 |
| `rows` | 行列表（每行是单元格列表） | 表格的行，每行是一串单元格 |
| `poster` | 文字 | 视频封面图地址 |
| `blankBefore` | 是/否 | 原文在这块前面有一个空段。公众号据此留出段距，FoodTalks 写成空段，头条号、百家号不管 |
| `compact` | 是/否 | 只在浏览器粘贴纯文本时出现：每行一段，段间不留公众号段距（除非 blankBefore）；整篇长段之间基本不空行（飞书、Word 复制）时每段都不排紧 |
| `tightBefore` | 是/否 | 只在抓网页原文（公众号、FoodTalks 已发布稿）时出现：这段和上一段都是文字，原文上紧挨着（上一段段后距、这段段前距写明是 0，中间没空段），像一列短句。FoodTalks 两段之间不垫空段，公众号、头条号、百家号不管 |

### 片段（runs 里的每一项）

| 字段 | 类型 | 意思 |
| --- | --- | --- |
| `type` | 文字 | txt 是普通文字，link 是链接 |
| `text` | 文字 | 这一片的文字 |
| `bold` | 是/否 | 加粗。一段里所有有字的片段都加粗，这段就是「整段加粗」 |
| `href` | 文字 | 链接地址，只有 link 才有 |

片段的 type 只有：`txt` `link`。

### 表格单元格（rows 里的每一格）

| 字段 | 类型 | 意思 |
| --- | --- | --- |
| `runs` | 片段列表 | 单元格里的文字片段 |
| `header` | 是/否 | 表头格（原文 th 或表头行） |

## 块外面的字段

抓取接口 `POST /api/fetch-article` 抓到正文时返回：

| 字段 | 类型 | 意思 |
| --- | --- | --- |
| `title` | 文字 | 文章标题 |
| `blocks` | 块列表 | 正文块，从上到下就是读者看到的顺序 |
| `source` | 文字 | 抓取的网址 |
| `source_name` | 文字 | 来源公众号或网站的名字 |
| `byline` | 文字 | 页面上写的作者（公众号作者栏）。只作参考，不补成正文署名 |
| `original_source_url` | 文字 | 公众号「阅读原文」的地址；转载稿的转载声明优先链到它 |
| `provenance` | 来源判定（见下） | 原创还是转载、转载自谁：和发布流程定 FoodTalks 来源类型同一个判定，网站开头署名和文末声明照它写 |

### 来源判定（provenance）

| 字段 | 类型 | 意思 |
| --- | --- | --- |
| `original` | 是/否 | 是不是自家原创 |
| `source` | 文字 | 转载自谁；原创或说不出原作者时不给 |

## 旧写法

2026-07 以前的稿子里块种类有旧写法，读的时候换成现在的，新代码不许再产出：

- `blockquote` 当 `bq`
- `image` 当 `img`
- `p` 当 `txt`

## 例子

开头一行署名、一段带局部加粗的话、一个小标题、一张图带两条图注（说明和图片来源）、一段整段加粗、一段带链接：

```json
[
  {
    "k": "txt",
    "runs": [
      {
        "type": "txt",
        "text": "作者：Sage"
      }
    ]
  },
  {
    "k": "txt",
    "runs": [
      {
        "type": "txt",
        "text": "今年的新品里，"
      },
      {
        "type": "txt",
        "text": "苦味",
        "bold": true
      },
      {
        "type": "txt",
        "text": "成了关键词。"
      }
    ]
  },
  {
    "k": "h",
    "level": 2,
    "text": "一、苦味升级",
    "blankBefore": true
  },
  {
    "k": "img",
    "src": "https://mmbiz.qpic.cn/example.jpg",
    "dataW": "1080",
    "dataH": "720",
    "dataRatio": "0.6667"
  },
  {
    "k": "cap",
    "text": "▲ 某品牌的苦味气泡水"
  },
  {
    "k": "cap",
    "text": "图片来源：某品牌官网"
  },
  {
    "k": "txt",
    "runs": [
      {
        "type": "txt",
        "text": "整段加粗的一句话。",
        "bold": true
      }
    ]
  },
  {
    "k": "txt",
    "runs": [
      {
        "type": "txt",
        "text": "详见"
      },
      {
        "type": "link",
        "text": "品牌官网",
        "href": "https://example.com"
      },
      {
        "type": "txt",
        "text": "。"
      }
    ]
  }
]
```
