# FBIF 排版接口 v1

> 这份说明由排版工具的代码生成（`api/layout_api_doc.go`），线上 `GET https://fbifmp-layout.foodtalks.cn/api/layout/v1/docs` 回的就是它，和代码永远一致。给程序用的 OpenAPI 在 `GET https://fbifmp-layout.foodtalks.cn/api/layout/v1/openapi.json`。两个地址都不用钥匙。

交一篇文章（公众号链接、普通网页链接、飞书云文档链接、Word、HTML、Markdown、纯文本）和一个账号，拿回按这个账号排好的成品 HTML，和网页版排版工具点「复制」得到的是同一套排版规则排出来的，能直接粘进公众号后台或 FoodTalks 后台。

接口**只排版**：不建发布单、不发到 FoodTalks、不发到任何平台、不存你的稿子。排好的结果在服务器内存里只留一会儿（见「限额」），拿到就自己存好。

## 一分钟上手

1. 找排版工具管理员要一把钥匙（`flk_` 开头的一长串），放进环境变量：`export FLK=flk_你的钥匙`。
2. 发一篇公众号文章，按「FoodTalks 默认」排：

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -H 'Content-Type: application/json' \
  --data-binary '{"account":"ft-default","url":"https://mp.weixin.qq.com/s/文章编号"}' \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

3. 回 **200** 就是排好了，成品在 `html`，标题在 `title`；回 **202** 是还在排，按回答里的 `operation.poll_url` 再取（见「还没排好（202）怎么取」）；回 4xx/5xx 看 `error.code` 和 `error.fix`（见「出错」）。

给 AI 助手用时，把这份说明整篇（或 openapi.json）交给它即可：每个错误都带 `fix`，原话转告给人就行。

## 接口一览

| 方法 | 路径 | 要钥匙 | 做什么 |
| --- | --- | --- | --- |
| POST | `/api/layout/v1/render` | 要 | 交一篇文章，拿回排好的 HTML（或 202 + poll_url） |
| GET | `/api/layout/v1/operations/{id}` | 要 | 取还没排好的那篇（202 里的 poll_url） |
| GET | `/api/layout/v1/docs` | 不要 | 这份说明书（Markdown），不用钥匙 |
| GET | `/api/layout/v1/openapi.json` | 不要 | OpenAPI 3.1 描述，给程序和 AI 助手生成调用代码，不用钥匙 |

每个回答都带请求头 `X-Request-ID`（和回答体里的 `request_id` 一样），找人排查时报它。

## 钥匙

- 请求头写 `Authorization: Bearer flk_…`，整串复制，前后不能有空格。
- 钥匙一家（一个系统或一位同事）一把，找排版工具管理员要。钥匙只在发的那一刻出现一次，丢了就重发一把，旧的停用。
- 钥匙只开 `/api/layout/v1/*`；排版工具旧的 `RENDER_TOKEN` 只开旧接口，两边不通用。
- 停用的钥匙回 403 `key_disabled`；排版工具暂时核对不了钥匙时回 503 `auth_unavailable`，等一会儿重发。
- 管理员发钥匙：`cd api && go run . -new-layout-key`，把打印出来的指纹填进 FBIF 中台「接口调用方」表新的一行（code、name、owner、status 填「在用」、key_sha256 填指纹），钥匙本身交给调用方。停用把 status 改成别的（比如「停用」），一分钟内生效；表里只有指纹，没有钥匙。

## 账号

`account` 只能填下表的 id（大小写一致）。同一篇稿子换账号，排出来的样式、署名、页脚都不一样。

| id | 账号 | 渠道 | 成品粘进哪里 |
| --- | --- | --- | --- |
| `fbif` | FBIF食品饮料创新 | 公众号 | 微信公众号后台编辑器 |
| `foodtalks-series` | FoodTalks什么是系列 | 公众号 | 微信公众号后台编辑器 |
| `wowfood` | WowFood爆品超市 | 公众号 | 微信公众号后台编辑器 |
| `ft-default` | FoodTalks默认 | FoodTalks | FoodTalks 后台编辑器 |
| `ft-series` | FoodTalks·什么是系列 | FoodTalks | FoodTalks 后台编辑器 |

## 能交什么

`url`、`html`、`markdown`、`text`、`file` 一次只给一个。服务器认出是哪种以后，按网页版上同样的操作排：

| 种类（`input.kind`） | 怎么交 | 等于网页上的 | 不填 `repost` 时按 | 说明 |
| --- | --- | --- | --- | --- |
| `wechat_url` 公众号文章链接 | `url` | 粘贴公众号链接 | 转载 | mp.weixin.qq.com/s 开头的文章链接 |
| `web_url` 普通网页链接 | `url` | 粘贴网页链接 | 转载 | 直接抓不到正文时改用阅读模式（返回里带提醒 reader_fallback） |
| `feishu_doc` 飞书云文档链接 | `url` | 粘贴飞书链接 | 原创 | docx、知识库文档链接；文档要先加「排版工具」应用，见「飞书文档」 |
| `word` Word（.docx、.doc） | `file` | 上传 Word | 原创 | multipart/form-data 上传；文档里的图片会转存到 FBIF 的 OSS |
| `html` 文章 HTML | `html` | （网页上没有这一项，按抓到的网页正文排） | 原创 | 图片写公开的 https 绝对地址 |
| `markdown` Markdown | `markdown` | 上传 .md 文件 | 原创 | 图片用 ![](https://…) 写公开的绝对地址 |
| `text` 纯文本 | `text` | 粘贴文字 | 原创 | 没有图片链接时只排正文、不加页脚；有图片链接时按 Markdown 排 |

`repost`：转载稿和原创稿的署名、页脚不一样。拿不准就不填，按网页上同样操作的默认排；返回的 `repost` 写着实际按哪种排的。

## 请求

`POST https://fbifmp-layout.foodtalks.cn/api/layout/v1/render`

链接、HTML、Markdown、文字用 `Content-Type: application/json`，请求体字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `account` | 文字 | 必填 | 按哪个账号排，必填。只能是「账号」表里的 id |
| `url` | 文字 | 四选一 | 公众号文章、普通网页或飞书云文档的链接，服务器自己认是哪种 |
| `html` | 文字 | 四选一 | 文章 HTML，整页或正文片段都行。图片要写公开的 https 绝对地址 |
| `markdown` | 文字 | 四选一 | Markdown 正文，按网页上传 .md 文件的规则排 |
| `text` | 文字 | 四选一 | 纯文本，按网页上粘贴文字的规则排 |
| `title` | 文字 | 可不填 | 标题。不填就用原文的（网页标题、Word 文件名、飞书文档名） |
| `repost` | true/false | 可不填 | true 按转载排，false 按原创排；不填按网页上同样操作的默认（见「能交什么」）。转载和原创的署名、页脚不一样 |
| `wait_seconds` | 整数 | 可不填 | POST 最多等几秒，0 到 100，默认 90。到点没排完回 202，再按 poll_url 取 |

Word 用 `multipart/form-data`：文件放在 `file` 字段，其余字段（`account`、`title`、`repost` 写 `true`/`false`、`wait_seconds`）作为同名表单字段，不收 `url`、`html`、`markdown`、`text`。

没写进上表的字段一律 400 `bad_request`（防止拼错字段名却以为生效了）。

## 每种输入怎么交

先 `export FLK=flk_你的钥匙`。下面每段都能直接跑（`jq` 用来把文件内容安全地放进 JSON；没有 jq 就用后面的 Python）。客户端超时一律设 130 秒（`--max-time 130`）：服务器最多等 100 秒就会回答，多留的是网络余量。

### 公众号文章链接（`wechat_url`）

mp.weixin.qq.com/s 开头的文章链接。

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -H 'Content-Type: application/json' \
  --data-binary '{"account":"ft-default","url":"https://mp.weixin.qq.com/s/文章编号"}' \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

### 普通网页链接（`web_url`）

直接抓不到正文时改用阅读模式（返回里带提醒 reader_fallback）。

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -H 'Content-Type: application/json' \
  --data-binary '{"account":"ft-default","url":"https://www.example.com/news/123.html","repost":true}' \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

### 飞书云文档链接（`feishu_doc`）

docx、知识库文档链接；文档要先加「排版工具」应用，见「飞书文档」。

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -H 'Content-Type: application/json' \
  --data-binary '{"account":"fbif","url":"https://xxx.feishu.cn/docx/文档编号"}' \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

### Word（.docx、.doc）（`word`）

multipart/form-data 上传；文档里的图片会转存到 FBIF 的 OSS。

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -F account=fbif -F 'file=@稿子.docx' \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

### 文章 HTML（`html`）

图片写公开的 https 绝对地址。

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -H 'Content-Type: application/json' \
  --data-binary "$(jq -n --rawfile html 正文.html '{account:"wowfood", title:"标题", html:$html}')" \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

### Markdown（`markdown`）

图片用 ![](https://…) 写公开的绝对地址。

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -H 'Content-Type: application/json' \
  --data-binary "$(jq -n --rawfile md 稿子.md '{account:"ft-series", markdown:$md}')" \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

### 纯文本（`text`）

没有图片链接时只排正文、不加页脚；有图片链接时按 Markdown 排。

```bash
curl -sS --max-time 130 \
  -H "Authorization: Bearer $FLK" \
  -H 'Content-Type: application/json' \
  --data-binary "$(jq -n --rawfile text 稿子.txt '{account:"foodtalks-series", title:"标题", text:$text}')" \
  https://fbifmp-layout.foodtalks.cn/api/layout/v1/render
```

### Python（所有输入通用，带轮询和重提）

需要 `pip install requests`。

```python
import os, time, requests

BASE = "https://fbifmp-layout.foodtalks.cn"
KEY = os.environ["FLK"]  # flk_ 开头的钥匙
HEADERS = {"Authorization": "Bearer " + KEY}
LOST_RETRY_WAITS = [10, 30, 60]  # operation_lost：最多重提 3 次


def _submit(payload, file_path=None):
    if file_path:  # Word 用表单上传
        with open(file_path, "rb") as f:
            data = {k: ("true" if v is True else "false" if v is False else str(v)) for k, v in payload.items()}
            return requests.post(BASE + "/api/layout/v1/render", headers=HEADERS, data=data,
                                 files={"file": (os.path.basename(file_path), f)}, timeout=130)
    return requests.post(BASE + "/api/layout/v1/render", headers=HEADERS, json=payload, timeout=130)


def _wait(resp):
    # 202 就按 poll_url 长等，直到 200 或出错。
    while resp.status_code == 202:
        poll_url = resp.json()["operation"]["poll_url"]
        resp = requests.get(BASE + poll_url, headers=HEADERS, params={"wait_seconds": 25}, timeout=40)
    return resp


def layout(payload, file_path=None):
    lost = 0
    while True:
        resp = _wait(_submit(payload, file_path))
        body = resp.json()
        if resp.status_code == 200:
            return body  # body["html"] 就是成品
        error = body["error"]
        if error["code"] == "operation_lost" and lost < len(LOST_RETRY_WAITS):
            time.sleep(LOST_RETRY_WAITS[lost])
            lost += 1
            continue
        if error.get("retryable") and error["code"] != "operation_lost":
            time.sleep(error.get("retry_after_seconds") or 30)
            continue  # 真实使用时请给这类重发也加上次数上限
        raise RuntimeError(f"{error['code']}：{error['message']}。{error['fix']}（request_id={body['request_id']}）")


# 公众号文章链接
# result = layout({"account": "ft-default", "url": "https://mp.weixin.qq.com/s/文章编号"})
# 普通网页链接
# result = layout({"account": "ft-default", "url": "https://www.example.com/news/123.html", "repost": True})
# 飞书云文档链接
# result = layout({"account": "fbif", "url": "https://xxx.feishu.cn/docx/文档编号"})
# Word（.docx、.doc）
# result = layout({"account": "fbif"}, file_path="稿子.docx")
# 文章 HTML
# result = layout({"account": "wowfood", "title": "标题", "html": open("正文.html", encoding="utf-8").read()})
# Markdown
# result = layout({"account": "ft-series", "markdown": open("稿子.md", encoding="utf-8").read()})
# 纯文本
# result = layout({"account": "foodtalks-series", "title": "标题", "text": open("稿子.txt", encoding="utf-8").read()})
```

## 回答

### 排好了（200）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `account` | 对象 | 按哪个账号排的 |
| `account.id` | 文字 | 账号 id |
| `account.name` | 文字 | 账号名 |
| `account.paste_into` | 文字 | 成品粘进哪个后台 |
| `title` | 文字 | 文章标题。html 里不含标题，贴进后台时单独填 |
| `summary` | 文字 | 摘要。FoodTalks 账号从原文开头的短导语取（取到时这段导语已从 html 里拿掉，和网页版一样）；没有就是空串 |
| `html` | 文字 | 成品：正文加页脚，样式全部写在标签上。粘进 account.paste_into 说的后台 |
| `repost` | true/false | 实际按转载（true）还是原创（false）排的 |
| `footer` | true/false | html 里有没有页脚。纯文本里没有图片链接时只排正文，没有页脚 |
| `input` | 对象 | 服务器认出来的输入 |
| `input.kind` | 文字 | 认出来的输入种类：wechat_url、web_url、feishu_doc、word、html、markdown、text 之一 |
| `input.url` | 文字 | 交的是链接时，那个链接 |
| `input.file_name` | 文字 | 交的是 Word 时的文件名 |
| `input.source_name` | 文字 | 抓到的来源：公众号名，或网页上写的来源 |
| `images` | 对象数组 | html 里的每张图（去重，按出现顺序）和它在哪个图床 |
| `images[].src` | 文字 | html 里这张图的地址 |
| `images[].host` | 文字 | wechat：微信图床，只有公众号账号会留；fbif：FBIF 自己的图床（OSS、FoodTalks 图床 static.foodtalks.cn） |
| `stats` | 对象 | 数量 |
| `stats.images` | 整数 | 正文图片数（不含页脚） |
| `stats.images_mirrored` | 整数 | 这次转存到 OSS 的图片数（含 Word、飞书文档里的图和页脚图） |
| `stats.headings` | 整数 | 小标题数 |
| `stats.html_bytes` | 整数 | html 的字节数 |
| `rule` | 对象 | 用的哪一版排版规则：总是提交那一刻生效的版本 |
| `rule.preset_id` | 文字 | 规则编号 |
| `rule.revision` | 整数 | 规则版本号 |
| `rule.config_hash` | 文字 | 规则内容的指纹 |
| `build` | 文字 | 排版程序的版本（git 提交号） |
| `output_hash` | 文字 | html 的 sha256（sha256: 开头）。两次结果一样，这个值就一样 |
| `warnings` | 对象数组 | 排好了但要留意的事，没有就是空数组。种类见「提醒」 |
| `warnings[].code` | 文字 | 提醒种类 |
| `warnings[].message` | 文字 | 给人看的说明 |
| `request_id` | 文字 | 提交这篇时那次请求的编号，找人排查时报这个 |

- 同一个请求先回 202、再用 `poll_url` 取到的 200，和直接回的 200 逐字节一样。
- `html` 不含标题；贴进后台时标题单独填 `title`。
- `output_hash` 只看 `html`：两次排出来一模一样，它就一样。

### 还在排（202）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `operation` | 对象 | 这篇在排的稿子 |
| `operation.id` | 文字 | 排版编号，op_ 开头 |
| `operation.state` | 文字 | queued：排队等排版；running：正在抓取、转换、排版或转存图片 |
| `operation.poll_url` | 文字 | 用同一把钥匙 GET 这个地址取结果，可带 ?wait_seconds=（最多 25） |
| `operation.retry_after_seconds` | 整数 | 建议隔几秒再取 |
| `request_id` | 文字 | 这次请求的编号 |

202 同时带请求头 `Location`（就是 `poll_url`）和 `Retry-After`。

```json
{
  "operation": {
    "id": "op_0a1b2c3d4e5fAbCdEfGhIjKlMnOpQrStUv",
    "state": "running",
    "poll_url": "/api/layout/v1/operations/op_0a1b2c3d4e5fAbCdEfGhIjKlMnOpQrStUv",
    "retry_after_seconds": 2
  },
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

### 出错（4xx、5xx）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `error` | 对象 | 出了什么错 |
| `error.code` | 文字 | 错误种类，程序按它分支，见「出错」 |
| `error.message` | 文字 | 出了什么事，给人看 |
| `error.fix` | 文字 | 该怎么办，给人看；AI 助手原话转告即可 |
| `error.retryable` | true/false | 原样重发有没有可能成功 |
| `error.retry_after_seconds` | 整数 | 建议等几秒再发；没有建议时不出现 |
| `request_id` | 文字 | 这次请求的编号，找人排查时报这个 |

能重发的错误同时带请求头 `Retry-After`。

```json
{
  "error": {
    "code": "feishu_not_shared",
    "message": "排版工具读不到这篇飞书文档：文档没加「排版工具」应用，或文档已删除、链接不对",
    "fix": "打开飞书文档，右上角「…」→「更多」→「添加文档应用」，搜「排版工具」加上（知识库文档也可以把应用加成知识库成员），再重发",
    "retryable": false
  },
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

## 还没排好（202）怎么取

POST 默认最多等 90 秒（`wait_seconds` 可填 0 到 100）。飞书长文档、图多的 Word 可能更久，这时先回 202：

1. 记下 `operation.poll_url`。
2. 用**同一把钥匙** `GET https://fbifmp-layout.foodtalks.cn<poll_url>?wait_seconds=25`：服务器最多陪你等 25 秒，排好了立刻回。
3. 还是 202 就再取一次（不用自己 sleep，`wait_seconds` 已经在等了）；回 200 或错误就结束。
4. 别家钥匙取不到你的稿子（回 404 `operation_not_found`）；排好的结果留 15 分钟，过期回 404 `operation_expired`。

```bash
curl -sS --max-time 40 -H "Authorization: Bearer $FLK" \
  "https://fbifmp-layout.foodtalks.cn/api/layout/v1/operations/op_…?wait_seconds=25"
```

一篇从提交起最长排 5 分钟，超了回 504 `timeout`。轮询本身也计次（见「限额」），请带 `wait_seconds` 长等，不要连着发。

## 出错

程序按 `error.code` 分支；`error.fix` 写给人看，AI 助手原话转告即可。`retryable` 为 true 的，等 `retry_after_seconds` 秒（没有就等 30 秒）原样重发。

| code | HTTP | 能重发 | 什么意思 | 怎么办 |
| --- | --- | --- | --- | --- |
| `unauthorized` | 401 | 否 | 没带钥匙，或钥匙不对 | 检查请求头 Authorization: Bearer flk_…，钥匙要整串复制，前后不能有空格。没有钥匙找排版工具管理员要一把 |
| `key_disabled` | 403 | 否 | 这把钥匙已经停用 | 找排版工具管理员问清楚，需要的话再发一把新的 |
| `auth_unavailable` | 503 | 能，等 30 秒 | 排版工具暂时核对不了钥匙（读不到 FBIF 中台的调用方名单） | 等 retry_after_seconds 秒再发；超过 10 分钟还这样，把 request_id 发给排版工具维护者 |
| `service_restarting` | 503 | 能，等 30 秒 | 排版工具正在更新上线，这段时间不接新稿 | 等 retry_after_seconds 秒再发，一般一两分钟就好 |
| `bad_request` | 400 | 否 | 请求读不懂 | 照说明书「请求」一节改：JSON 用 Content-Type: application/json，Word 用 multipart/form-data；message 里写了具体哪一项不对 |
| `request_too_large` | 413 | 否 | 请求体太大 | JSON 请求体不能超过说明书「限额」里的大小；正文太长就拆成几篇，或改用 Word 上传 |
| `unknown_account` | 400 | 否 | account 不认识 | account 只能是说明书「账号」表里的 id，大小写要一致 |
| `too_many_inputs` | 400 | 否 | url、html、markdown、text、file 只能给一个 | 一次只交一样内容；同一篇有链接就只给 url |
| `invalid_url` | 400 | 否 | url 不是能公开打开的 http(s) 链接 | 换成浏览器里能直接打开的公开链接；内网地址、带账号密码的链接都不收 |
| `unsupported_file` | 415 | 否 | 文件不是 Word（.docx 或 .doc） | 用 Word 打开后另存为 .docx 再传；PDF、WPS、表格、PPT 都不收 |
| `file_too_large` | 413 | 否 | 文件太大 | 上传的 Word 不能超过说明书「限额」里的大小；去掉图片后还太大，多半是文档里嵌了附件、视频或别的对象，删掉嵌入的附件、对象后重传 |
| `rate_limited` | 429 | 能 | 发得太快，超过了每分钟的次数 | 等 retry_after_seconds 秒再发；批量排版请排队一篇一篇发 |
| `busy` | 503 | 能，等 5 秒 | 同时在排的稿子太多 | 等手上的稿子排完（或等 retry_after_seconds 秒）再发 |
| `operation_not_found` | 404 | 否 | 找不到这个排版编号 | 确认编号是这把钥匙提交时拿到的；拿不准就重新提交 |
| `operation_lost` | 404 | 能，等 10 秒 | 排版工具重启过，这篇在排的稿子丢了 | 重新提交同一个请求。最多重提 3 次，依次等 10 秒、30 秒、60 秒；3 次都这样就把 request_id 发给排版工具维护者 |
| `operation_expired` | 404 | 能 | 排好的结果已经过期清掉了 | 重新提交同一个请求。排好的结果只留一段时间（见「限额」），拿到 200 后请自己存好 |
| `fetch_failed` | 502 | 能，等 30 秒 | 原文暂时抓不到（网络不通，或公众号临时限流） | 等 retry_after_seconds 秒重发；一直不行就在浏览器打开原文，把正文用 html 或 text 交过来 |
| `wechat_verify_page` | 502 | 能，等 60 秒 | 公众号没给正文，回的是「环境异常」验证页（也可能文章已被删除） | 过几分钟再发；一直这样就在浏览器打开原文确认还在，再把正文用 html 或 text 交过来 |
| `page_has_no_body` | 422 | 否 | 这个网页没给出正文，可能开了反爬验证 | 在浏览器打开原文，把正文复制出来用 html 或 text 交；或者贴进飞书云文档，用飞书链接交 |
| `page_too_large` | 422 | 否 | 原网页太大，抓不下来 | 在浏览器打开原文，把正文复制出来用 html 或 text 交 |
| `feishu_not_shared` | 403 | 否 | 排版工具读不到这篇飞书文档：文档没加「排版工具」应用，或文档已删除、链接不对 | 打开飞书文档，右上角「…」→「更多」→「添加文档应用」，搜「排版工具」加上（知识库文档也可以把应用加成知识库成员），再重发 |
| `feishu_not_document` | 400 | 否 | 这个飞书链接不是云文档（可能是表格、多维表格或目录） | 只收飞书云文档（docx、知识库里的文档）；表格请先整理成文档 |
| `feishu_too_large` | 413 | 否 | 文档或图片太多，飞书导不出来 | 把文档拆成几篇，或删掉一些图片后重发 |
| `feishu_unavailable` | 502 | 能，等 30 秒 | 飞书导出失败或超时 | 等 retry_after_seconds 秒重发；一直不行把 request_id 发给排版工具维护者 |
| `doc_convert_unavailable` | 503 | 否 | .doc 转换服务没开 | 用 Word 打开后另存为 .docx 再传 |
| `doc_convert_failed` | 422 | 否 | .doc 转换失败 | 用 Word 打开后另存为 .docx 再传 |
| `docx_images_unreadable` | 422 | 否 | Word 里的图片拆不出来（文件结构异常，或图片太大、太多） | 用 Word 打开后另存为 .docx 再传；单张图不要超过 15 MB、一篇不要超过 100 张 |
| `word_unreadable` | 422 | 否 | Word 文件读不出来 | 用 Word 打开后另存为 .docx 再传 |
| `empty_article` | 422 | 否 | 交过来的内容里没有正文 | 检查交的内容：html 要有正文段落，markdown、text 不能只有空行 |
| `image_mirror_failed` | 502 | 能，等 30 秒 | 有图片转存失败 | 等 retry_after_seconds 秒重发；一直失败把 message 里的图片地址和 request_id 发给排版工具维护者 |
| `image_not_allowed` | 422 | 否 | 排好的稿子里有不能直接用的图片地址 | html、markdown 里的图片要写成公开的 https 绝对地址；blob:、data:、相对地址都不收。message 里写了是哪张 |
| `rules_unavailable` | 503 | 能，等 30 秒 | 读不到这个账号当前生效的排版规则 | 等 retry_after_seconds 秒重发 |
| `render_unavailable` | 503 | 能，等 30 秒 | 排版程序没起来 | 等 retry_after_seconds 秒重发；一直这样把 request_id 发给排版工具维护者 |
| `render_failed` | 500 | 否 | 排版时出错 | 把 request_id 发给排版工具维护者 |
| `timeout` | 504 | 能，等 30 秒 | 排版超过了时限还没完 | 重发一次；飞书长文档、图多的稿子本来就慢，带 wait_seconds 并按 poll_url 取结果 |

### 排版工具重启了（`operation_lost`）

排版中的稿子只在服务器内存里，排版工具更新上线会重启，正在排的和排好没取走的都会丢，拿着旧的 `poll_url` 去取回 404 `operation_lost`。这时**原样重新提交一次**；重提最多 3 次，依次等 10 秒、30 秒、60 秒；3 次都不行把 `request_id` 发给排版工具维护者。上线那一两分钟里新提交会回 503 `service_restarting`，同样等一会儿重发。

## 图片

`images` 列出 `html` 里的每一张图和它在哪个图床：

- `fbif`：FBIF 自己的图床（OSS，和 FoodTalks 图床 `static.foodtalks.cn`），长期有效，公众号后台和 FoodTalks 后台都能用。
- `wechat`：微信图床（`mmbiz.qpic.cn`、`mmbiz.qlogo.cn`）。只有公众号账号会留，粘进公众号后台编辑器微信自己认；FoodTalks 账号一律转成 `fbif`。

各渠道怎么处理：

- 公众号账号：微信图床（mmbiz）和 FBIF 自己图床的图原样留，其余转存到 FBIF 的 OSS。粘进公众号后台编辑器时微信会把这些图转进自己的图床；要是走微信草稿接口（draft/add）存稿，先用 uploadimg 把 host 是 fbif 的图换成微信图床地址，微信会过滤外部图片。
- FoodTalks 账号：FBIF 自己图床的图原样留，其余全部转存到 FBIF 的 OSS，微信图床的图也转：微信图床有防盗链，在 FoodTalks 页面上显示不出来。

交 HTML、Markdown 时图片写公开的 https 绝对地址，服务器会转存到 OSS。Markdown 里 png、jpeg、gif、webp 的 `data:` 内嵌图也会转存（加起来不超过 16 MB）；HTML 里的 `data:` 图和网页版一样直接略过（公众号页面里的 `data:` 多是占位图）。`blob:`、SVG 内嵌图、相对地址、内网地址都不收，整篇回 422 `image_not_allowed`，message 里写着是哪张。Word 和飞书文档里的图片服务器自己拆出来转存，不用管。转存失败回 502 `image_mirror_failed`，可以重发。

## 飞书文档

排版工具用自己的飞书应用「排版工具」去读文档，不借任何人的授权，所以**文档要先把这个应用加进来**，否则回 403 `feishu_not_shared`：

1. 在飞书里打开文档，点右上角「…」→「更多」→「添加文档应用」。
2. 搜「排版工具」，加上（只要能阅读就够）。知识库里的文档也可以把应用加成知识库成员，整个知识库一次加好。
3. 用文档链接（`https://….feishu.cn/docx/…` 或 `/wiki/…`）交 `url`。

表格、多维表格、目录不是文档，回 400 `feishu_not_document`。文档里的图片会转存到 OSS。飞书导出比较慢，长文档常常先回 202。

## 提醒

排好了但要留意的事放在 `warnings`，不影响用：

| code | 意思 |
| --- | --- |
| `reader_fallback` | 这个网页直接抓不到正文，改用阅读模式取回的 Markdown 排的：小标题、加粗、图注可能不如直接抓准，请过目 |
| `render_note` | 排版程序排的时候留意到的事，message 里写了具体是什么 |

## 限额

| 项目 | 上限 | 说明 |
| --- | --- | --- |
| 每把钥匙同时在排 | 4 | 从提交算到排完（拿到 202 之后后台还在排的也算），超了回 503 busy |
| 每把钥匙每分钟提交 | 30 | 超了回 429 rate_limited，retry_after_seconds 写着还要等几秒 |
| 每把钥匙每分钟轮询 | 120 | GET poll_url 的次数，超了回 429 rate_limited |
| 全部调用方合计每分钟抓公众号 | 20 | 保护服务器出口，公众号限流会波及编辑；超了回 429 rate_limited |
| POST 最多等 | 100 秒 | wait_seconds 默认 90，可填 0 到 100；到点没排完回 202 |
| 轮询一次最多等 | 25 秒 | GET poll_url 可带 wait_seconds（0 到 25，默认 25） |
| 一篇最长排 | 5 分钟 | 超了回 504 timeout |
| 排好的结果保留 | 15 分钟 | 过期回 404 operation_expired；服务器内存紧张时最早排好的会先清掉 |
| JSON 请求体 | 8 MB | 超了回 413 request_too_large |
| 上传的 Word | 50 MB | 超了回 413 file_too_large；上传本身最多可以传 3 分钟 |
| Word 去掉图片后 | 16 MB | 图片单独转存不算在内；超了多半是嵌了附件或对象，回 413 file_too_large |
| 标题 | 200 字 | 超了回 400 bad_request |
| 建议的客户端超时 | 130 秒 | 比 wait_seconds 上限多留余量，curl 用 --max-time 130 |

批量排版请一篇排完再发下一篇，或最多 4 篇并发；不要对同一篇反复提交。

## 和网页版不一样的地方

- **不出「更多文章」**：网页版公众号稿文末的「更多文章」卡片要编辑挑往期文章、裁封面、把标题画进封面图，还要求卡片图在微信图床里（OSS 图存进公众号草稿会被微信过滤），这些接口替不了人；空着标题出卡片反而难看。需要的话把成品粘进公众号后台后，在网页版排版工具或后台里手动加。
- **图片已经转存好**：网页版在你点复制那一刻才转存图片；接口在回答之前就转存完，html 里每张图都是能长期用的地址（见「图片」）。
- **规则总是提交那一刻生效的版本**：不能指定旧版本的排版规则；返回里的 rule 记着用的是哪一版，规则改了再排，结果可能不一样。

## 回答长什么样

下面的例子还是占位（字段齐全、内容是假的），排版程序上线后会换成真实排过的回答。

### 公众号链接排成 FoodTalks 稿

```json
{
  "account": {
    "id": "ft-default",
    "name": "FoodTalks默认",
    "paste_into": "FoodTalks 后台编辑器"
  },
  "title": "（占位）例子标题",
  "summary": "",
  "html": "<section><p>（占位：排版程序上线后用真回答重新生成，见 api/layout_api_doc_test.go 的 TestLayoutAPIExamplesRegenerate）</p></section>",
  "repost": true,
  "footer": true,
  "input": {
    "kind": "wechat_url",
    "url": "https://mp.weixin.qq.com/s/文章编号",
    "source_name": "来源公众号"
  },
  "images": [],
  "stats": {
    "images": 0,
    "images_mirrored": 0,
    "headings": 0,
    "html_bytes": 155
  },
  "rule": {
    "preset_id": "（占位）",
    "revision": 0,
    "config_hash": "（占位）"
  },
  "build": "（占位）",
  "output_hash": "sha256:8f7a23b6259e4df25274bbeea6179393665f22fdd1cb68e4aea5edb008b72754",
  "warnings": [],
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

### Word 排成「FBIF食品饮料创新」公众号稿

```json
{
  "account": {
    "id": "fbif",
    "name": "FBIF食品饮料创新",
    "paste_into": "微信公众号后台编辑器"
  },
  "title": "（占位）例子标题",
  "summary": "",
  "html": "<section><p>（占位：排版程序上线后用真回答重新生成，见 api/layout_api_doc_test.go 的 TestLayoutAPIExamplesRegenerate）</p></section>",
  "repost": false,
  "footer": true,
  "input": {
    "kind": "word",
    "file_name": "稿子.docx"
  },
  "images": [],
  "stats": {
    "images": 0,
    "images_mirrored": 0,
    "headings": 0,
    "html_bytes": 155
  },
  "rule": {
    "preset_id": "（占位）",
    "revision": 0,
    "config_hash": "（占位）"
  },
  "build": "（占位）",
  "output_hash": "sha256:8f7a23b6259e4df25274bbeea6179393665f22fdd1cb68e4aea5edb008b72754",
  "warnings": [],
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

### Markdown 排成 FoodTalks「什么是系列」稿

```json
{
  "account": {
    "id": "ft-series",
    "name": "FoodTalks·什么是系列",
    "paste_into": "FoodTalks 后台编辑器"
  },
  "title": "（占位）例子标题",
  "summary": "",
  "html": "<section><p>（占位：排版程序上线后用真回答重新生成，见 api/layout_api_doc_test.go 的 TestLayoutAPIExamplesRegenerate）</p></section>",
  "repost": false,
  "footer": true,
  "input": {
    "kind": "markdown"
  },
  "images": [],
  "stats": {
    "images": 0,
    "images_mirrored": 0,
    "headings": 0,
    "html_bytes": 155
  },
  "rule": {
    "preset_id": "（占位）",
    "revision": 0,
    "config_hash": "（占位）"
  },
  "build": "（占位）",
  "output_hash": "sha256:8f7a23b6259e4df25274bbeea6179393665f22fdd1cb68e4aea5edb008b72754",
  "warnings": [],
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

### 纯文本排成「FoodTalks什么是系列」公众号稿

```json
{
  "account": {
    "id": "foodtalks-series",
    "name": "FoodTalks什么是系列",
    "paste_into": "微信公众号后台编辑器"
  },
  "title": "（占位）例子标题",
  "summary": "",
  "html": "<section><p>（占位：排版程序上线后用真回答重新生成，见 api/layout_api_doc_test.go 的 TestLayoutAPIExamplesRegenerate）</p></section>",
  "repost": false,
  "footer": true,
  "input": {
    "kind": "text"
  },
  "images": [],
  "stats": {
    "images": 0,
    "images_mirrored": 0,
    "headings": 0,
    "html_bytes": 155
  },
  "rule": {
    "preset_id": "（占位）",
    "revision": 0,
    "config_hash": "（占位）"
  },
  "build": "（占位）",
  "output_hash": "sha256:8f7a23b6259e4df25274bbeea6179393665f22fdd1cb68e4aea5edb008b72754",
  "warnings": [],
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

### HTML 排成「WowFood爆品超市」公众号稿

```json
{
  "account": {
    "id": "wowfood",
    "name": "WowFood爆品超市",
    "paste_into": "微信公众号后台编辑器"
  },
  "title": "（占位）例子标题",
  "summary": "",
  "html": "<section><p>（占位：排版程序上线后用真回答重新生成，见 api/layout_api_doc_test.go 的 TestLayoutAPIExamplesRegenerate）</p></section>",
  "repost": false,
  "footer": true,
  "input": {
    "kind": "html"
  },
  "images": [],
  "stats": {
    "images": 0,
    "images_mirrored": 0,
    "headings": 0,
    "html_bytes": 155
  },
  "rule": {
    "preset_id": "（占位）",
    "revision": 0,
    "config_hash": "（占位）"
  },
  "build": "（占位）",
  "output_hash": "sha256:8f7a23b6259e4df25274bbeea6179393665f22fdd1cb68e4aea5edb008b72754",
  "warnings": [],
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

### 飞书文档先回 202

```json
{
  "operation": {
    "id": "op_0a1b2c3d4e5fAbCdEfGhIjKlMnOpQrStUv",
    "state": "running",
    "poll_url": "/api/layout/v1/operations/op_0a1b2c3d4e5fAbCdEfGhIjKlMnOpQrStUv",
    "retry_after_seconds": 2
  },
  "request_id": "lr_Q2xhdWRlRXhh"
}
```

## 版本

路径里的 `v1` 不变时，只会加字段、加错误码、加提醒，不会删字段或改字段意思；请按字段名取值、忽略不认识的字段，遇到不认识的 `error.code` 按 `retryable` 处理。排版规则随时会被编辑改进，`rule`、`build` 记着这一篇用的是哪一版。
