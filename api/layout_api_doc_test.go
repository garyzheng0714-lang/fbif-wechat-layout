package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const layoutAPIDocPath = "../docs/layout-api.md"

// TestLayoutAPIDocIsGenerated docs/layout-api.md 就是线上 GET /api/layout/v1/docs 回的那份，从代码里的表生成。
// 改了路由、字段、输入、错误、限额、账号或例子，跑 cd api && SPEC_UPDATE=1 go test -run TestLayoutAPIDocIsGenerated . 重新生成。
func TestLayoutAPIDocIsGenerated(t *testing.T) {
	doc, err := renderLayoutAPIDoc()
	if err != nil {
		t.Fatalf("说明书生成失败：%v", err)
	}
	if os.Getenv("SPEC_UPDATE") != "1" {
		if got, err := os.ReadFile(layoutAPIDocPath); err != nil || string(got) != doc {
			t.Fatalf("%s 和代码对不上（%v）：改完代码跑 cd api && SPEC_UPDATE=1 go test -run TestLayoutAPIDocIsGenerated . 重新生成", layoutAPIDocPath, err)
		}
	}
	checkGeneratedSpecFile(t, layoutAPIDocPath, doc)
}

func TestLayoutAPIDocCoversEverything(t *testing.T) {
	doc, err := renderLayoutAPIDoc()
	if err != nil {
		t.Fatal(err)
	}
	must := []string{
		"--max-time 130", "10 秒、30 秒、60 秒", "添加文档应用", layoutFeishuAppName, "`wechat`", "`fbif`", "更多文章",
		"poll_url", "wait_seconds", "Retry-After", "X-Request-ID", "multipart/form-data", "-new-layout-key", "pip install requests",
	}
	for _, route := range layoutAPIRoutes {
		must = append(must, route.Path)
	}
	for _, spec := range layoutErrorSpecs {
		must = append(must, "`"+string(spec.Code)+"`", spec.Fix)
	}
	for _, spec := range layoutInputSpecs {
		must = append(must, "`"+string(spec.Kind)+"`")
	}
	for _, id := range catalogOrderIDs() {
		must = append(must, "`"+id+"`")
	}
	for _, limit := range layoutLimits {
		must = append(must, limit.Name)
	}
	for _, spec := range layoutWarningSpecs {
		must = append(must, "`"+spec.Code+"`")
	}
	for _, field := range layoutDocFields(reflect.TypeOf(layoutResultJSON{}), "") {
		must = append(must, "`"+field.Path+"`")
	}
	for _, want := range must {
		if !strings.Contains(doc, want) {
			t.Errorf("说明书里要有 %q", want)
		}
	}
	for _, example := range layoutInputExamples {
		if !strings.Contains(doc, renderLayoutCurl(example)) {
			t.Errorf("%s 要有能直接跑的 curl", example.Kind)
		}
	}
	if len(layoutInputExamples) != len(layoutInputSpecs) {
		t.Error("每种输入都要有例子")
	}
	if strings.Contains(doc, "网站") {
		t.Error("对外只说 FoodTalks，不说网站")
	}
}

func TestLayoutAPIDocAndOpenAPIAreServed(t *testing.T) {
	env := newLayoutTestEnv(t)
	rec := env.do(httptest.NewRequest(http.MethodGet, "/api/layout/v1/docs", nil))
	want, _ := renderLayoutAPIDoc()
	if rec.Code != http.StatusOK || rec.Body.String() != want || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("GET /docs 回的就是生成的那份：%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	rec = env.do(httptest.NewRequest(http.MethodGet, "/api/layout/v1/openapi.json", nil))
	var spec struct {
		OpenAPI    string                    `json:"openapi"`
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil || spec.OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI 要是合法 JSON：%v", err)
	}
	for _, route := range layoutAPIRoutes {
		if _, ok := spec.Paths[route.Path][strings.ToLower(route.Method)]; !ok {
			t.Errorf("OpenAPI 缺 %s %s", route.Method, route.Path)
		}
	}
	errorSchema := string(spec.Components.Schemas["Error"])
	for _, s := range layoutErrorSpecs {
		if !strings.Contains(errorSchema, `"`+string(s.Code)+`"`) {
			t.Errorf("OpenAPI 错误码枚举缺 %s", s.Code)
		}
	}
	request := string(spec.Components.Schemas["RenderRequest"])
	for _, id := range catalogOrderIDs() {
		if !strings.Contains(request, `"`+id+`"`) {
			t.Errorf("OpenAPI 账号枚举缺 %s", id)
		}
	}
	if !strings.Contains(request, `"additionalProperties": false`) {
		t.Error("请求不收多余字段")
	}
	if layoutAPIOpenAPICached() != renderLayoutAPIOpenAPI() {
		t.Error("缓存的和现生成的一样")
	}
}

// TestLayoutAPIExamplesExist 说明书里的例子回答编进程序里，缺了说明书就生成不了。
func TestLayoutAPIExamplesExist(t *testing.T) {
	for _, example := range layoutAPIExamples {
		if _, err := layoutExampleForDoc(example); err != nil {
			t.Errorf("%v\n生成方法：排版程序（LAYOUT_RENDER_URL）上线后，用一把在用的钥匙跑 "+
				"cd api && LAYOUT_EXAMPLES_BASE=https://fbifmp-layout.foodtalks.cn LAYOUT_EXAMPLES_KEY=flk_… "+
				"LAYOUT_EXAMPLES_WECHAT_URL=<一篇公众号文章> LAYOUT_EXAMPLES_DOCX=<一个 .docx> go test -run TestLayoutAPIExamplesRegenerate . ，"+
				"再 SPEC_UPDATE=1 go test -run TestLayoutAPIDocIsGenerated . 重新生成说明书", err)
		}
	}
	entries, err := os.ReadDir(layoutExampleDir)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, example := range layoutAPIExamples {
		listed[example.File+".json"] = true
	}
	for _, entry := range entries {
		if !listed[entry.Name()] {
			t.Errorf("%s/%s 不在 layoutAPIExamples 表里，删掉或登记", layoutExampleDir, entry.Name())
		}
	}
}

// TestLayoutAPIExamplesRegenerate 用线上（或本机）真接口重排每个例子，覆盖 testdata/layout-examples/*.json。
// 平时跳过；要更新例子时按 TestLayoutAPIExamplesExist 里写的方法跑。
func TestLayoutAPIExamplesRegenerate(t *testing.T) {
	base := strings.TrimRight(os.Getenv("LAYOUT_EXAMPLES_BASE"), "/")
	key := os.Getenv("LAYOUT_EXAMPLES_KEY")
	if base == "" || key == "" {
		t.Skip("没给 LAYOUT_EXAMPLES_BASE、LAYOUT_EXAMPLES_KEY，不重新生成例子")
	}
	client := &http.Client{Timeout: layoutClientTimeout}
	for _, example := range layoutAPIExamples {
		body, contentType, err := layoutExampleRequest(example)
		if err != nil {
			t.Fatalf("%s：%v", example.File, err)
		}
		req, _ := http.NewRequest(http.MethodPost, base+"/api/layout/v1/render", body)
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s：%v", example.File, err)
		}
		for resp.StatusCode == http.StatusAccepted {
			var pending layoutPendingJSON
			_ = json.NewDecoder(resp.Body).Decode(&pending)
			resp.Body.Close()
			poll, _ := http.NewRequest(http.MethodGet, base+pending.Operation.PollURL+"?wait_seconds=25", nil)
			poll.Header.Set("Authorization", "Bearer "+key)
			if resp, err = client.Do(poll); err != nil {
				t.Fatalf("%s：%v", example.File, err)
			}
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s：HTTP %d %s", example.File, resp.StatusCode, raw)
		}
		var result layoutResultJSON
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		result.RequestID = "lr_Q2xhdWRlRXhh"
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(result)
		if err := os.WriteFile(filepath.Join(layoutExampleDir, example.File+".json"), out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
	}
}

// layoutExampleSamples 重新生成例子时交的内容（链接和 Word 由环境变量给）。
var layoutExampleSamples = map[layoutInputKind]string{
	layoutInputMarkdown: "# 植物基饮料的下一个十年\n\n作者：李四\n\n这是一段**加粗的开头**，后面跟着普通文字。\n\n## 市场有多大\n\n2025 年植物基饮料零售额同比增长 12%。\n\n- 燕麦奶：份额第一\n- 豆奶：老牌品类\n",
	layoutInputText:     "植物基饮料的下一个十年\n\n这是第一段正文，讲市场有多大。\n\n这是第二段正文，讲品牌怎么做。",
	layoutInputHTML:     "<h1>植物基饮料的下一个十年</h1><p>这是第一段正文，讲市场有多大。</p><h2>品牌怎么做</h2><p>先做口味，再做渠道。</p>",
}

func layoutExampleRequest(example layoutAPIExample) (io.Reader, string, error) {
	switch example.Kind {
	case layoutInputWechatURL:
		url := os.Getenv("LAYOUT_EXAMPLES_WECHAT_URL")
		if url == "" {
			return nil, "", fmt.Errorf("要 LAYOUT_EXAMPLES_WECHAT_URL")
		}
		raw, _ := json.Marshal(map[string]any{"account": example.Account, "url": url})
		return bytes.NewReader(raw), "application/json", nil
	case layoutInputWord:
		path := os.Getenv("LAYOUT_EXAMPLES_DOCX")
		file, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("要 LAYOUT_EXAMPLES_DOCX：%w", err)
		}
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("account", example.Account)
		part, _ := mw.CreateFormFile("file", filepath.Base(path))
		_, _ = part.Write(file)
		_ = mw.Close()
		return &body, mw.FormDataContentType(), nil
	}
	sample, ok := layoutExampleSamples[example.Kind]
	if !ok {
		return nil, "", fmt.Errorf("没有 %s 的样稿", example.Kind)
	}
	field := layoutInputSpecFor(example.Kind).Field
	raw, _ := json.Marshal(map[string]any{"account": example.Account, field: sample})
	return bytes.NewReader(raw), "application/json", nil
}

func TestLayoutAPIExamplesCoverEveryAccount(t *testing.T) {
	var accounts []string
	for _, example := range layoutAPIExamples {
		accounts = append(accounts, example.Account)
	}
	sort.Strings(accounts)
	want := catalogOrderIDs()
	sort.Strings(want)
	if strings.Join(accounts, ",") != strings.Join(want, ",") {
		t.Fatalf("每个账号一个例子：%v %v", accounts, want)
	}
}
