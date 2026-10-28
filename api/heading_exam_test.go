package main

// 标题考卷（testdata/heading-exam，怎么收卷见 tools/heading-exam/README.md）：用户提过意见、编辑手改过小标题的
// 真文章，原文存成 <编号>.html.gz。这里把每篇原文抓成正文块，存在 <编号>.blocks.json；块一变测试就失败，
// 看过确认后用 GOLDEN_UPDATE=1 重写。网站排出来的小标题与编辑改定稿（<编号>.answer.txt）比，在 tests/heading-exam.test.mjs。

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const headingExamDir = "testdata/heading-exam"

// headingExamBlocks 是 Node 那一半的输入：和网页抓链接时交给排版的一样。
type headingExamBlocks struct {
	Title      string  `json:"title"`
	SourceName string  `json:"source_name"`
	Byline     string  `json:"byline"`
	Blocks     []Block `json:"blocks"`
}

func TestHeadingExamBlocks(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join(headingExamDir, "*.html.gz"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("考卷是空的：%v", err)
	}
	update := os.Getenv("GOLDEN_UPDATE") != ""
	for _, page := range pages {
		key := strings.TrimSuffix(filepath.Base(page), ".html.gz")
		t.Run(key, func(t *testing.T) {
			raw := readHeadingExamPage(t, page)
			title, blocks, _ := extractArticleBlocks(raw)
			nickname, byline, _ := extractWechatArticleMeta(raw)
			got := headingExamBlocks{Title: title, SourceName: nickname, Byline: byline, Blocks: blocks}
			encoded := encodeDistributionGolden(t, got)
			recordPath := filepath.Join(headingExamDir, key+".blocks.json")
			if update {
				if err := os.WriteFile(recordPath, encoded, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			recorded, err := os.ReadFile(recordPath)
			if err != nil {
				t.Fatalf("没有 %s：GOLDEN_UPDATE=1 go test -run TestHeadingExamBlocks 生成后看过再提交", recordPath)
			}
			if !bytes.Equal(recorded, encoded) {
				var before headingExamBlocks
				_ = json.Unmarshal(recorded, &before)
				t.Errorf("%s 抓出来的块变了：\n%s看过确认后用 GOLDEN_UPDATE=1 go test -run TestHeadingExamBlocks 重写，再跑 tests/heading-exam.test.mjs 看网站上的小标题。",
					key, headingExamChange(before.Blocks, got.Blocks))
			}
		})
	}
}

func readHeadingExamPage(t *testing.T, path string) string {
	t.Helper()
	gz, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// headingExamChange 列出哪些小标题没了、哪些是新的；别的块只报变了几块。
func headingExamChange(before, now []Block) string {
	headings := func(blocks []Block) []string {
		var out []string
		for _, b := range blocks {
			if b.K == "h" {
				out = append(out, fmt.Sprintf("h(level %d) %s", b.Level, blockPlainText(b)))
			}
		}
		return out
	}
	var out strings.Builder
	was, is := map[string]int{}, map[string]int{}
	for _, line := range headings(before) {
		was[line]++
	}
	for _, line := range headings(now) {
		is[line]++
	}
	for _, line := range headings(now) {
		if was[line] > 0 {
			was[line]--
			continue
		}
		out.WriteString("新出现的小标题：" + line + "\n")
	}
	for _, line := range headings(before) {
		if is[line] > 0 {
			is[line]--
			continue
		}
		out.WriteString("没了的小标题：" + line + "\n")
	}
	if out.Len() == 0 {
		fmt.Fprintf(&out, "小标题没变，别的块变了（原来 %d 块，现在 %d 块）\n", len(before), len(now))
	}
	return out.String()
}
