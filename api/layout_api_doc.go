package main

// layout_api_doc.go — 排版接口的对外说明书（Markdown）和 OpenAPI，全部从代码里的表生成。
//
// 路由（layoutAPIRoutes）、字段（线上类型的 doc 标签）、输入（layoutInputSpecs）、账号（accountCatalog + layoutChannelSpecs）、
// 错误（layoutErrorSpecs）、提醒（layoutWarningSpecs）、限额（layoutLimits）都只写一处，这里只负责拼。
// GET /api/layout/v1/docs 回的就是 renderLayoutAPIDoc()；docs/layout-api.md 是同一份的存档，
// TestLayoutAPIDocIsGenerated 比对，改了代码跑 cd api && SPEC_UPDATE=1 go test -run TestLayoutAPIDocIsGenerated . 重新生成。
// 例子回答在 testdata/layout-examples/*.json（编进程序里），怎么用真回答重新生成见 layout_api_doc_test.go。

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"sync"
	"time"
)

//go:embed testdata/layout-examples/*.json
var layoutExampleFiles embed.FS

const layoutExampleDir = "testdata/layout-examples"

// layoutExamplePlaceholder 占位例子回答里的标记；换成真回答后说明书自动去掉「还是占位」那句。
const layoutExamplePlaceholder = "（占位"

// layoutAPIExample 说明书「回答长什么样」里的一个真例子：哪种输入、哪个账号，回答存在 testdata/layout-examples/<File>.json。
type layoutAPIExample struct {
	File    string
	Kind    layoutInputKind
	Account string
	Title   string
}

var layoutAPIExamples = []layoutAPIExample{
	{"wechat-url-ft-default", layoutInputWechatURL, "ft-default", "公众号链接排成 FoodTalks 稿"},
	{"word-fbif", layoutInputWord, "fbif", "Word 排成「FBIF食品饮料创新」公众号稿"},
	{"markdown-ft-series", layoutInputMarkdown, "ft-series", "Markdown 排成 FoodTalks「什么是系列」稿"},
	{"text-foodtalks-series", layoutInputText, "foodtalks-series", "纯文本排成「FoodTalks什么是系列」公众号稿"},
	{"html-wowfood", layoutInputHTML, "wowfood", "HTML 排成「WowFood爆品超市」公众号稿"},
}

// layoutInputExample 每种输入一个能直接跑的请求（说明书「每种输入怎么交」）。
type layoutInputExample struct {
	Kind    layoutInputKind
	Account string
	Curl    string // 不含钥匙和地址的那部分由 renderLayoutCurl 补齐
	Python  string // layout(...) 的参数
}

var layoutInputExamples = []layoutInputExample{
	{layoutInputWechatURL, "ft-default",
		`--data-binary '{"account":"ft-default","url":"https://mp.weixin.qq.com/s/文章编号"}'`,
		`{"account": "ft-default", "url": "https://mp.weixin.qq.com/s/文章编号"}`},
	{layoutInputWebURL, "ft-default",
		`--data-binary '{"account":"ft-default","url":"https://www.example.com/news/123.html","repost":true}'`,
		`{"account": "ft-default", "url": "https://www.example.com/news/123.html", "repost": True}`},
	{layoutInputFeishuDoc, "fbif",
		`--data-binary '{"account":"fbif","url":"https://xxx.feishu.cn/docx/文档编号"}'`,
		`{"account": "fbif", "url": "https://xxx.feishu.cn/docx/文档编号"}`},
	{layoutInputWord, "fbif",
		`-F account=fbif -F 'file=@稿子.docx'`,
		`{"account": "fbif"}, file_path="稿子.docx"`},
	{layoutInputHTML, "wowfood",
		`--data-binary "$(jq -n --rawfile html 正文.html '{account:"wowfood", title:"标题", html:$html}')"`,
		`{"account": "wowfood", "title": "标题", "html": open("正文.html", encoding="utf-8").read()}`},
	{layoutInputMarkdown, "ft-series",
		`--data-binary "$(jq -n --rawfile md 稿子.md '{account:"ft-series", markdown:$md}')"`,
		`{"account": "ft-series", "markdown": open("稿子.md", encoding="utf-8").read()}`},
	{layoutInputText, "foodtalks-series",
		`--data-binary "$(jq -n --rawfile text 稿子.txt '{account:"foodtalks-series", title:"标题", text:$text}')"`,
		`{"account": "foodtalks-series", "title": "标题", "text": open("稿子.txt", encoding="utf-8").read()}`},
}

// layoutAPIDifferences 和网页版排出来的不一样的地方，每条写原因。
var layoutAPIDifferences = []struct{ What, Why string }{
	{"不出「更多文章」",
		"网页版公众号稿文末的「更多文章」卡片要编辑挑往期文章、裁封面、把标题画进封面图，还要求卡片图在微信图床里（OSS 图存进公众号草稿会被微信过滤），这些接口替不了人；空着标题出卡片反而难看。需要的话把成品粘进公众号后台后，在网页版排版工具或后台里手动加"},
	{"图片已经转存好",
		"网页版在你点复制那一刻才转存图片；接口在回答之前就转存完，html 里每张图都是能长期用的地址（见「图片」）"},
	{"规则总是提交那一刻生效的版本",
		"不能指定旧版本的排版规则；返回里的 rule 记着用的是哪一版，规则改了再排，结果可能不一样"},
}

// ---- 缓存 ----

var layoutAPIDocOnce = sync.OnceValues(renderLayoutAPIDoc)

// layoutAPIDocCached 说明书只在第一次用时生成一次（内容只取决于代码和编进去的例子）。
func layoutAPIDocCached() (string, error) { return layoutAPIDocOnce() }

var layoutAPIOpenAPIOnce = sync.OnceValue(renderLayoutAPIOpenAPI)

func layoutAPIOpenAPICached() string { return layoutAPIOpenAPIOnce() }

// ---- 字段表 ----

type layoutDocField struct {
	Path     string
	Type     string
	Optional bool
	Doc      string
}

func layoutJSONName(field reflect.StructField) (string, bool) {
	tag := field.Tag.Get("json")
	if tag == "" || tag == "-" {
		return "", false
	}
	name, opts, _ := strings.Cut(tag, ",")
	return name, strings.Contains(","+opts+",", ",omitempty,")
}

func layoutTypeLabel(typ reflect.Type) string {
	switch typ.Kind() {
	case reflect.Pointer:
		return layoutTypeLabel(typ.Elem())
	case reflect.String:
		return "文字"
	case reflect.Int:
		return "整数"
	case reflect.Bool:
		return "true/false"
	case reflect.Struct:
		return "对象"
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Struct {
			return "对象数组"
		}
		return layoutTypeLabel(typ.Elem()) + "数组"
	}
	return typ.String()
}

// layoutDocFields 按声明顺序列出一个线上类型的字段，嵌套的对象展开成 a.b、数组成 a[].b。
func layoutDocFields(typ reflect.Type, prefix string) []layoutDocField {
	var out []layoutDocField
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name, omitempty := layoutJSONName(field)
		if name == "" {
			continue
		}
		path := prefix + name
		ft := field.Type
		optional := omitempty || ft.Kind() == reflect.Pointer
		out = append(out, layoutDocField{Path: path, Type: layoutTypeLabel(ft), Optional: optional, Doc: field.Tag.Get("doc")})
		switch {
		case ft.Kind() == reflect.Struct:
			out = append(out, layoutDocFields(ft, path+".")...)
		case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
			out = append(out, layoutDocFields(ft.Elem(), path+"[].")...)
		}
	}
	return out
}

func writeLayoutFieldTable(b *strings.Builder, typ reflect.Type, request bool) {
	if request {
		b.WriteString("| 字段 | 类型 | 必填 | 说明 |\n| --- | --- | --- | --- |\n")
	} else {
		b.WriteString("| 字段 | 类型 | 说明 |\n| --- | --- | --- |\n")
	}
	for _, f := range layoutDocFields(typ, "") {
		doc := layoutDocCell(f.Doc)
		if request {
			must := "必填"
			if f.Optional {
				must = "可不填"
			}
			switch f.Path {
			case "url", "html", "markdown", "text":
				must = "四选一"
			}
			fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", f.Path, f.Type, must, doc)
			continue
		}
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", f.Path, f.Type, doc)
	}
}

func layoutDocCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

// ---- 说明书 ----

func layoutChannelLabel(channel string) string {
	switch channel {
	case "mp":
		return "公众号"
	case "ft":
		return "FoodTalks"
	}
	return channel
}

// layoutChannelAccounts「公众号账号」「FoodTalks 账号」：英文后面空一格。
func layoutChannelAccounts(channel string) string {
	label := layoutChannelLabel(channel)
	if label != "" && label[len(label)-1] < 0x80 {
		return label + " 账号"
	}
	return label + "账号"
}

func renderLayoutCurl(example layoutInputExample) string {
	var b strings.Builder
	b.WriteString("curl -sS --max-time 130 \\\n")
	b.WriteString("  -H \"Authorization: Bearer $FLK\" \\\n")
	if example.Kind != layoutInputWord {
		b.WriteString("  -H 'Content-Type: application/json' \\\n")
	}
	b.WriteString("  " + example.Curl + " \\\n")
	b.WriteString("  " + layoutAPIPublicBase + "/api/layout/v1/render\n")
	return b.String()
}

func layoutPrettyJSON(raw []byte) string {
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimSpace(raw), "", "  "); err != nil {
		return string(bytes.TrimSpace(raw))
	}
	return out.String()
}

// layoutExampleForDoc 读一个例子回答，html 截短，免得说明书被一整篇稿子占满。
func layoutExampleForDoc(example layoutAPIExample) (string, error) {
	raw, err := fs.ReadFile(layoutExampleFiles, layoutExampleDir+"/"+example.File+".json")
	if err != nil {
		return "", fmt.Errorf("缺例子回答 %s/%s.json：%w", layoutExampleDir, example.File, err)
	}
	var result layoutResultJSON
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return "", fmt.Errorf("例子回答 %s.json 和 200 的字段对不上：%w", example.File, err)
	}
	if result.Account.ID != example.Account || result.Input.Kind != string(example.Kind) {
		return "", fmt.Errorf("例子回答 %s.json 是 %s/%s，表里写的是 %s/%s", example.File,
			result.Account.ID, result.Input.Kind, example.Account, example.Kind)
	}
	const keep = 240
	if runes := []rune(result.HTML); len(runes) > keep {
		result.HTML = string(runes[:keep]) + fmt.Sprintf("…（共 %d 字节，这里只列开头）", result.Stats.HTMLBytes)
	}
	return layoutPrettyJSON(layoutMarshal(result)), nil
}

func layoutExamplePending() string {
	return layoutPrettyJSON(layoutMarshal(layoutPendingJSON{
		Operation: layoutOperationJSON{
			ID:                "op_0a1b2c3d4e5fAbCdEfGhIjKlMnOpQrStUv",
			State:             layoutStateRunning,
			PollURL:           layoutPollURL("op_0a1b2c3d4e5fAbCdEfGhIjKlMnOpQrStUv"),
			RetryAfterSeconds: int(layoutPendingRetryAfter / time.Second),
		},
		RequestID: "lr_Q2xhdWRlRXhh",
	}))
}

func layoutExampleError() string {
	return layoutPrettyJSON(layoutErrorResponse("lr_Q2xhdWRlRXhh", layoutErr(layoutErrFeishuNotShared)).Body)
}

func renderLayoutAPIDoc() (string, error) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	base := layoutAPIPublicBase

	w("# FBIF 排版接口 v1\n\n")
	w("> 这份说明由排版工具的代码生成（`api/layout_api_doc.go`），线上 `GET %s/api/layout/v1/docs` 回的就是它，和代码永远一致。"+
		"给程序用的 OpenAPI 在 `GET %s/api/layout/v1/openapi.json`。两个地址都不用钥匙。\n\n", base, base)
	w("交一篇文章（公众号链接、普通网页链接、飞书云文档链接、Word、HTML、Markdown、纯文本）和一个账号，" +
		"拿回按这个账号排好的成品 HTML，和网页版排版工具点「复制」得到的是同一套排版规则排出来的，能直接粘进公众号后台或 FoodTalks 后台。\n\n")
	w("接口**只排版**：不建发布单、不发到 FoodTalks、不发到任何平台、不存你的稿子。排好的结果在服务器内存里只留一会儿（见「限额」），拿到就自己存好。\n\n")

	w("## 一分钟上手\n\n")
	w("1. 找排版工具管理员要一把钥匙（`flk_` 开头的一长串），放进环境变量：`export FLK=flk_你的钥匙`。\n")
	w("2. 发一篇公众号文章，按「FoodTalks 默认」排：\n\n```bash\n%s```\n\n", renderLayoutCurl(layoutInputExamples[0]))
	w("3. 回 **200** 就是排好了，成品在 `html`，标题在 `title`；回 **202** 是还在排，按回答里的 `operation.poll_url` 再取（见「还没排好（202）怎么取」）；" +
		"回 4xx/5xx 看 `error.code` 和 `error.fix`（见「出错」）。\n\n")
	w("给 AI 助手用时，把这份说明整篇（或 openapi.json）交给它即可：每个错误都带 `fix`，原话转告给人就行。\n\n")

	w("## 接口一览\n\n| 方法 | 路径 | 要钥匙 | 做什么 |\n| --- | --- | --- | --- |\n")
	for _, route := range layoutAPIRoutes {
		auth := "要"
		if !route.Auth {
			auth = "不要"
		}
		w("| %s | `%s` | %s | %s |\n", route.Method, route.Path, auth, route.Summary)
	}
	w("\n每个回答都带请求头 `X-Request-ID`（和回答体里的 `request_id` 一样），找人排查时报它。\n\n")

	w("## 钥匙\n\n")
	w("- 请求头写 `Authorization: Bearer flk_…`，整串复制，前后不能有空格。\n")
	w("- 钥匙一家（一个系统或一位同事）一把，找排版工具管理员要。钥匙只在发的那一刻出现一次，丢了就重发一把，旧的停用。\n")
	w("- 钥匙只开 `/api/layout/v1/*`；排版工具旧的 `RENDER_TOKEN` 只开旧接口，两边不通用。\n")
	w("- 停用的钥匙回 403 `key_disabled`；排版工具暂时核对不了钥匙时回 503 `auth_unavailable`，等一会儿重发。\n")
	w("- 管理员发钥匙：`cd api && go run . -new-layout-key`，把打印出来的指纹填进 FBIF 中台「接口调用方」表新的一行（code、name、owner、status 填「在用」、key_sha256 填指纹），" +
		"钥匙本身交给调用方。停用把 status 改成别的（比如「停用」），一分钟内生效；表里只有指纹，没有钥匙。\n\n")

	w("## 账号\n\n`account` 只能填下表的 id（大小写一致）。同一篇稿子换账号，排出来的样式、署名、页脚都不一样。\n\n")
	w("| id | 账号 | 渠道 | 成品粘进哪里 |\n| --- | --- | --- | --- |\n")
	for _, id := range catalogOrderIDs() {
		meta, _ := accountMetaByID(id)
		channel, _ := layoutChannelFor(meta.Channel)
		w("| `%s` | %s | %s | %s |\n", meta.ID, layoutAccountName(meta), layoutChannelLabel(meta.Channel), channel.PasteInto)
	}
	w("\n")

	w("## 能交什么\n\n`url`、`html`、`markdown`、`text`、`file` 一次只给一个。服务器认出是哪种以后，按网页版上同样的操作排：\n\n")
	w("| 种类（`input.kind`） | 怎么交 | 等于网页上的 | 不填 `repost` 时按 | 说明 |\n| --- | --- | --- | --- | --- |\n")
	for _, spec := range layoutInputSpecs {
		w("| `%s` %s | `%s` | %s | %s | %s |\n", spec.Kind, spec.Name, spec.Field, spec.WebAction, spec.DefaultRepost, layoutDocCell(spec.Note))
	}
	w("\n`repost`：转载稿和原创稿的署名、页脚不一样。拿不准就不填，按网页上同样操作的默认排；返回的 `repost` 写着实际按哪种排的。\n\n")

	w("## 请求\n\n`POST %s/api/layout/v1/render`\n\n", base)
	w("链接、HTML、Markdown、文字用 `Content-Type: application/json`，请求体字段：\n\n")
	writeLayoutFieldTable(&b, reflect.TypeOf(layoutRenderRequestJSON{}), true)
	w("\nWord 用 `multipart/form-data`：文件放在 `file` 字段，其余字段（`account`、`title`、`repost` 写 `true`/`false`、`wait_seconds`）作为同名表单字段，不收 `url`、`html`、`markdown`、`text`。\n\n")
	w("没写进上表的字段一律 400 `bad_request`（防止拼错字段名却以为生效了）。\n\n")

	w("## 每种输入怎么交\n\n先 `export FLK=flk_你的钥匙`。下面每段都能直接跑（`jq` 用来把文件内容安全地放进 JSON；没有 jq 就用后面的 Python）。客户端超时一律设 %d 秒（`--max-time %d`）："+
		"服务器最多等 %d 秒就会回答，多留的是网络余量。\n\n",
		int(layoutClientTimeout/time.Second), int(layoutClientTimeout/time.Second), int(layoutMaxWait/time.Second))
	for _, example := range layoutInputExamples {
		spec := layoutInputSpecFor(example.Kind)
		w("### %s（`%s`）\n\n", spec.Name, spec.Kind)
		w("%s。\n\n```bash\n%s```\n\n", spec.Note, renderLayoutCurl(example))
	}
	w("### Python（所有输入通用，带轮询和重提）\n\n需要 `pip install requests`。\n\n```python\n%s```\n\n", layoutPythonClient())

	w("## 回答\n\n")
	w("### 排好了（200）\n\n")
	writeLayoutFieldTable(&b, reflect.TypeOf(layoutResultJSON{}), false)
	w("\n- 同一个请求先回 202、再用 `poll_url` 取到的 200，和直接回的 200 逐字节一样。\n")
	w("- 链接、Word、飞书文档的标题不在 `html` 里，贴进后台时标题单独填 `title`。粘贴的 HTML、Markdown、文字原样排（和网页版一样），开头写的「# 标题」或第一行会留在 `html` 里当正文；不想要就别写进正文，改用 `title` 传。\n")
	w("- `output_hash` 只看 `html`：两次排出来一模一样，它就一样。\n\n")
	w("### 还在排（202）\n\n")
	writeLayoutFieldTable(&b, reflect.TypeOf(layoutPendingJSON{}), false)
	w("\n202 同时带请求头 `Location`（就是 `poll_url`）和 `Retry-After`。\n\n```json\n%s\n```\n\n", layoutExamplePending())
	w("### 出错（4xx、5xx）\n\n")
	writeLayoutFieldTable(&b, reflect.TypeOf(layoutErrorJSON{}), false)
	w("\n能重发的错误同时带请求头 `Retry-After`。\n\n```json\n%s\n```\n\n", layoutExampleError())

	w("## 还没排好（202）怎么取\n\n")
	w("POST 默认最多等 %d 秒（`wait_seconds` 可填 0 到 %d）。飞书长文档、图多的 Word 可能更久，这时先回 202：\n\n", int(layoutDefaultWait/time.Second), int(layoutMaxWait/time.Second))
	w("1. 记下 `operation.poll_url`。\n")
	w("2. 用**同一把钥匙** `GET %s<poll_url>?wait_seconds=%d`：服务器最多陪你等 %d 秒，排好了立刻回。\n", base, int(layoutPollMaxWait/time.Second), int(layoutPollMaxWait/time.Second))
	w("3. 还是 202 就再取一次（不用自己 sleep，`wait_seconds` 已经在等了）；回 200 或错误就结束。\n")
	w("4. 别家钥匙取不到你的稿子（回 404 `operation_not_found`）；排好的结果留 %s，过期回 404 `operation_expired`。\n\n", layoutDurationLabel(layoutResultTTL))
	w("```bash\ncurl -sS --max-time 40 -H \"Authorization: Bearer $FLK\" \\\n  \"%s/api/layout/v1/operations/op_…?wait_seconds=%d\"\n```\n\n", base, int(layoutPollMaxWait/time.Second))
	w("一篇从提交起最长排 %s，超了回 504 `timeout`。轮询本身也计次（见「限额」），请带 `wait_seconds` 长等，不要连着发。\n\n", layoutDurationLabel(layoutOperationTimeout))

	w("## 出错\n\n程序按 `error.code` 分支；`error.fix` 写给人看，AI 助手原话转告即可。`retryable` 为 true 的，等 `retry_after_seconds` 秒（没有就等 30 秒）原样重发。\n\n")
	w("| code | HTTP | 能重发 | 什么意思 | 怎么办 |\n| --- | --- | --- | --- | --- |\n")
	for _, spec := range layoutErrorSpecs {
		retry := "否"
		if spec.Retryable {
			retry = "能"
			if spec.RetryAfter > 0 {
				retry = fmt.Sprintf("能，等 %d 秒", int(spec.RetryAfter/time.Second))
			}
		}
		w("| `%s` | %d | %s | %s | %s |\n", spec.Code, spec.Status, retry, layoutDocCell(spec.Message), layoutDocCell(spec.Fix))
	}
	w("\n### 排版工具重启了（`operation_lost`）\n\n")
	w("排版中的稿子只在服务器内存里，排版工具更新上线会重启，正在排的和排好没取走的都会丢，" +
		"拿着旧的 `poll_url` 去取回 404 `operation_lost`。这时**原样重新提交一次**；重提最多 3 次，依次等 10 秒、30 秒、60 秒；3 次都不行把 `request_id` 发给排版工具维护者。" +
		"上线那一两分钟里新提交会回 503 `service_restarting`，同样等一会儿重发。\n\n")

	w("## 图片\n\n`images` 列出 `html` 里的每一张图和它在哪个图床：\n\n")
	w("- `fbif`：FBIF 自己的图床（OSS，和 FoodTalks 图床 `static.foodtalks.cn`），长期有效，公众号后台和 FoodTalks 后台都能用。\n")
	w("- `wechat`：微信图床（`mmbiz.qpic.cn`、`mmbiz.qlogo.cn`）。只有公众号账号会留，粘进公众号后台编辑器微信自己认；FoodTalks 账号一律转成 `fbif`。\n\n")
	w("各渠道怎么处理：\n\n")
	for _, channel := range layoutChannelSpecs {
		w("- %s：%s。\n", layoutChannelAccounts(channel.Channel), channel.Images)
	}
	w("\n交 HTML、Markdown 时图片写公开的 https 绝对地址，服务器会转存到 OSS。"+
		"Markdown 里 png、jpeg、gif、webp 的 `data:` 内嵌图也会转存（加起来不超过 %d MB）；HTML 里的 `data:` 图和网页版一样直接略过（公众号页面里的 `data:` 多是占位图）。"+
		"`blob:`、SVG 内嵌图、相对地址、内网地址都不收，整篇回 422 `image_not_allowed`，message 里写着是哪张。"+
		"Word 和飞书文档里的图片服务器自己拆出来转存，不用管。转存失败回 502 `image_mirror_failed`，可以重发。\n\n", layoutInlineImagesMaxBytes>>20)

	w("## 飞书文档\n\n")
	w("排版工具用自己的飞书应用「%s」去读文档，不借任何人的授权，所以**文档要先把这个应用加进来**，否则回 403 `feishu_not_shared`：\n\n", layoutFeishuAppName)
	w("1. 在飞书里打开文档，点右上角「…」→「更多」→「添加文档应用」。\n")
	w("2. 搜「%s」，加上（只要能阅读就够）。知识库里的文档也可以把应用加成知识库成员，整个知识库一次加好。\n", layoutFeishuAppName)
	w("3. 用文档链接（`https://….feishu.cn/docx/…` 或 `/wiki/…`）交 `url`。\n\n")
	w("表格、多维表格、目录不是文档，回 400 `feishu_not_document`。文档里的图片会转存到 OSS。飞书导出比较慢，长文档常常先回 202。\n\n")

	w("## 提醒\n\n排好了但要留意的事放在 `warnings`，不影响用：\n\n| code | 意思 |\n| --- | --- |\n")
	for _, spec := range layoutWarningSpecs {
		w("| `%s` | %s |\n", spec.Code, layoutDocCell(spec.Message))
	}
	w("\n")

	w("## 限额\n\n| 项目 | 上限 | 说明 |\n| --- | --- | --- |\n")
	for _, limit := range layoutLimits {
		w("| %s | %s | %s |\n", limit.Name, limit.Value, layoutDocCell(limit.Note))
	}
	w("\n批量排版请一篇排完再发下一篇，或最多 %d 篇并发；不要对同一篇反复提交。\n\n", layoutPerKeyConcurrent)

	w("## 和网页版不一样的地方\n\n")
	for _, diff := range layoutAPIDifferences {
		w("- **%s**：%s。\n", diff.What, diff.Why)
	}
	w("\n")

	var examples strings.Builder
	placeholder := false
	for _, example := range layoutAPIExamples {
		text, err := layoutExampleForDoc(example)
		if err != nil {
			return "", err
		}
		placeholder = placeholder || strings.Contains(text, layoutExamplePlaceholder)
		fmt.Fprintf(&examples, "### %s\n\n```json\n%s\n```\n\n", example.Title, text)
	}
	w("## 回答长什么样\n\n")
	if placeholder {
		w("下面的例子还是占位（字段齐全、内容是假的），排版程序上线后会换成真实排过的回答。\n\n")
	} else {
		w("下面是真实排过的回答（`html` 只列开头）。\n\n")
	}
	b.WriteString(examples.String())
	w("### 飞书文档先回 202\n\n```json\n%s\n```\n\n", layoutExamplePending())

	w("## 版本\n\n路径里的 `v1` 不变时，只会加字段、加错误码、加提醒，不会删字段或改字段意思；请按字段名取值、忽略不认识的字段，遇到不认识的 `error.code` 按 `retryable` 处理。" +
		"排版规则随时会被编辑改进，`rule`、`build` 记着这一篇用的是哪一版。\n")
	return b.String(), nil
}

func layoutPythonClient() string {
	var calls strings.Builder
	for _, example := range layoutInputExamples {
		spec := layoutInputSpecFor(example.Kind)
		fmt.Fprintf(&calls, "# %s\n# result = layout(%s)\n", spec.Name, example.Python)
	}
	return fmt.Sprintf(`import os, time, requests

BASE = %q
KEY = os.environ["FLK"]  # flk_ 开头的钥匙
HEADERS = {"Authorization": "Bearer " + KEY}
LOST_RETRY_WAITS = [10, 30, 60]  # operation_lost：最多重提 3 次


def _submit(payload, file_path=None):
    if file_path:  # Word 用表单上传
        with open(file_path, "rb") as f:
            data = {k: ("true" if v is True else "false" if v is False else str(v)) for k, v in payload.items()}
            return requests.post(BASE + "/api/layout/v1/render", headers=HEADERS, data=data,
                                 files={"file": (os.path.basename(file_path), f)}, timeout=%d)
    return requests.post(BASE + "/api/layout/v1/render", headers=HEADERS, json=payload, timeout=%d)


def _wait(resp):
    # 202 就按 poll_url 长等，直到 200 或出错。
    while resp.status_code == 202:
        poll_url = resp.json()["operation"]["poll_url"]
        resp = requests.get(BASE + poll_url, headers=HEADERS, params={"wait_seconds": %d}, timeout=40)
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


%s`, layoutAPIPublicBase, int(layoutClientTimeout/time.Second), int(layoutClientTimeout/time.Second),
		int(layoutPollMaxWait/time.Second), calls.String())
}

// ---- OpenAPI ----

func layoutOpenAPISchema(typ reflect.Type) map[string]any {
	switch typ.Kind() {
	case reflect.Pointer:
		return layoutOpenAPISchema(typ.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Int:
		return map[string]any{"type": "integer"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": layoutOpenAPISchema(typ.Elem())}
	case reflect.Struct:
		properties := map[string]any{}
		required := []string{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, omitempty := layoutJSONName(field)
			if name == "" {
				continue
			}
			schema := layoutOpenAPISchema(field.Type)
			if doc := field.Tag.Get("doc"); doc != "" {
				schema["description"] = doc
			}
			properties[name] = schema
			if !omitempty && field.Type.Kind() != reflect.Pointer {
				required = append(required, name)
			}
		}
		out := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	return map[string]any{}
}

func renderLayoutAPIOpenAPI() string {
	accounts := catalogOrderIDs()
	kinds := make([]string, 0, len(layoutInputSpecs))
	for _, spec := range layoutInputSpecs {
		kinds = append(kinds, string(spec.Kind))
	}
	codes := make([]string, 0, len(layoutErrorSpecs))
	for _, spec := range layoutErrorSpecs {
		codes = append(codes, string(spec.Code))
	}
	request := layoutOpenAPISchema(reflect.TypeOf(layoutRenderRequestJSON{}))
	request["properties"].(map[string]any)["account"].(map[string]any)["enum"] = accounts
	request["additionalProperties"] = false
	form := map[string]any{
		"type":     "object",
		"required": []string{"account", "file"},
		"properties": map[string]any{
			"account":      map[string]any{"type": "string", "enum": accounts, "description": "按哪个账号排"},
			"file":         map[string]any{"type": "string", "format": "binary", "description": fmt.Sprintf("Word 文件（.docx 或 .doc），不超过 %d MB", layoutFileMaxBytes>>20)},
			"title":        map[string]any{"type": "string"},
			"repost":       map[string]any{"type": "string", "enum": []string{"true", "false"}},
			"wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": int(layoutMaxWait / time.Second)},
		},
	}
	result := layoutOpenAPISchema(reflect.TypeOf(layoutResultJSON{}))
	result["properties"].(map[string]any)["input"].(map[string]any)["properties"].(map[string]any)["kind"].(map[string]any)["enum"] = kinds
	errorSchema := layoutOpenAPISchema(reflect.TypeOf(layoutErrorJSON{}))
	errorSchema["properties"].(map[string]any)["error"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)["enum"] = codes

	jsonContent := func(ref string) map[string]any {
		return map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + ref}}}
	}
	errorResponse := map[string]any{"description": "出错，见说明书「出错」", "content": jsonContent("Error")}
	renderResponses := map[string]any{
		"200":     map[string]any{"description": "排好了", "content": jsonContent("Result")},
		"202":     map[string]any{"description": "还在排，按 operation.poll_url 取", "content": jsonContent("Pending")},
		"default": errorResponse,
	}
	pollWait := map[string]any{
		"name": "wait_seconds", "in": "query", "required": false,
		"description": fmt.Sprintf("最多等几秒，0 到 %d，默认 %d", int(layoutPollMaxWait/time.Second), int(layoutPollDefaultWait/time.Second)),
		"schema":      map[string]any{"type": "integer", "minimum": 0, "maximum": int(layoutPollMaxWait / time.Second)},
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "FBIF 排版接口",
			"version":     "1",
			"description": "交一篇文章和一个账号，拿回排好的成品 HTML。完整说明（中文）：GET /api/layout/v1/docs",
		},
		"servers":  []any{map[string]any{"url": layoutAPIPublicBase}},
		"security": []any{map[string]any{"layoutKey": []string{}}},
		"paths": map[string]any{
			"/api/layout/v1/render": map[string]any{
				"post": map[string]any{
					"operationId": "layoutRender",
					"summary":     layoutAPIRoutes[0].Summary,
					"requestBody": map[string]any{
						"required": true,
						"content": map[string]any{
							"application/json":    map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/RenderRequest"}},
							"multipart/form-data": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/RenderForm"}},
						},
					},
					"responses": renderResponses,
				},
			},
			"/api/layout/v1/operations/{id}": map[string]any{
				"get": map[string]any{
					"operationId": "layoutOperation",
					"summary":     layoutAPIRoutes[1].Summary,
					"parameters": []any{
						map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
						pollWait,
					},
					"responses": renderResponses,
				},
			},
			"/api/layout/v1/docs": map[string]any{
				"get": map[string]any{
					"operationId": "layoutDocs",
					"summary":     layoutAPIRoutes[2].Summary,
					"security":    []any{},
					"responses": map[string]any{"200": map[string]any{
						"description": "说明书",
						"content":     map[string]any{"text/markdown": map[string]any{"schema": map[string]any{"type": "string"}}},
					}},
				},
			},
			"/api/layout/v1/openapi.json": map[string]any{
				"get": map[string]any{
					"operationId": "layoutOpenAPI",
					"summary":     layoutAPIRoutes[3].Summary,
					"security":    []any{},
					"responses":   map[string]any{"200": map[string]any{"description": "这份 OpenAPI"}},
				},
			},
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"layoutKey": map[string]any{"type": "http", "scheme": "bearer", "description": "flk_ 开头的钥匙，找排版工具管理员要"},
			},
			"schemas": map[string]any{
				"RenderRequest": request,
				"RenderForm":    form,
				"Result":        result,
				"Pending":       layoutOpenAPISchema(reflect.TypeOf(layoutPendingJSON{})),
				"Error":         errorSchema,
			},
		},
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		panic("排版接口 OpenAPI 生成失败：" + err.Error())
	}
	return out.String()
}
