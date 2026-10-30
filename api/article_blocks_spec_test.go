package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	articleBlocksDocPath    = "../docs/article-blocks.md"
	articleBlocksSchemaPath = "../docs/article-blocks.schema.json"
)

// articleBlocksExample 说明书里的例子：一小段文章抽出来的样子。
var articleBlocksExample = []Block{
	{K: "txt", Runs: []Run{{Type: "txt", Text: "作者：Sage"}}},
	{K: "txt", Runs: []Run{{Type: "txt", Text: "今年的新品里，"}, {Type: "txt", Text: "苦味", Bold: true}, {Type: "txt", Text: "成了关键词。"}}},
	{K: "h", Level: 2, Text: "一、苦味升级", BlankBefore: true},
	{K: "img", Src: "https://mmbiz.qpic.cn/example.jpg", DataW: "1080", DataH: "720", DataRatio: "0.6667"},
	{K: "cap", Text: "▲ 某品牌的苦味气泡水"},
	{K: "cap", Text: "图片来源：某品牌官网"},
	{K: "txt", Runs: []Run{{Type: "txt", Text: "整段加粗的一句话。", Bold: true}}},
	{K: "txt", Runs: []Run{{Type: "txt", Text: "详见"}, {Type: "link", Text: "品牌官网", Href: "https://example.com"}, {Type: "txt", Text: "。"}}},
}

// 块的定义、种类表和两份生成的说明书三者一致；SPEC_UPDATE=1 重写说明书。
func TestArticleBlockSpec(t *testing.T) {
	fields := articleBlockJSONFields(reflect.TypeOf(Block{}))
	used := map[string]bool{}
	seen := map[string]bool{}
	for _, kind := range articleBlockKinds {
		if seen[kind.K] {
			t.Errorf("块种类 %s 写了两遍", kind.K)
		}
		seen[kind.K] = true
		for _, f := range kind.Fields {
			if _, ok := fields[f]; !ok || f == "k" {
				t.Errorf("块种类 %s 列了 Block 上没有的字段 %s", kind.K, f)
			}
			used[f] = true
		}
	}
	for name := range fields {
		if name != "k" && !used[name] {
			t.Errorf("Block 的字段 %s 没有哪种块用到：在 articleBlockKinds 里写明哪种块用它", name)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(Block{}), reflect.TypeOf(Run{}), reflect.TypeOf(TableCell{}), reflect.TypeOf(articleBlocksResponse{}), reflect.TypeOf(articleProvenance{})} {
		for name, field := range articleBlockJSONFields(typ) {
			if field.Tag.Get("doc") == "" {
				t.Errorf("%s.%s（%s）没写 doc 标签", typ.Name(), field.Name, name)
			}
		}
	}
	for i, block := range articleBlocksExample {
		if err := checkArticleBlock(block); err != nil {
			t.Errorf("说明书例子第 %d 块：%v", i+1, err)
		}
	}
	checkGeneratedSpecFile(t, articleBlocksDocPath, renderArticleBlocksDoc())
	checkGeneratedSpecFile(t, articleBlocksSchemaPath, renderArticleBlocksSchema())
}

// 样稿考卷里 6 篇真稿的 FoodTalks 正文抽出来的块都照说明书。
func TestArticleBlocksFromRealArticlesFollowSpec(t *testing.T) {
	files, err := filepath.Glob("testdata/distribution-golden/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("没找到样稿：%v", err)
	}
	for _, file := range files {
		var sample distributionGoldenSample
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &sample); err != nil {
			t.Fatal(err)
		}
		_, blocks, _ := extractArticleBlocks(sample.News.Content)
		for i, block := range blocks {
			if err := checkArticleBlock(block); err != nil {
				t.Errorf("%s 第 %d 块：%v", filepath.Base(file), i+1, err)
			}
		}
	}
}

func checkArticleBlock(block Block) error {
	var kind *articleBlockKind
	for i := range articleBlockKinds {
		if articleBlockKinds[i].K == block.K {
			kind = &articleBlockKinds[i]
		}
	}
	if kind == nil {
		return fmt.Errorf("块种类 %q 不在说明书里", block.K)
	}
	raw, _ := json.Marshal(block)
	var present map[string]any
	_ = json.Unmarshal(raw, &present)
	for name := range present {
		if name != "k" && !containsString(kind.Fields, name) {
			return fmt.Errorf("%s 块不该有字段 %s", block.K, name)
		}
	}
	return nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func articleBlockJSONFields(typ reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		out[strings.Split(field.Tag.Get("json"), ",")[0]] = field
	}
	return out
}

func orderedJSONFields(typ reflect.Type) []reflect.StructField {
	out := make([]reflect.StructField, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		out = append(out, typ.Field(i))
	}
	return out
}

func jsonName(field reflect.StructField) string {
	return strings.Split(field.Tag.Get("json"), ",")[0]
}

func checkGeneratedSpecFile(t *testing.T, path, want string) {
	t.Helper()
	if os.Getenv("SPEC_UPDATE") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s 和代码对不上（%v）：改完代码跑 cd api && SPEC_UPDATE=1 go test -run 'TestArticleBlockSpec|TestDistributionRulesDoc' . 重新生成", path, err)
	}
}

func goTypeLabel(typ reflect.Type) string {
	switch typ {
	case reflect.TypeOf(""):
		return "文字"
	case reflect.TypeOf(0):
		return "整数"
	case reflect.TypeOf(false):
		return "是/否"
	case reflect.TypeOf([]Run{}):
		return "片段列表"
	case reflect.TypeOf([][]TableCell{}):
		return "行列表（每行是单元格列表）"
	case reflect.TypeOf([]Block{}):
		return "块列表"
	case reflect.TypeOf(&articleProvenance{}):
		return "来源判定（见下）"
	}
	return typ.String()
}

func renderArticleBlocksDoc() string {
	var b strings.Builder
	b.WriteString("# 文章正文块（格式说明书）\n\n")
	b.WriteString("> 由 `api/article_blocks.go` 生成，别手改。改了块的定义跑 `cd api && SPEC_UPDATE=1 go test -run TestArticleBlockSpec .`。程序校验用同目录的 `article-blocks.schema.json`。\n\n")
	b.WriteString("一篇文章抽出来是一串块，从上到下就是读者看到的顺序。公众号网址、供应商网页、FoodTalks 已发布稿都抽成这样的块，公众号、FoodTalks、头条号、百家号的排版都读它；浏览器里粘贴纯文本也产出同样的块。Word、Markdown 在浏览器里各自分类，不经块。外发第三方平台时先整理成外发通用版正文块（[distribution-blocks.md](distribution-blocks.md)），每种块在各平台写成什么标签见分发服务的 `docs/platforms/`。\n\n")
	b.WriteString("## 块的种类\n\n| k | 叫法 | 是什么 | 用到的字段 |\n| --- | --- | --- | --- |\n")
	for _, kind := range articleBlockKinds {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", kind.K, kind.Name, kind.Meaning, "`"+strings.Join(kind.Fields, "` `")+"`")
	}
	b.WriteString("\n## 块的字段\n\n")
	writeFieldTable(&b, reflect.TypeOf(Block{}))
	b.WriteString("\n### 片段（runs 里的每一项）\n\n")
	writeFieldTable(&b, reflect.TypeOf(Run{}))
	fmt.Fprintf(&b, "\n片段的 type 只有：`%s`。\n", strings.Join(articleRunTypes, "` `"))
	b.WriteString("\n### 表格单元格（rows 里的每一格）\n\n")
	writeFieldTable(&b, reflect.TypeOf(TableCell{}))
	b.WriteString("\n## 块外面的字段\n\n抓取接口 `POST /api/fetch-article` 抓到正文时返回：\n\n")
	writeFieldTable(&b, reflect.TypeOf(articleBlocksResponse{}))
	b.WriteString("\n### 来源判定（provenance）\n\n")
	writeFieldTable(&b, reflect.TypeOf(articleProvenance{}))
	b.WriteString("\n## 旧写法\n\n2026-07 以前的稿子里块种类有旧写法，读的时候换成现在的，新代码不许再产出：\n\n")
	legacy := make([]string, 0, len(articleBlockLegacyKinds))
	for old := range articleBlockLegacyKinds {
		legacy = append(legacy, old)
	}
	sort.Strings(legacy)
	for _, old := range legacy {
		fmt.Fprintf(&b, "- `%s` 当 `%s`\n", old, articleBlockLegacyKinds[old])
	}
	b.WriteString("\n## 例子\n\n开头一行署名、一段带局部加粗的话、一个小标题、一张图带两条图注（说明和图片来源）、一段整段加粗、一段带链接：\n\n```json\n")
	b.WriteString(indentedJSON(articleBlocksExample))
	b.WriteString("\n```\n")
	return b.String()
}

func writeFieldTable(b *strings.Builder, typ reflect.Type) {
	b.WriteString("| 字段 | 类型 | 意思 |\n| --- | --- | --- |\n")
	for _, field := range orderedJSONFields(typ) {
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", jsonName(field), goTypeLabel(field.Type), field.Tag.Get("doc"))
	}
}

func indentedJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return strings.TrimRight(buf.String(), "\n")
}

func schemaForType(typ reflect.Type) map[string]any {
	switch typ {
	case reflect.TypeOf(""):
		return map[string]any{"type": "string"}
	case reflect.TypeOf(0):
		return map[string]any{"type": "integer"}
	case reflect.TypeOf(false):
		return map[string]any{"type": "boolean"}
	case reflect.TypeOf([]Run{}):
		return map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/run"}}
	case reflect.TypeOf([][]TableCell{}):
		return map[string]any{"type": "array", "items": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/tableCell"}}}
	}
	panic("说明书不认识的字段类型 " + typ.String())
}

func schemaObject(typ reflect.Type, only []string) map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, field := range orderedJSONFields(typ) {
		name := jsonName(field)
		if only != nil && name != "k" && !containsString(only, name) {
			continue
		}
		prop := schemaForType(field.Type)
		prop["description"] = field.Tag.Get("doc")
		props[name] = prop
		if !strings.Contains(field.Tag.Get("json"), "omitempty") {
			required = append(required, name)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func renderArticleBlocksSchema() string {
	variants := make([]any, 0, len(articleBlockKinds))
	for _, kind := range articleBlockKinds {
		obj := schemaObject(reflect.TypeOf(Block{}), kind.Fields)
		props := obj["properties"].(map[string]any)
		props["k"] = map[string]any{"const": kind.K, "description": kind.Name}
		obj["title"] = kind.Name
		obj["description"] = kind.Meaning
		variants = append(variants, obj)
	}
	run := schemaObject(reflect.TypeOf(Run{}), nil)
	run["properties"].(map[string]any)["type"].(map[string]any)["enum"] = articleRunTypes
	schema := map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "article-blocks.schema.json",
		"title":       "文章正文块",
		"description": "由 api/article_blocks.go 生成，别手改；说明见 article-blocks.md。",
		"type":        "array",
		"items":       map[string]any{"$ref": "#/$defs/block"},
		"$defs": map[string]any{
			"block":     map[string]any{"oneOf": variants},
			"run":       run,
			"tableCell": schemaObject(reflect.TypeOf(TableCell{}), nil),
		},
	}
	return indentedJSON(schema) + "\n"
}
