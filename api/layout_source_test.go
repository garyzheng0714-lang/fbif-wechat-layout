package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ---- 链接 ----

func TestLayoutWechatURLBecomesBlocksCarrier(t *testing.T) {
	env := newLayoutTestEnv(t)
	blocks := []Block{{K: "h", Level: 2, Text: "小标题"}, {K: "txt", Text: "正文第一段"}}
	env.fetcher.article = layoutFetchedArticle{Title: "原文标题", Blocks: blocks, SourceName: "来源号", Byline: "作者甲",
		OriginalSourceURL: "https://example.com/original"}
	url := "https://mp.weixin.qq.com/s/abcdefg"
	result := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "ft-default", "url": url}))
	req := env.node.lastRequest(t)
	if req.Account != "ft-default" || req.Repost != nil || req.Title != "" {
		t.Fatalf("不填 repost 交 null、不覆盖标题：%+v", req)
	}
	in := req.Input
	if in.Kind != layoutCarrierBlocks || len(in.Blocks) != 2 || in.Source != url || in.SourceName != "来源号" ||
		in.Byline != "作者甲" || in.Title != "原文标题" {
		t.Fatalf("公众号链接交块：%+v", in)
	}
	if in.FetchMeta != (layoutFetchMeta{Title: "原文标题", Source: url, SourceName: "来源号", Byline: "作者甲", OriginalSourceURL: "https://example.com/original"}) {
		t.Fatalf("fetch-article 的块外字段原样带上：%+v", in.FetchMeta)
	}
	if result.Input != (layoutInputJSON{Kind: "wechat_url", URL: url, SourceName: "来源号"}) {
		t.Fatalf("回给调用方的输入：%+v", result.Input)
	}
	if result.Account.ID != "ft-default" || result.Account.PasteInto != "FoodTalks 后台编辑器" || strings.Contains(result.Account.Name, "网站") {
		t.Fatalf("账号说法：%+v", result.Account)
	}
	if result.Title != "排版程序给的标题" || !result.Repost || result.Rule.PresetID != "ft-default-preset" || result.Build != "build-sha" {
		t.Fatalf("排版程序的回答原样带出：%+v", result)
	}
	if env.fetcher.readerCalls != 0 {
		t.Fatal("有块就不走阅读模式")
	}
}

func TestLayoutWechatURLWithoutBodyIsVerifyPage(t *testing.T) {
	env := newLayoutTestEnv(t)
	url := map[string]any{"account": "fbif", "url": "https://mp.weixin.qq.com/s/abcdefg"}
	expectLayoutError(t, env.postJSON(env.keys.active, url), http.StatusBadGateway, layoutErrWechatVerifyPage)
	waitLayoutIdle(t, env.api, "c1")
	env.fetcher.err = errors.New("connection reset")
	expectLayoutError(t, env.postJSON(env.keys.active, url), http.StatusBadGateway, layoutErrFetchFailed)
	waitLayoutIdle(t, env.api, "c1")
	env.fetcher.err = errArticlePageTooLarge
	expectLayoutError(t, env.postJSON(env.keys.active, url), http.StatusUnprocessableEntity, layoutErrPageTooLarge)
	if env.fetcher.readerCalls != 0 || env.node.count() != 0 {
		t.Fatal("公众号不交给外部阅读服务，也不排")
	}
}

func TestLayoutWebURLFallsBackToReaderMarkdown(t *testing.T) {
	env := newLayoutTestEnv(t)
	env.fetcher.readerTitle, env.fetcher.readerText = "网页标题", "# 网页标题\n\n正文"
	env.node.warnings = `["表格太宽，已缩小", {"code":"heading_guess","message":"小标题是猜的"}]`
	url := "https://www.example.com/news/1.html"
	result := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "ft-default", "url": url}))
	in := env.node.lastRequest(t).Input
	if in.Kind != layoutCarrierMarkdown || in.Text != "# 网页标题\n\n正文" || in.Source != url || in.Title != "网页标题" || len(in.Blocks) != 0 {
		t.Fatalf("阅读模式交 Markdown：%+v", in)
	}
	codes := []string{}
	for _, w := range result.Warnings {
		codes = append(codes, w.Code+":"+w.Message)
	}
	want := []string{"reader_fallback:" + layoutWarning("reader_fallback").Message, "render_note:表格太宽，已缩小", "heading_guess:小标题是猜的"}
	if strings.Join(codes, "|") != strings.Join(want, "|") {
		t.Fatalf("提醒：%v", codes)
	}
	if result.Input.Kind != "web_url" {
		t.Fatalf("输入种类：%+v", result.Input)
	}
}

func TestLayoutWebURLReaderFailures(t *testing.T) {
	env := newLayoutTestEnv(t)
	body := map[string]any{"account": "ft-default", "url": "https://www.example.com/news/1.html"}
	env.fetcher.readerErr = errJinaReaderNoBody
	expectLayoutError(t, env.postJSON(env.keys.active, body), http.StatusUnprocessableEntity, layoutErrPageHasNoBody)
	waitLayoutIdle(t, env.api, "c1")
	env.fetcher.err = errors.New("dial tcp: timeout")
	expectLayoutError(t, env.postJSON(env.keys.active, body), http.StatusBadGateway, layoutErrFetchFailed)
}

func TestLayoutURLClassification(t *testing.T) {
	cases := []struct {
		url  string
		kind layoutInputKind
		code layoutErrorCode
	}{
		{"https://mp.weixin.qq.com/s/abcdefg", layoutInputWechatURL, ""},
		{"https://www.example.com/a", layoutInputWebURL, ""},
		{"https://abc.feishu.cn/docx/AbCdEfGhIjKlMnOpQrSt", layoutInputFeishuDoc, ""},
		{"https://abc.feishu.cn/wiki/AbCdEfGhIjKlMnOpQrSt", layoutInputFeishuDoc, ""},
		{"https://abc.feishu.cn/sheets/AbCdEfGhIjKlMnOpQrSt", "", layoutErrFeishuNotDocument},
		{"http://127.0.0.1:9001/api/x", "", layoutErrInvalidURL},
		{"https://user:pass@example.com/a", "", layoutErrInvalidURL},
		{"ftp://example.com/a", "", layoutErrInvalidURL},
		{"不是链接", "", layoutErrInvalidURL},
	}
	for _, c := range cases {
		req, lerr := layoutRequestForURL(layoutRequest{}, c.url)
		if c.code != "" {
			if lerr == nil || lerr.Code != c.code {
				t.Errorf("%s 要 %s：%v", c.url, c.code, lerr)
			}
			continue
		}
		if lerr != nil || req.Kind != c.kind {
			t.Errorf("%s 要认成 %s：%v %v", c.url, c.kind, req.Kind, lerr)
		}
	}
}

// ---- HTML、Markdown、文字 ----

func TestLayoutHTMLBecomesBlocksCarrier(t *testing.T) {
	env := newLayoutTestEnv(t)
	html := `<html><head><title>网页标题</title></head><body><div id="js_content"><p>第一段正文，写得足够长一些。</p><p>第二段正文。</p></div></body></html>`
	result := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "wowfood", "html": html}))
	in := env.node.lastRequest(t).Input
	if in.Kind != layoutCarrierBlocks || len(in.Blocks) == 0 || in.Title != "网页标题" {
		t.Fatalf("html 按网页抓取同一套切块：%+v", in)
	}
	if result.Input.Kind != "html" || result.Title != "排版程序给的标题" {
		t.Fatalf("%+v", result)
	}
	expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "wowfood", "html": "<div> </div>"}),
		http.StatusUnprocessableEntity, layoutErrEmptyArticle)
}

func TestLayoutMarkdownAndTextPassThrough(t *testing.T) {
	env := newLayoutTestEnv(t)
	decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "ft-series", "markdown": "# 标题\n\n正文", "repost": false}))
	req := env.node.lastRequest(t)
	if req.Input.Kind != layoutCarrierMarkdown || req.Input.Text != "# 标题\n\n正文" || req.Repost == nil || *req.Repost {
		t.Fatalf("markdown 原样交、repost 照填的交：%+v", req)
	}
	waitLayoutIdle(t, env.api, "c1")
	result := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "foodtalks-series", "text": "第一段\n\n第二段", "title": "  自己的标题  "}))
	req = env.node.lastRequest(t)
	if req.Input.Kind != layoutCarrierText || req.Input.Text != "第一段\n\n第二段" || req.Title != "自己的标题" {
		t.Fatalf("文字原样交、标题去空白后覆盖：%+v", req)
	}
	if result.Title != "自己的标题" {
		t.Fatalf("调用方给的标题优先：%q", result.Title)
	}
}

func TestLayoutNodeCarrierAlwaysHasEveryKey(t *testing.T) {
	raw, err := json.Marshal(layoutNodeRequest{Account: "fbif", Input: emptyLayoutNodeInput(layoutCarrierText)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	if v, ok := decoded["repost"]; !ok || v != nil {
		t.Fatalf("没填 repost 要交 null：%s", raw)
	}
	input := decoded["input"].(map[string]any)
	for _, key := range []string{"kind", "title", "source", "source_name", "byline", "blocks", "fetch_meta", "docx_base64", "file_name", "text"} {
		if _, ok := input[key]; !ok {
			t.Errorf("载体缺 %s：%s", key, raw)
		}
	}
	if blocks, _ := input["blocks"].([]any); blocks == nil {
		t.Fatalf("blocks 交空数组不是 null：%s", raw)
	}
}

func TestLayoutRequestValidation(t *testing.T) {
	env := newLayoutTestEnv(t)
	cases := []struct {
		name   string
		body   string
		status int
		code   layoutErrorCode
	}{
		{"两样内容", `{"account":"fbif","text":"a","markdown":"b"}`, 400, layoutErrTooManyInputs},
		{"没内容", `{"account":"fbif"}`, 400, layoutErrBadRequest},
		{"空白内容算没给", `{"account":"fbif","text":"   "}`, 400, layoutErrBadRequest},
		{"没账号", `{"text":"a"}`, 400, layoutErrBadRequest},
		{"不认识的账号", `{"account":"FBIF","text":"a"}`, 400, layoutErrUnknownAccount},
		{"拼错字段", `{"account":"fbif","txt":"a"}`, 400, layoutErrBadRequest},
		{"不是 JSON", `{"account":`, 400, layoutErrBadRequest},
		{"两个对象", `{"account":"fbif","text":"a"}{}`, 400, layoutErrBadRequest},
		{"等太久", `{"account":"fbif","text":"a","wait_seconds":101}`, 400, layoutErrBadRequest},
		{"标题太长", `{"account":"fbif","text":"a","title":"` + strings.Repeat("字", layoutTitleMaxRunes+1) + `"}`, 400, layoutErrBadRequest},
		{"repost 写成字符串", `{"account":"fbif","text":"a","repost":"yes"}`, 400, layoutErrBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expectLayoutError(t, env.postJSON(env.keys.active, c.body), c.status, c.code)
		})
	}
	req := httptest.NewRequest(http.MethodPost, "/api/layout/v1/render", strings.NewReader("account=fbif"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+env.keys.active)
	expectLayoutError(t, env.do(req), http.StatusBadRequest, layoutErrBadRequest)

	big := `{"account":"fbif","text":"` + strings.Repeat("a", layoutJSONMaxBytes) + `"}`
	expectLayoutError(t, env.postJSON(env.keys.active, big), http.StatusRequestEntityTooLarge, layoutErrRequestTooLarge)
	if env.node.count() != 0 || env.api.limits.runningFor("c1") != 0 {
		t.Fatal("请求不对不许排，也不许占着并发位")
	}
}

// ---- Word ----

const layoutTestOSSHost = "fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com"

// startLayoutUploadServer 假的上传服务：base64_images（Word、飞书的图）和 urls（成品里的外链图）都收，回 OSS 地址。
func startLayoutUploadServer(t *testing.T, status int) *layoutUploadStats {
	t.Helper()
	stats := &layoutUploadStats{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Base64Images map[string]string `json:"base64_images"`
			URLs         []string          `json:"urls"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		stats.mu.Lock()
		stats.calls++
		stats.urls = append(stats.urls, payload.URLs...)
		stats.mu.Unlock()
		if status != http.StatusOK {
			writeJSON(w, status, map[string]string{"error": "上游坏了"})
			return
		}
		results := map[string]string{}
		for key := range payload.Base64Images {
			results[key] = "https://" + layoutTestOSSHost + "/doc/" + key + ".png"
		}
		for i, src := range payload.URLs {
			results[src] = fmt.Sprintf("https://%s/url/%d.png", layoutTestOSSHost, i)
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}))
	t.Cleanup(server.Close)
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", server.URL)
	return stats
}

type layoutUploadStats struct {
	mu    sync.Mutex
	calls int
	urls  []string
}

// layoutTestDocxSeq 每份测试 Word 内容都不一样：拆图转存按整份 Word 的指纹记结果，同一份会直接用上次转存的地址。
var layoutTestDocxSeq atomic.Int64

func layoutTestDocx(t *testing.T, withImage bool) []byte {
	t.Helper()
	entries := map[string][]byte{
		"docProps/custom.xml":          []byte(fmt.Sprintf("<Properties>%s-%d</Properties>", t.Name(), layoutTestDocxSeq.Add(1))),
		"[Content_Types].xml":          []byte(`<Types/>`),
		"_rels/.rels":                  []byte(`<Relationships/>`),
		"word/document.xml":            []byte(`<w:document><w:body><w:p><w:r><w:t>正文</w:t></w:r></w:p></w:body></w:document>`),
		"word/_rels/document.xml.rels": []byte(`<Relationships><Relationship Id="rId5" Target="media/image1.png"/></Relationships>`),
	}
	if withImage {
		entries["word/media/image1.png"] = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}
	}
	return zipFixture(t, entries)
}

func layoutMultipart(t *testing.T, fields map[string]string, fileName string, file []byte) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := mw.WriteField(key, fields[key]); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		part, err := mw.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(file)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, mw.FormDataContentType()
}

func (env *layoutTestEnv) postForm(key string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/layout/v1/render", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+key)
	return env.do(req)
}

// layoutNodeEchoDocxImages 假排版程序：把 docx 里图片表指向的地址原样排进 html，看图片一路走到成品里。
func layoutNodeEchoDocxImages(t *testing.T) func(layoutNodeRequest) string {
	return func(req layoutNodeRequest) string {
		docx, err := base64.StdEncoding.DecodeString(req.Input.DocxBase64)
		if err != nil {
			t.Errorf("docx_base64 解不开：%v", err)
			return "<p>坏</p>"
		}
		urls, err := readDocxImageURLMap(docx)
		if err != nil {
			return "<p>没有图</p>"
		}
		var b strings.Builder
		for _, url := range urls {
			b.WriteString(`<p><img src="` + url + `"></p>`)
		}
		return b.String()
	}
}

func TestLayoutWordDocxIsStrippedAndMirroredBeforeNode(t *testing.T) {
	env := newLayoutTestEnv(t)
	startLayoutUploadServer(t, http.StatusOK)
	env.node.html = layoutNodeEchoDocxImages(t)
	body, contentType := layoutMultipart(t, map[string]string{"account": "fbif", "repost": "true"}, `C:\稿子\茶的故事.docx`, layoutTestDocx(t, true))
	result := decodeLayoutResultBody(t, env.postForm(env.keys.active, body, contentType))
	req := env.node.lastRequest(t)
	if req.Input.Kind != layoutCarrierDocx || req.Input.FileName != "茶的故事.docx" || req.Input.Title != "茶的故事" || req.Repost == nil || !*req.Repost {
		t.Fatalf("Word 交 docx 载体：%+v", req)
	}
	docx, _ := base64.StdEncoding.DecodeString(req.Input.DocxBase64)
	if layoutDocxHasMedia(docx) || !docxContainsEntry(docx, docxImageURLMapEntry) {
		t.Fatal("交给排版程序的 docx 不带图，图片表指向 OSS")
	}
	if result.Stats.ImagesMirrored != 1 || len(result.Images) != 1 || result.Images[0].Host != layoutImageHostOwn ||
		!strings.HasPrefix(result.Images[0].Src, "https://"+layoutTestOSSHost+"/") {
		t.Fatalf("Word 里的图转存到 OSS：%+v %+v", result.Stats, result.Images)
	}
	if result.Input != (layoutInputJSON{Kind: "word", FileName: "茶的故事.docx"}) {
		t.Fatalf("%+v", result.Input)
	}
}

func TestLayoutWordWithoutImagesNeedsNoUpload(t *testing.T) {
	env := newLayoutTestEnv(t)
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	body, contentType := layoutMultipart(t, map[string]string{"account": "fbif"}, "a.docx", layoutTestDocx(t, false))
	result := decodeLayoutResultBody(t, env.postForm(env.keys.active, body, contentType))
	if result.Stats.ImagesMirrored != 0 || env.node.lastRequest(t).Repost != nil {
		t.Fatalf("%+v", result.Stats)
	}
}

func TestLayoutWordImageMirrorFailures(t *testing.T) {
	env := newLayoutTestEnv(t)
	t.Setenv("WECHAT_UPLOAD_ENDPOINT", "")
	body, contentType := layoutMultipart(t, map[string]string{"account": "fbif"}, "a.docx", layoutTestDocx(t, true))
	expectLayoutError(t, env.postForm(env.keys.active, body, contentType), http.StatusBadGateway, layoutErrImageMirrorFailed)
	waitLayoutIdle(t, env.api, "c1")

	startLayoutUploadServer(t, http.StatusInternalServerError)
	body, contentType = layoutMultipart(t, map[string]string{"account": "fbif"}, "a.docx", layoutTestDocx(t, true))
	expectLayoutError(t, env.postForm(env.keys.active, body, contentType), http.StatusBadGateway, layoutErrImageMirrorFailed)
	if env.node.count() != 0 {
		t.Fatal("图没转存成，排版程序碰不到带图的 Word")
	}
}

func TestLayoutDocGoesThroughLibreOfficeOnly(t *testing.T) {
	env := newLayoutTestEnv(t)
	t.Setenv("CLOUDCONVERT_API_KEY", "should-never-be-used")
	doc := append(append([]byte{}, ole2Magic...), []byte("老格式 Word")...)
	send := func() *httptest.ResponseRecorder {
		body, contentType := layoutMultipart(t, map[string]string{"account": "fbif"}, "旧稿.doc", doc)
		rec := env.postForm(env.keys.active, body, contentType)
		waitLayoutIdle(t, env.api, "c1")
		return rec
	}

	t.Setenv("LIBREOFFICE_URL", "")
	expectLayoutError(t, send(), http.StatusServiceUnavailable, layoutErrDocConvertUnavailable)

	var gotAuth string
	converter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write(layoutTestDocx(t, false))
	}))
	defer converter.Close()
	t.Setenv("LIBREOFFICE_URL", converter.URL)
	t.Setenv("LIBREOFFICE_TOKEN", "lo-token")
	decodeLayoutResultBody(t, send())
	if gotAuth != "Bearer lo-token" || env.node.lastRequest(t).Input.Kind != layoutCarrierDocx {
		t.Fatalf(".doc 经自建 LibreOffice 转成 docx：%q", gotAuth)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()
	t.Setenv("LIBREOFFICE_URL", broken.URL)
	expectLayoutError(t, send(), http.StatusUnprocessableEntity, layoutErrDocConvertFailed)

	notWord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not a zip"))
	}))
	defer notWord.Close()
	t.Setenv("LIBREOFFICE_URL", notWord.URL)
	expectLayoutError(t, send(), http.StatusUnprocessableEntity, layoutErrWordUnreadable)
}

func TestLayoutWordRejectsWhatIsNotWord(t *testing.T) {
	env := newLayoutTestEnv(t)
	cases := []struct {
		name   string
		fields map[string]string
		file   []byte
		status int
		code   layoutErrorCode
	}{
		{"PDF", map[string]string{"account": "fbif"}, []byte("%PDF-1.7"), 415, layoutErrUnsupportedFile},
		{"别的压缩包", map[string]string{"account": "fbif"}, zipFixture(t, map[string][]byte{"xl/workbook.xml": []byte("x")}), 415, layoutErrUnsupportedFile},
		{"空文件", map[string]string{"account": "fbif"}, []byte{}, 415, layoutErrUnsupportedFile},
		{"没文件", map[string]string{"account": "fbif"}, nil, 400, layoutErrBadRequest},
		{"表单里带文字", map[string]string{"account": "fbif", "text": "x"}, layoutTestDocx(t, false), 400, layoutErrTooManyInputs},
		{"不认识的字段", map[string]string{"account": "fbif", "foo": "x"}, layoutTestDocx(t, false), 400, layoutErrBadRequest},
		{"repost 乱写", map[string]string{"account": "fbif", "repost": "是"}, layoutTestDocx(t, false), 400, layoutErrBadRequest},
		{"不认识的账号", map[string]string{"account": "nope"}, layoutTestDocx(t, false), 400, layoutErrUnknownAccount},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, contentType := layoutMultipart(t, c.fields, "x.docx", c.file)
			expectLayoutError(t, env.postForm(env.keys.active, body, contentType), c.status, c.code)
			waitLayoutIdle(t, env.api, "c1")
		})
	}
}

func TestLayoutWordUploadTooLarge(t *testing.T) {
	env := newLayoutTestEnv(t)
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		_ = mw.WriteField("account", "fbif")
		part, _ := mw.CreateFormFile("file", "big.docx")
		_, _ = io.Copy(part, io.LimitReader(zeroReader{}, layoutFileMaxBytes+1))
		_ = mw.Close()
		_ = pw.Close()
	}()
	rec := env.postForm(env.keys.active, pr, mw.FormDataContentType())
	_, _ = io.Copy(io.Discard, pr)
	expectLayoutError(t, rec, http.StatusRequestEntityTooLarge, layoutErrFileTooLarge)
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestLayoutDocxCarrierLimits(t *testing.T) {
	if lerr := checkLayoutDocxCarrier(layoutTestDocx(t, true)); lerr == nil || lerr.Code != layoutErrDocxImagesUnreadable {
		t.Fatalf("还带着图的 docx 不交：%v", lerr)
	}
	if lerr := checkLayoutDocxCarrier(make([]byte, layoutDocxMaxBytes+1)); lerr == nil || lerr.Code != layoutErrFileTooLarge {
		t.Fatalf("拆图后还超过上限：%v", lerr)
	}
	if lerr := checkLayoutDocxCarrier(layoutTestDocx(t, false)); lerr != nil {
		t.Fatal(lerr)
	}
}

// ---- 排版程序（Node）----

func TestLayoutNodeClientContract(t *testing.T) {
	var got struct {
		path, auth string
		body       map[string]any
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		_, _ = w.Write([]byte(`{"html":"<p>x</p>","title":"T","summary":"S","repost":false,"footer":true,"images":["a"],
			"stats":{"images":1,"headings":3},"rule":{"preset_id":"p","revision":4,"config_hash":"h"},"build":"b","warnings":["w"]}`))
	}))
	defer server.Close()
	client := &layoutNodeClient{baseURL: server.URL, token: func() string { return "tok" }, client: server.Client()}
	out, lerr := client.Layout(context.Background(), layoutNodeRequest{Account: "fbif", Input: emptyLayoutNodeInput(layoutCarrierMarkdown)})
	if lerr != nil {
		t.Fatal(lerr)
	}
	if got.path != "/layout" || got.auth != "Bearer tok" || got.body["account"] != "fbif" {
		t.Fatalf("合同：%+v", got)
	}
	if out.HTML != "<p>x</p>" || out.Summary != "S" || out.Repost || !out.Footer || out.Stats.Headings != 3 || out.Rule.Revision != 4 ||
		len(normalizeLayoutNodeWarnings(out.Warnings)) != 1 {
		t.Fatalf("回答：%+v", out)
	}

	empty := &layoutNodeClient{baseURL: server.URL, token: func() string { return "" }, client: server.Client()}
	if _, lerr := empty.Layout(context.Background(), layoutNodeRequest{}); lerr == nil || lerr.Code != layoutErrRenderUnavailable {
		t.Fatalf("没配 RENDER_TOKEN：%v", lerr)
	}
	down := &layoutNodeClient{baseURL: "http://127.0.0.1:1", token: func() string { return "tok" }, client: server.Client()}
	if _, lerr := down.Layout(context.Background(), layoutNodeRequest{}); lerr == nil || lerr.Code != layoutErrRenderUnavailable {
		t.Fatalf("排版程序没起来：%v", lerr)
	}
	huge := layoutNodeRequest{Input: emptyLayoutNodeInput(layoutCarrierText)}
	huge.Input.Text = strings.Repeat("a", layoutNodeMaxBody)
	if _, lerr := client.Layout(context.Background(), huge); lerr == nil || lerr.Code != layoutErrRequestTooLarge {
		t.Fatalf("超过排版程序请求上限：%v", lerr)
	}
}

func TestLayoutNodeErrorsMapToPublicCodes(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   layoutErrorCode
	}{
		{503, `{"error":"规则读不到","code":"rules_unavailable"}`, layoutErrRulesUnavailable},
		{422, `{"error":"Word 坏了","code":"word_unreadable"}`, layoutErrWordUnreadable},
		{422, `{"error":"没有正文","code":"empty_article"}`, layoutErrEmptyArticle},
		{400, `{"error":"x","code":"unsupported_input"}`, layoutErrRenderFailed},
		{400, `{"error":"x","code":"bad_request"}`, layoutErrRenderFailed},
		{400, `{"error":"x","code":"unknown_account"}`, layoutErrRenderFailed},
		{500, `{"error":"x","code":"render_failed"}`, layoutErrRenderFailed},
		{502, `bad gateway`, layoutErrRenderUnavailable},
		{500, `not json`, layoutErrRenderFailed},
		{200, `{"html":"  "}`, layoutErrRenderFailed},
		{200, `not json`, layoutErrRenderFailed},
	}
	for _, c := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		client := &layoutNodeClient{baseURL: server.URL, token: func() string { return "tok" }, client: server.Client()}
		_, lerr := client.Layout(context.Background(), layoutNodeRequest{})
		server.Close()
		if lerr == nil || lerr.Code != c.code {
			t.Errorf("%d %s 要 %s：%v", c.status, c.body, c.code, lerr)
		}
	}
	if lerr := layoutNodeError(422, "empty_article", "这篇只有图片"); lerr.message() != "这篇只有图片" {
		t.Fatalf("内容本身的问题原话转告：%q", lerr.message())
	}
}

func TestNormalizeLayoutNodeWarnings(t *testing.T) {
	got := normalizeLayoutNodeWarnings(json.RawMessage(`["  ", "a", {"code":"","message":"b"}, {"code":"c"}, 3, {}]`))
	want := []layoutWarningJSON{{Code: layoutWarningRenderNote, Message: "a"}, {Code: layoutWarningRenderNote, Message: "b"}, {Code: "c", Message: "c"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%+v", got)
	}
	if out := normalizeLayoutNodeWarnings(nil); out == nil || len(out) != 0 {
		t.Fatal("没有提醒是空数组")
	}
	if out := normalizeLayoutNodeWarnings(json.RawMessage(`{"x":1}`)); len(out) != 0 {
		t.Fatal("不是数组就当没有")
	}
}

func TestLayoutNodeFailureReachesCaller(t *testing.T) {
	env := newLayoutTestEnv(t)
	env.node.err = layoutNodeError(503, "rules_unavailable", "")
	expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "x"}), http.StatusServiceUnavailable, layoutErrRulesUnavailable)
	env2 := newLayoutTestEnv(t)
	env2.api.node = nil
	expectLayoutError(t, env2.postJSON(env2.keys.active, map[string]any{"account": "fbif", "text": "x"}), http.StatusServiceUnavailable, layoutErrRenderUnavailable)
}
