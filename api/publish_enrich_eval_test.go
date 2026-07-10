package main

// publish_enrich_eval_test.go — M7 准确率评测（docs/editorial-norms-foodtalks.md §7）。
// 用真实历史文章（tools/enrich-eval/eval_set.json，输入=标题+正文，期望=编辑部实际
// 发布参数）真调 ARK，走与生产逐字节相同的 arkEnrichArticle 链路，量化 AI 预填质量。
//
// 平时 CI 自动跳过（要花钱、要网络）；手动运行：
//
//	cd api && ENRICH_EVAL=1 ARK_API_KEY=... go test -run TestEnrichEval -v -timeout 30m
//
// 换模型回归：加 ARK_MODEL_ID=<新端点> 重跑，全部达标即可切换（prompt 与模型解耦，
// 不用改代码）。可用 ENRICH_EVAL_N=5 先抽样冒烟。
//
// 评测口径说明：
//   - 正文先剥掉 <img>（封面无历史真值可对——编辑部封面是 CMS 转存 URL，与候选
//     无从对应；剥图后模型走纯文本路径，省下 40 篇 × 8 张的多模态 token）。
//   - 摘要是创作不是抽取，没有唯一正确答案 → 只对「长度合规 + 风格合规」打分。
//   - 作者只对编辑部有填的样本计精确率（编辑部对转载文作者填写是宽松裁量，规范 §5）。
//   - 出处抽取只对「真值出处名确实出现在正文里」的转载样本计分（不在正文里的
//     无从抽取，不算失分）。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

type enrichEvalArticle struct {
	ID         int64    `json:"id"`
	Title      string   `json:"title"`
	SourceType int      `json:"source_type"`
	SourceName string   `json:"source_name"`
	Author     string   `json:"author"`
	Summary    string   `json:"summary"`
	Tags       []string `json:"tags"`
	Content    string   `json:"content"`
}

// 达标线（规范文档 §7；换模型时的通过/不通过裁决线）。
const (
	evalFloorSummaryLen   = 0.90
	evalFloorSummaryStyle = 0.90
	evalFloorTagCount     = 0.95
	evalFloorTagHit       = 0.60
	evalFloorAuthor       = 0.80
	evalFloorSource       = 0.70
)

var evalImgTagRe = regexp.MustCompile(`(?is)<img[^>]*>`)

// evalNorm 对比前归一：去首尾空白、拉丁字母折小写、去内部多余空格。
func evalNorm(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), ""))
}

func TestEnrichEvalAgainstEditorial(t *testing.T) {
	if os.Getenv("ENRICH_EVAL") == "" {
		t.Skip("评测需 ENRICH_EVAL=1（真调 ARK，花钱且慢）")
	}
	if strings.TrimSpace(os.Getenv(arkAPIKeyEnv)) == "" {
		t.Fatal("评测需 ARK_API_KEY")
	}
	raw, err := os.ReadFile("../tools/enrich-eval/eval_set.json")
	if err != nil {
		t.Fatalf("读评测集失败: %v", err)
	}
	var articles []enrichEvalArticle
	if err := json.Unmarshal(raw, &articles); err != nil {
		t.Fatalf("评测集不是合法 JSON: %v", err)
	}
	if n := 0; os.Getenv("ENRICH_EVAL_N") != "" {
		fmt.Sscanf(os.Getenv("ENRICH_EVAL_N"), "%d", &n)
		if n > 0 && n < len(articles) {
			articles = articles[:n]
		}
	}
	vocab := map[string]bool{}
	for _, v := range editorialTagVocab() {
		vocab[evalNorm(v.TagName)] = true
	}

	type evalResult struct {
		art enrichEvalArticle
		out publishEnrichment
		err error
	}
	results := make([]evalResult, len(articles))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // 并发 4：一轮 40 篇几分钟内跑完，又不至于触发限流
	for i, art := range articles {
		wg.Add(1)
		go func(i int, art enrichEvalArticle) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			job := publishJob{Title: art.Title, ContentHTML: evalImgTagRe.ReplaceAllString(art.Content, "")}
			out, err := arkEnrichArticle(context.Background(), job)
			results[i] = evalResult{art: art, out: out, err: err}
		}(i, art)
	}
	wg.Wait()

	var (
		total, apiFail                        int
		lenOK, styleOK, tagCountOK, tagHit    int
		authorTotal, authorOK, authorInvented int
		sourceTotal, sourceOK                 int
		tagPred, tagPredInVocab               int
	)
	bannedPrefixes := []string{"本文", "文章", "这篇", "该文", "本篇"}
	for _, r := range results {
		if r.err != nil {
			apiFail++
			t.Logf("✗ #%d %s ARK 失败: %v", r.art.ID, r.art.Title, r.err)
			continue
		}
		total++
		// 摘要长度：5~45 字（编辑部 avg 29、p84=40，放 5 字余量）。
		sumLen := utf8.RuneCountInString(r.out.Summary)
		thisLenOK := sumLen >= 5 && sumLen <= 45
		if thisLenOK {
			lenOK++
		}
		// 摘要风格：无禁用开头 + 至多两个句终标点（编辑部有双问句惯例）。
		thisStyleOK := true
		for _, p := range bannedPrefixes {
			if strings.HasPrefix(r.out.Summary, p) {
				thisStyleOK = false
			}
		}
		if strings.Count(r.out.Summary, "。")+strings.Count(r.out.Summary, "？")+strings.Count(r.out.Summary, "！") > 2 {
			thisStyleOK = false
		}
		if thisStyleOK {
			styleOK++
		}
		// 标签：数量 1~3；至少命中一个编辑部真实标签（有真值的样本）；词表率仅报告。
		if n := len(r.out.Tags); n >= 1 && n <= 3 {
			tagCountOK++
		}
		truthTags := map[string]bool{}
		for _, tg := range r.art.Tags {
			truthTags[evalNorm(tg)] = true
		}
		hit := false
		for _, tg := range r.out.Tags {
			tagPred++
			if vocab[evalNorm(tg)] {
				tagPredInVocab++
			}
			if truthTags[evalNorm(tg)] {
				hit = true
			}
		}
		if len(r.art.Tags) == 0 || hit {
			tagHit++ // 编辑部没打标签的样本不算失分
		}
		// 作者：只对「编辑部有填 **且** 真值确实出现在正文里」的样本计精确率——不在
		// 正文里的无从抽取（62411 实测：编辑从原文页面外部获知），与出处抽取同口径。
		// 匹配用「一方包含另一方」——编辑部自己对同一人就有「Mote莫特 / Mote / mote」
		// 三种写法（62496/62443 实测），AI 照抄正文原署名是对的，不能因编辑部缩写而
		// 判错。真值为空时统计编造量（红线）。
		if r.art.Author != "" && strings.Contains(evalNorm(r.art.Content), evalNorm(r.art.Author)) {
			authorTotal++
			gotA, wantA := evalNorm(r.out.Author), evalNorm(r.art.Author)
			if gotA != "" && (strings.Contains(gotA, wantA) || strings.Contains(wantA, gotA)) {
				authorOK++
			}
		} else if r.out.Author != "" && !strings.Contains(evalNorm(r.art.Content), evalNorm(r.out.Author)) {
			authorInvented++ // 正文里根本不存在的名字=幻觉编造
		}
		// 出处抽取：转载样本且真值出处名出现在正文里才计分。
		if r.art.SourceType == 1 && r.art.SourceName != "" &&
			strings.Contains(evalNorm(r.art.Content), evalNorm(r.art.SourceName)) {
			sourceTotal++
			if evalNorm(r.out.SourceName) == evalNorm(r.art.SourceName) {
				sourceOK++
			}
		}
		t.Logf("#%d 摘要(%d字 len=%v style=%v)=%q tags=%v(真值%v) author=%q(真值%q) source=%q",
			r.art.ID, sumLen, thisLenOK, thisStyleOK, r.out.Summary, r.out.Tags, r.art.Tags, r.out.Author, r.art.Author, r.out.SourceName)
	}
	if total == 0 {
		t.Fatalf("全部 %d 篇 ARK 调用失败，无法评测", apiFail)
	}
	rate := func(n, d int) float64 {
		if d == 0 {
			return 1
		}
		return float64(n) / float64(d)
	}
	t.Logf("========== 评测汇总（模型 %s，样本 %d，API 失败 %d）==========", arkModelID(), total, apiFail)
	t.Logf("摘要长度合规  %d/%d = %.0f%%（达标线 %.0f%%）", lenOK, total, rate(lenOK, total)*100, evalFloorSummaryLen*100)
	t.Logf("摘要风格合规  %d/%d = %.0f%%（达标线 %.0f%%）", styleOK, total, rate(styleOK, total)*100, evalFloorSummaryStyle*100)
	t.Logf("标签数量合规  %d/%d = %.0f%%（达标线 %.0f%%）", tagCountOK, total, rate(tagCountOK, total)*100, evalFloorTagCount*100)
	t.Logf("标签命中真值  %d/%d = %.0f%%（达标线 %.0f%%）", tagHit, total, rate(tagHit, total)*100, evalFloorTagHit*100)
	t.Logf("标签词表率    %d/%d = %.0f%%（仅报告）", tagPredInVocab, tagPred, rate(tagPredInVocab, tagPred)*100)
	t.Logf("作者精确率    %d/%d = %.0f%%（达标线 %.0f%%；编造 %d 例）", authorOK, authorTotal, rate(authorOK, authorTotal)*100, evalFloorAuthor*100, authorInvented)
	t.Logf("出处抽取精确  %d/%d = %.0f%%（达标线 %.0f%%）", sourceOK, sourceTotal, rate(sourceOK, sourceTotal)*100, evalFloorSource*100)
	type gate struct {
		name  string
		got   float64
		floor float64
	}
	// 标签命中不做门禁（仅上方报告）：2026-07-10 后端反馈标签走单独接口、发布先不
	// 标注（契约 §9）；且编辑部真值混有「FBIF2026会后报告」这类人工栏目标签，AI 按
	// 规范本就不该产出。等标签接口接入时再回升为门禁。
	for _, g := range []gate{
		{"摘要长度", rate(lenOK, total), evalFloorSummaryLen},
		{"摘要风格", rate(styleOK, total), evalFloorSummaryStyle},
		{"标签数量", rate(tagCountOK, total), evalFloorTagCount},
		{"作者精确", rate(authorOK, authorTotal), evalFloorAuthor},
		{"出处抽取", rate(sourceOK, sourceTotal), evalFloorSource},
	} {
		if g.got < g.floor {
			t.Errorf("未达标：%s %.0f%% < %.0f%%", g.name, g.got*100, g.floor*100)
		}
	}
	if authorInvented > 0 {
		t.Errorf("作者幻觉编造 %d 例（红线：宁空勿造）", authorInvented)
	}
}
