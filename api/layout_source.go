package main

// layout_source.go — 排版接口的输入：请求怎么解析、每种输入怎么变成交给排版程序（Node）的载体。
//
// 每个分支调的都是网页版已在用的 Go 函数，这里不写新的解析：
//   - 公众号/普通网页链接：和 POST /api/fetch-article 同一套抓取（fetchDirectBlocksWithOptions，参数按账号取），
//     普通网页直抓没正文时同样退到 Jina 阅读模式（只回 Markdown，载体 markdown，加提醒 reader_fallback）。
//   - html：网页抓取同一套 HTML→块（extractArticleBlocksWithOptions + extractWechatArticleMeta）。
//   - Word：.doc 先走自建 LibreOffice（convertViaLibreOffice，绝不走付费 CloudConvert），然后和网页上传 Word 一样
//     拆图（stripImagesFromDocxWithProgress）、整批转存 OSS（mirrorCachedDocxImagesToOSS）；拆不出、转不成就整篇失败，
//     排版程序碰不到带图的 Word。
//   - 飞书文档：layout_feishu.go。
//   - markdown、text：原样交给排版程序，由它用网页同一个函数分流。

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

type layoutInputKind string

const (
	layoutInputWechatURL layoutInputKind = "wechat_url"
	layoutInputWebURL    layoutInputKind = "web_url"
	layoutInputFeishuDoc layoutInputKind = "feishu_doc"
	layoutInputWord      layoutInputKind = "word"
	layoutInputHTML      layoutInputKind = "html"
	layoutInputMarkdown  layoutInputKind = "markdown"
	layoutInputText      layoutInputKind = "text"
)

// layoutCarrierKind 交给排版程序的载体：blocks、docx、markdown 就是浏览器交给渲染器的三种，text 是「粘贴文字」。
type layoutCarrierKind string

const (
	layoutCarrierBlocks   layoutCarrierKind = "blocks"
	layoutCarrierDocx     layoutCarrierKind = "docx"
	layoutCarrierMarkdown layoutCarrierKind = "markdown"
	layoutCarrierText     layoutCarrierKind = "text"
)

// layoutInputSpec 一种输入。说明书「能交什么」就是这张表。
type layoutInputSpec struct {
	Kind          layoutInputKind
	Name          string
	Field         string
	Carrier       layoutCarrierKind
	WebAction     string // 等于网页上的哪个操作
	DefaultRepost string // 不传 repost 时按什么排（网页上同样操作的默认）
	Note          string
}

var layoutInputSpecs = []layoutInputSpec{
	{layoutInputWechatURL, "公众号文章链接", "url", layoutCarrierBlocks, "粘贴公众号链接", "转载",
		"mp.weixin.qq.com/s 开头的文章链接"},
	{layoutInputWebURL, "普通网页链接", "url", layoutCarrierBlocks, "粘贴网页链接", "转载",
		"直接抓不到正文时改用阅读模式（返回里带提醒 reader_fallback）；FoodTalks 文章链接（foodtalks.cn/news/编号）直接取 FoodTalks 后台存的正文，读不到回 fetch_failed"},
	{layoutInputFeishuDoc, "飞书云文档链接", "url", layoutCarrierDocx, "粘贴飞书链接", "原创",
		"docx、知识库文档链接；文档要先加「" + layoutFeishuAppName + "」应用，见「飞书文档」"},
	{layoutInputWord, "Word（.docx、.doc）", "file", layoutCarrierDocx, "上传 Word", "原创",
		"multipart/form-data 上传；文档里的图片会转存到 FBIF 的 OSS"},
	{layoutInputHTML, "文章 HTML", "html", layoutCarrierBlocks, "（网页上没有这一项，按抓到的网页正文排）", "原创",
		"图片写公开的 https 绝对地址"},
	{layoutInputMarkdown, "Markdown", "markdown", layoutCarrierMarkdown, "上传 .md 文件", "原创",
		"图片用 ![](https://…) 写公开的绝对地址"},
	{layoutInputText, "纯文本", "text", layoutCarrierText, "粘贴文字", "原创",
		"没有图片链接时只排正文、不加页脚；有图片链接时按 Markdown 排"},
}

func layoutInputSpecFor(kind layoutInputKind) layoutInputSpec {
	for _, spec := range layoutInputSpecs {
		if spec.Kind == kind {
			return spec
		}
	}
	panic("排版接口输入种类不在 layoutInputSpecs 表里：" + string(kind))
}

// ---- 交给排版程序的请求与回答（Go ↔ Node 合同，Node 那边是 render-service 的 POST /layout）----

type layoutNodeRequest struct {
	Account string          `json:"account"`
	Title   string          `json:"title"`  // 调用方给的标题，空串表示不覆盖
	Repost  *bool           `json:"repost"` // null 表示按网页同样操作的默认
	Input   layoutNodeInput `json:"input"`
}

// layoutFetchMeta /api/fetch-article 回给网页的块外字段，原样带给排版程序，它用网页同一个函数判原创/转载。
type layoutFetchMeta struct {
	Title             string             `json:"title,omitempty"`
	Source            string             `json:"source,omitempty"`
	SourceName        string             `json:"source_name,omitempty"`
	Byline            string             `json:"byline,omitempty"`
	OriginalSourceURL string             `json:"original_source_url,omitempty"`
	Provenance        *articleProvenance `json:"provenance,omitempty"`
}

// layoutNodeInput 载体。字段一律都写出来（没有就是空），排版程序按 kind 读。
type layoutNodeInput struct {
	Kind       layoutCarrierKind `json:"kind"`
	Title      string            `json:"title"`
	Source     string            `json:"source"`
	SourceName string            `json:"source_name"`
	Byline     string            `json:"byline"`
	Blocks     []Block           `json:"blocks"`
	FetchMeta  layoutFetchMeta   `json:"fetch_meta"`
	DocxBase64 string            `json:"docx_base64"`
	FileName   string            `json:"file_name"`
	Text       string            `json:"text"`
}

// layoutPrepared 一篇准备好交给排版程序的东西，加上回给调用方的输入说明和这一步转存的图数。
type layoutPrepared struct {
	Input          layoutNodeInput
	View           layoutInputJSON
	Warnings       []layoutWarningJSON
	ImagesMirrored int
}

// ---- 解析请求 ----

func parseLayoutRenderRequest(w http.ResponseWriter, r *http.Request) (layoutRequest, *layoutError) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		mediaType = ""
	}
	switch strings.ToLower(mediaType) {
	case "application/json":
		return parseLayoutJSONRequest(w, r)
	case "multipart/form-data":
		return parseLayoutFormRequest(w, r)
	default:
		return layoutRequest{}, newLayoutError(layoutErrBadRequest,
			"Content-Type 要是 application/json（链接、HTML、Markdown、文字）或 multipart/form-data（上传 Word）")
	}
}

func parseLayoutJSONRequest(w http.ResponseWriter, r *http.Request) (layoutRequest, *layoutError) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, layoutJSONMaxBytes))
	decoder.DisallowUnknownFields()
	var body layoutRenderRequestJSON
	if err := decoder.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return layoutRequest{}, newLayoutError(layoutErrRequestTooLarge,
				fmt.Sprintf("JSON 请求体超过 %d MB", layoutJSONMaxBytes>>20))
		}
		if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
			return layoutRequest{}, newLayoutError(layoutErrBadRequest,
				"不认识的字段 "+field+"；能写的字段见说明书「请求」一节")
		}
		return layoutRequest{}, newLayoutError(layoutErrBadRequest, "请求体不是合法的 JSON 对象："+err.Error())
	}
	if decoder.More() {
		return layoutRequest{}, newLayoutError(layoutErrBadRequest, "请求体只能是一个 JSON 对象")
	}
	req, lerr := layoutRequestBase(body.Account, body.Title, body.Repost)
	if lerr != nil {
		return layoutRequest{}, lerr
	}
	wait := layoutDefaultWait
	if body.WaitSeconds != nil {
		if *body.WaitSeconds < 0 || time.Duration(*body.WaitSeconds)*time.Second > layoutMaxWait {
			return layoutRequest{}, newLayoutError(layoutErrBadRequest,
				fmt.Sprintf("wait_seconds 要是 0 到 %d 的整数", int(layoutMaxWait/time.Second)))
		}
		wait = time.Duration(*body.WaitSeconds) * time.Second
	}
	req.Wait = wait

	given := map[string]string{}
	for name, value := range map[string]string{"url": body.URL, "html": body.HTML, "markdown": body.Markdown, "text": body.Text} {
		if strings.TrimSpace(value) != "" {
			given[name] = value
		}
	}
	if len(given) > 1 {
		return layoutRequest{}, newLayoutError(layoutErrTooManyInputs,
			"url、html、markdown、text、file 只能给一个，这次给了 "+layoutJoinKeys(given))
	}
	switch {
	case given["url"] != "":
		return layoutRequestForURL(req, given["url"])
	case given["html"] != "":
		req.Kind, req.Body = layoutInputHTML, body.HTML
	case given["markdown"] != "":
		req.Kind, req.Body = layoutInputMarkdown, body.Markdown
	case given["text"] != "":
		req.Kind, req.Body = layoutInputText, body.Text
	default:
		return layoutRequest{}, newLayoutError(layoutErrBadRequest,
			"没给要排的内容：url、html、markdown、text 给一个；Word 用 multipart/form-data 上传 file")
	}
	return req, nil
}

func layoutJoinKeys(given map[string]string) string {
	var names []string
	for _, name := range []string{"url", "html", "markdown", "text", "file"} {
		if _, ok := given[name]; ok {
			names = append(names, name)
		}
	}
	return strings.Join(names, "、")
}

func layoutRequestBase(account, title string, repost *bool) (layoutRequest, *layoutError) {
	meta, lerr := layoutAccountFor(account)
	if lerr != nil {
		return layoutRequest{}, lerr
	}
	cleanTitle, lerr := layoutRequestTitle(title)
	if lerr != nil {
		return layoutRequest{}, lerr
	}
	return layoutRequest{Account: meta, Title: cleanTitle, Repost: repost}, nil
}

// layoutRequestForURL 一个链接是飞书、公众号还是普通网页，只在这里判断。
func layoutRequestForURL(req layoutRequest, raw string) (layoutRequest, *layoutError) {
	raw = strings.TrimSpace(raw)
	if ref, err := parseFeishuDocumentURL(raw); err == nil {
		req.Kind, req.URL, req.Feishu = layoutInputFeishuDoc, ref.URL, ref
		return req, nil
	}
	parsed, err := parsePublicWebURL(raw)
	if err != nil {
		return layoutRequest{}, layoutErr(layoutErrInvalidURL)
	}
	if officialFeishuDocumentHost(parsed.Hostname()) {
		return layoutRequest{}, layoutErr(layoutErrFeishuNotDocument)
	}
	req.URL = parsed.String()
	req.Kind = layoutInputWebURL
	if isSafeWeChatArticleURL(req.URL) {
		req.Kind = layoutInputWechatURL
	}
	return req, nil
}

// parseLayoutFormRequest 上传 Word。只放宽这一个请求的读超时（全局 ReadTimeout 30 秒不动），边读边限大小，不落临时文件。
func parseLayoutFormRequest(w http.ResponseWriter, r *http.Request) (layoutRequest, *layoutError) {
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(layoutUploadReadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("layout-api: 放宽上传读超时失败：%v", err)
	}
	// 写超时从读完请求头起算（全局 150 秒），慢上传会把它吃掉：给读和等各留够时间。
	if err := controller.SetWriteDeadline(time.Now().Add(layoutUploadReadTimeout + layoutMaxWait + 30*time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("layout-api: 放宽上传写超时失败：%v", err)
	}
	// 整个表单最多比文件上限多 1 MB（放其余字段和分隔行）。
	r.Body = http.MaxBytesReader(w, r.Body, layoutFileMaxBytes+1<<20)
	reader, err := r.MultipartReader()
	if err != nil {
		return layoutRequest{}, newLayoutError(layoutErrBadRequest, "multipart/form-data 读不出来："+err.Error())
	}
	fields := map[string]string{}
	var fileName string
	var file []byte
	var extra []string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return layoutRequest{}, newLayoutError(layoutErrFileTooLarge,
					fmt.Sprintf("上传的文件超过 %d MB", layoutFileMaxBytes>>20))
			}
			return layoutRequest{}, newLayoutError(layoutErrBadRequest, "上传中断或表单格式不对："+err.Error())
		}
		name := part.FormName()
		switch name {
		case "file":
			if file != nil {
				_ = part.Close()
				return layoutRequest{}, newLayoutError(layoutErrTooManyInputs, "一次只能传一个 file")
			}
			data, err := readBoundedBody(part, layoutFileMaxBytes)
			_ = part.Close()
			var tooLarge *http.MaxBytesError
			if errors.Is(err, errOutboundResponseTooLarge) || errors.As(err, &tooLarge) {
				return layoutRequest{}, newLayoutError(layoutErrFileTooLarge,
					fmt.Sprintf("上传的文件超过 %d MB", layoutFileMaxBytes>>20))
			}
			if err != nil {
				return layoutRequest{}, newLayoutError(layoutErrBadRequest, "上传中断："+err.Error())
			}
			fileName, file = path.Base(strings.ReplaceAll(part.FileName(), "\\", "/")), data
		case "account", "title", "repost", "wait_seconds":
			value, err := readBoundedBody(part, 4<<10)
			_ = part.Close()
			if err != nil {
				return layoutRequest{}, newLayoutError(layoutErrBadRequest, "表单字段 "+name+" 太长或读不出来")
			}
			fields[name] = string(value)
		default:
			_ = part.Close()
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		name := extra[0]
		switch name {
		case "url", "html", "markdown", "text":
			if file != nil {
				return layoutRequest{}, newLayoutError(layoutErrTooManyInputs,
					"url、html、markdown、text、file 只能给一个，这次表单里还带了 "+name)
			}
			return layoutRequest{}, newLayoutError(layoutErrBadRequest,
				"表单上传只收 Word 文件；"+name+" 请用 application/json 发")
		}
		return layoutRequest{}, newLayoutError(layoutErrBadRequest, "不认识的表单字段 "+name+"；只收 account、file、title、repost、wait_seconds")
	}
	var repost *bool
	switch strings.ToLower(strings.TrimSpace(fields["repost"])) {
	case "":
	case "true":
		v := true
		repost = &v
	case "false":
		v := false
		repost = &v
	default:
		return layoutRequest{}, newLayoutError(layoutErrBadRequest, "repost 只能写 true 或 false")
	}
	req, lerr := layoutRequestBase(fields["account"], fields["title"], repost)
	if lerr != nil {
		return layoutRequest{}, lerr
	}
	wait, lerr := parseLayoutWait(fields["wait_seconds"], layoutDefaultWait, layoutMaxWait)
	if lerr != nil {
		return layoutRequest{}, lerr
	}
	req.Wait = wait
	if file == nil {
		return layoutRequest{}, newLayoutError(layoutErrBadRequest, "表单里没有 file：Word 文件放在 file 字段")
	}
	if len(file) == 0 {
		return layoutRequest{}, newLayoutError(layoutErrUnsupportedFile, "上传的文件是空的")
	}
	req.Kind, req.FileName, req.File = layoutInputWord, fileName, file
	// 读完了：接下来最多等 wait，写超时照这个重算。
	if err := controller.SetWriteDeadline(time.Now().Add(wait + 30*time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.Printf("layout-api: 重设上传写超时失败：%v", err)
	}
	return req, nil
}

// ---- 准备载体 ----

// layoutFetcher 抓链接，和 /api/fetch-article 是同一套；单独成口子好在测试里换假的。
type layoutFetcher interface {
	FetchArticle(ctx context.Context, url string, opts articleBlockOptions) (layoutFetchedArticle, error)
	FetchReader(ctx context.Context, url string) (title, markdown string, err error)
}

type layoutFetchedArticle struct {
	Title             string
	Blocks            []Block
	SourceName        string
	Byline            string
	OriginalSourceURL string
	DeclaredOriginal  bool   // 公众号标了「原创」
	RepostedFrom      string // 用公众号「转载」功能转的：原号名
}

// articleLayoutFetcher 生产用：直抓（最多 3 次）+ Jina 阅读模式，与网页导入链接一致；
// FoodTalks 文章链接和网页一样走管理接口取正文（foodtalks_news_import.go）。
type articleLayoutFetcher struct{}

func (articleLayoutFetcher) FetchArticle(ctx context.Context, url string, opts articleBlockOptions) (layoutFetchedArticle, error) {
	if news, handled, err := foodTalksNewsArticleBlocks(ctx, url, opts); handled {
		return layoutFetchedArticle{Title: news.Title, Blocks: news.Blocks, SourceName: news.SourceName, Byline: news.Byline}, err
	}
	direct, err := fetchDirectBlocksWithOptions(url, opts)
	return layoutFetchedArticle{Title: direct.Title, Blocks: direct.Blocks, SourceName: direct.SourceName, Byline: direct.Byline,
		OriginalSourceURL: direct.OriginalSourceURL, DeclaredOriginal: direct.DeclaredOriginal, RepostedFrom: direct.RepostedFrom}, err
}

func (articleLayoutFetcher) FetchReader(ctx context.Context, url string) (string, string, error) {
	return fetchArticleMarkdownViaJina(ctx, url)
}

func emptyLayoutNodeInput(kind layoutCarrierKind) layoutNodeInput {
	return layoutNodeInput{Kind: kind, Blocks: []Block{}}
}

func (a *layoutAPI) prepare(ctx context.Context, op *layoutOperation, req layoutRequest) (layoutPrepared, *layoutError) {
	view := layoutInputJSON{Kind: string(req.Kind)}
	switch req.Kind {
	case layoutInputWechatURL, layoutInputWebURL:
		view.URL = req.URL
		return a.prepareURL(ctx, req, view)
	case layoutInputFeishuDoc:
		view.URL = req.URL
		if a.feishu == nil {
			return layoutPrepared{}, newLayoutError(layoutErrFeishuUnavailable, "飞书导出没有接上")
		}
		doc, lerr := a.feishu.export(ctx, op.id, req.RequestID, req.Feishu)
		if lerr != nil {
			return layoutPrepared{}, lerr
		}
		input := emptyLayoutNodeInput(layoutCarrierDocx)
		input.Title, input.FileName = doc.Title, doc.FileName
		input.DocxBase64 = base64.StdEncoding.EncodeToString(doc.Docx)
		return layoutPrepared{Input: input, View: view, ImagesMirrored: doc.ImagesMirrored}, nil
	case layoutInputWord:
		view.FileName = req.FileName
		docx, mirrored, lerr := prepareLayoutWord(ctx, req.FileName, req.File)
		if lerr != nil {
			return layoutPrepared{}, lerr
		}
		input := emptyLayoutNodeInput(layoutCarrierDocx)
		input.Title = strings.TrimSuffix(strings.TrimSuffix(req.FileName, path.Ext(req.FileName)), ".")
		input.FileName = req.FileName
		input.DocxBase64 = base64.StdEncoding.EncodeToString(docx)
		return layoutPrepared{Input: input, View: view, ImagesMirrored: mirrored}, nil
	case layoutInputHTML:
		title, blocks, _ := extractArticleBlocksWithOptions(req.Body, articleBlockOptionsForAccount(a.rules, req.Account.ID))
		if len(blocks) == 0 {
			return layoutPrepared{}, newLayoutError(layoutErrEmptyArticle, "html 里没认出正文段落")
		}
		sourceName, byline, _ := extractWechatArticleMeta(req.Body)
		input := emptyLayoutNodeInput(layoutCarrierBlocks)
		input.Title, input.SourceName, input.Byline, input.Blocks = title, sourceName, byline, blocks
		view.SourceName = sourceName
		return layoutPrepared{Input: input, View: view}, nil
	case layoutInputMarkdown:
		input := emptyLayoutNodeInput(layoutCarrierMarkdown)
		input.Text = req.Body
		return layoutPrepared{Input: input, View: view}, nil
	case layoutInputText:
		input := emptyLayoutNodeInput(layoutCarrierText)
		input.Text = req.Body
		return layoutPrepared{Input: input, View: view}, nil
	}
	return layoutPrepared{}, newLayoutError(layoutErrBadRequest, "不认识的输入种类")
}

// prepareURL 和 /api/fetch-article 同样的判断：有块就交块；公众号没块说验证页；普通网页没块退到 Jina 阅读模式。
func (a *layoutAPI) prepareURL(ctx context.Context, req layoutRequest, view layoutInputJSON) (layoutPrepared, *layoutError) {
	if a.fetcher == nil {
		return layoutPrepared{}, newLayoutError(layoutErrFetchFailed, "抓取没有接上")
	}
	article, err := a.fetcher.FetchArticle(ctx, req.URL, articleBlockOptionsForAccount(a.rules, req.Account.ID))
	if errors.Is(err, errArticlePageTooLarge) {
		return layoutPrepared{}, layoutErr(layoutErrPageTooLarge).withCause(err)
	}
	if err == nil && len(article.Blocks) > 0 {
		input := emptyLayoutNodeInput(layoutCarrierBlocks)
		input.Title, input.Source, input.SourceName, input.Byline = article.Title, req.URL, article.SourceName, article.Byline
		input.Blocks = article.Blocks
		input.FetchMeta = layoutFetchMeta{Title: article.Title, Source: req.URL, SourceName: article.SourceName,
			Byline: article.Byline, OriginalSourceURL: article.OriginalSourceURL,
			Provenance: articleBlocksProvenance(article.SourceName, article.Blocks, article.DeclaredOriginal, article.RepostedFrom)}
		view.SourceName = article.SourceName
		return layoutPrepared{Input: input, View: view}, nil
	}
	if req.Kind == layoutInputWechatURL {
		if err != nil {
			return layoutPrepared{}, layoutErr(layoutErrFetchFailed).withCause(err)
		}
		return layoutPrepared{}, layoutErr(layoutErrWechatVerifyPage)
	}
	// FoodTalks 文章和网页导入一样只走管理接口：读不到就报错，不退到阅读模式（前端渲染的页面读出来是导航和页脚）。
	if _, ok := foodTalksNewsIDFromURL(req.URL); ok {
		if err == nil {
			err = errors.New("FoodTalks 文章没有正文")
		}
		return layoutPrepared{}, layoutErr(layoutErrFetchFailed).withCause(err)
	}
	// 不把调用方的网址交给外部阅读服务以外的进程；Jina 走自己的出口代理，和网页一致。
	title, markdown, jerr := a.fetcher.FetchReader(ctx, req.URL)
	if jerr != nil {
		if err != nil {
			return layoutPrepared{}, layoutErr(layoutErrFetchFailed).withCause(fmt.Errorf("直抓：%v；阅读模式：%w", err, jerr))
		}
		return layoutPrepared{}, layoutErr(layoutErrPageHasNoBody).withCause(jerr)
	}
	input := emptyLayoutNodeInput(layoutCarrierMarkdown)
	input.Title, input.Source, input.Text = title, req.URL, markdown
	input.FetchMeta = layoutFetchMeta{Title: title, Source: req.URL}
	return layoutPrepared{Input: input, View: view, Warnings: []layoutWarningJSON{layoutWarning("reader_fallback")}}, nil
}

// prepareLayoutWord .doc 先转 .docx，再拆图转存。返回去掉图片、图片表已指向 OSS 的 docx 和转存张数。
func prepareLayoutWord(ctx context.Context, fileName string, data []byte) ([]byte, int, *layoutError) {
	docx := data
	if len(data) >= len(ole2Magic) && bytes.Equal(data[:len(ole2Magic)], ole2Magic) {
		loURL := strings.TrimSpace(os.Getenv("LIBREOFFICE_URL"))
		if loURL == "" {
			return nil, 0, layoutErr(layoutErrDocConvertUnavailable)
		}
		converted, err := convertViaLibreOffice(ctx, loURL, strings.TrimSpace(os.Getenv("LIBREOFFICE_TOKEN")), data, fileName)
		if err != nil {
			return nil, 0, layoutErr(layoutErrDocConvertFailed).withCause(err)
		}
		docx = converted
	} else if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return nil, 0, layoutErr(layoutErrUnsupportedFile)
	}
	if !docxContainsEntry(docx, "word/document.xml") {
		if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return nil, 0, layoutErr(layoutErrWordUnreadable)
		}
		// 是 zip 但不是 Word（表格、PPT、普通压缩包）。
		return nil, 0, layoutErr(layoutErrUnsupportedFile)
	}
	stub, err := stripImagesFromDocxWithProgress(ctx, docx)
	if err != nil {
		return nil, 0, layoutErr(layoutErrDocxImagesUnreadable).withCause(err)
	}
	mirrored := 0
	if docxContainsEntry(stub, docxImageURLMapEntry) {
		if !docImageMirroringConfigured() {
			return nil, 0, newLayoutError(layoutErrImageMirrorFailed, "图片转存服务没配置").withCause(errors.New("WECHAT_UPLOAD_ENDPOINT 未配置"))
		}
		out, count, err := mirrorCachedDocxImagesToOSS(ctx, docx, stub)
		if err != nil {
			return nil, 0, layoutErr(layoutErrImageMirrorFailed).withCause(err)
		}
		stub, mirrored = out, count
	}
	if lerr := checkLayoutDocxCarrier(stub); lerr != nil {
		return nil, 0, lerr
	}
	return stub, mirrored, nil
}

// checkLayoutDocxCarrier 交给排版程序前最后看一眼：不能还带着图（排版程序那边没法处理），也不能太大。
func checkLayoutDocxCarrier(docx []byte) *layoutError {
	if layoutDocxHasMedia(docx) {
		return layoutErr(layoutErrDocxImagesUnreadable).withCause(errors.New("拆图后 word/media/ 里还有文件"))
	}
	if len(docx) > layoutDocxMaxBytes {
		return newLayoutError(layoutErrFileTooLarge,
			fmt.Sprintf("Word 去掉图片后还有 %d MB，超过 %d MB", len(docx)>>20, layoutDocxMaxBytes>>20))
	}
	return nil
}

func layoutDocxHasMedia(docx []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		return false
	}
	for _, file := range zr.File {
		if strings.HasPrefix(file.Name, "word/media/") && !file.FileInfo().IsDir() {
			return true
		}
	}
	return false
}

// ---- 排版程序（Node）----

// layoutRenderer 把载体交给排版程序排。单独成口子好在测试里换假的。
type layoutRenderer interface {
	Layout(ctx context.Context, req layoutNodeRequest) (layoutNodeResult, *layoutError)
}

type layoutNodeStats struct {
	Images   int `json:"images"`
	Headings int `json:"headings"`
}

// layoutNodeResult 排版程序 200 的回答。images 是 html 里 src 属性的原文，只作参考，图片以 Go 自己扫的为准。
type layoutNodeResult struct {
	HTML     string          `json:"html"`
	Title    string          `json:"title"`
	Summary  string          `json:"summary"`
	Repost   bool            `json:"repost"`
	Footer   bool            `json:"footer"`
	Images   []string        `json:"images"`
	Stats    layoutNodeStats `json:"stats"`
	Rule     layoutRuleJSON  `json:"rule"`
	Build    string          `json:"build"`
	Warnings json.RawMessage `json:"warnings"`
}

type layoutNodeClient struct {
	baseURL string
	token   func() string
	client  *http.Client
}

func newLayoutNodeClientFromEnv() *layoutNodeClient {
	return &layoutNodeClient{baseURL: layoutRenderURL(), token: renderToken, client: &http.Client{Timeout: layoutNodeTimeout}}
}

func (c *layoutNodeClient) Layout(ctx context.Context, req layoutNodeRequest) (layoutNodeResult, *layoutError) {
	token := c.token()
	if token == "" {
		return layoutNodeResult{}, newLayoutError(layoutErrRenderUnavailable, "排版程序的凭证没配置").
			withCause(errors.New("RENDER_TOKEN 未配置"))
	}
	body, err := json.Marshal(req)
	if err != nil {
		return layoutNodeResult{}, layoutErr(layoutErrRenderFailed).withCause(err)
	}
	if len(body) > layoutNodeMaxBody {
		return layoutNodeResult{}, newLayoutError(layoutErrRequestTooLarge,
			fmt.Sprintf("这篇交给排版程序的内容有 %d MB，超过 %d MB", len(body)>>20, layoutNodeMaxBody>>20))
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/layout", bytes.NewReader(body))
	if err != nil {
		return layoutNodeResult{}, layoutErr(layoutErrRenderUnavailable).withCause(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return layoutNodeResult{}, layoutContextError(ctx, layoutErr(layoutErrRenderUnavailable).withCause(err))
	}
	defer resp.Body.Close()
	raw, err := readBoundedBody(resp.Body, layoutNodeMaxResponse)
	if err != nil {
		return layoutNodeResult{}, layoutContextError(ctx, layoutErr(layoutErrRenderFailed).withCause(fmt.Errorf("读排版程序回答：%w", err)))
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(raw, &failure)
		return layoutNodeResult{}, layoutNodeError(resp.StatusCode, failure.Code, failure.Error)
	}
	var out layoutNodeResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return layoutNodeResult{}, layoutErr(layoutErrRenderFailed).withCause(fmt.Errorf("排版程序回答不是约定的 JSON：%w", err))
	}
	if strings.TrimSpace(out.HTML) == "" {
		return layoutNodeResult{}, layoutErr(layoutErrRenderFailed).withCause(errors.New("排版程序回了空 html"))
	}
	return out, nil
}

// layoutNodeError 排版程序的错误码翻成对外的。请求是 Go 组的，unsupported_input、bad_request、unknown_account
// 都是两边对不上，对调用方一律说 render_failed；只有内容本身的问题（读不出、没正文）原话转告。
func layoutNodeError(status int, code, message string) *layoutError {
	cause := fmt.Errorf("排版程序 HTTP %d code=%s：%s", status, firstNonEmpty(code, "-"), message)
	message = strings.TrimSpace(message)
	switch code {
	case "rules_unavailable":
		return layoutErr(layoutErrRulesUnavailable).withCause(cause)
	case "word_unreadable":
		return newLayoutError(layoutErrWordUnreadable, firstNonEmpty(message, layoutErrorSpecByCode[layoutErrWordUnreadable].Message)).withCause(cause)
	case "empty_article":
		return newLayoutError(layoutErrEmptyArticle, firstNonEmpty(message, layoutErrorSpecByCode[layoutErrEmptyArticle].Message)).withCause(cause)
	case "render_failed", "unsupported_input", "bad_request", "unknown_account":
		return layoutErr(layoutErrRenderFailed).withCause(cause)
	}
	if status == http.StatusServiceUnavailable || status == http.StatusBadGateway {
		return layoutErr(layoutErrRenderUnavailable).withCause(cause)
	}
	return layoutErr(layoutErrRenderFailed).withCause(cause)
}

// normalizeLayoutNodeWarnings 排版程序的提醒可以是字符串，也可以是 {code,message}；统一成 {code,message}。
func normalizeLayoutNodeWarnings(raw json.RawMessage) []layoutWarningJSON {
	out := []layoutWarningJSON{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return out
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return out
	}
	for _, item := range items {
		var text string
		if json.Unmarshal(item, &text) == nil {
			if text = strings.TrimSpace(text); text != "" {
				out = append(out, layoutWarningJSON{Code: layoutWarningRenderNote, Message: text})
			}
			continue
		}
		var obj layoutWarningJSON
		if json.Unmarshal(item, &obj) == nil && strings.TrimSpace(obj.Message+obj.Code) != "" {
			obj.Code = firstNonEmpty(strings.TrimSpace(obj.Code), layoutWarningRenderNote)
			obj.Message = firstNonEmpty(strings.TrimSpace(obj.Message), obj.Code)
			out = append(out, obj)
		}
	}
	return out
}
