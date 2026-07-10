package main

import (
	"strings"
	"testing"
)

// TestEditorialPromptAssembly M7 prompt 组装守护：词表占位符必须被真实标签名替换
// （占位符残留=模型收到一份没有词表的规范，标签质量静默劣化——fail loud）。
func TestEditorialPromptAssembly(t *testing.T) {
	prompt := editorialEnrichSystemPrompt()
	if strings.Contains(prompt, "{{VOCAB}}") {
		t.Fatal("prompt 里残留 {{VOCAB}} 占位符，词表未注入")
	}
	// 词表头部高频词（近一年 cnt 最高的通用品类词）必须在场。
	for _, want := range []string{"饮料", "零食", "简体中文", "1~3 个"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt 缺关键内容 %q", want)
		}
	}
	vocab := editorialTagVocab()
	if len(vocab) < 100 {
		t.Fatalf("词表条数异常：%d", len(vocab))
	}
	for _, v := range vocab[:10] {
		if v.TagID <= 0 || v.TagName == "" {
			t.Fatalf("词表条目缺 tag_id/tag_name：%+v", v)
		}
	}
}
