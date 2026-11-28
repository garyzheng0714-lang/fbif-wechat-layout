package main

// flash_writer.go — 照规范写快讯、对原文、查不推荐说法。
//
// 模型分工照仓库规矩（api/ai_models.go）：写和对原文都是高价值的活，用 pro；风险检查沿用发布线的 aiAssessRiskText。
// 交给模型的只有两样：本仓库写的任务说明（editorial/flash_writer.md、flash_verify.md，只讲拿到什么、怎么交），
// 和每次现取的飞书规范全文。怎么写一律以规范为准。
//
// 一篇最多调 4 次写稿（写、改格式、改对不上原文的、再改一次）和 2 次核对；改完还对不上原文的说法留在卡上给人看。

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

//go:embed editorial/flash_writer.md
var flashWriterTask string

//go:embed editorial/flash_verify.md
var flashVerifyTask string

const (
	// 规范说正文约 200–300 字、宁可略短；差得太多才让模型重写，不卡死在字数上。
	flashBodyMinRunes = 150 // 规范：约 200–300 字，宁可略短
	flashBodyMaxRunes = 330 // 约 300 字，多一成就让模型压
	flashTitleMaxRune = 50
)

// flashAI 调模型；生产是 MiMo pro，测试换成假的。
type flashAI func(ctx context.Context, system, user string) (string, error)

func mimoFlashAI(ctx context.Context, system, user string) (string, error) {
	return aiResponsesCall(ctx, aiModel(aiTierPro), system, []map[string]any{{"type": "input_text", "text": user}})
}

type flashWriteInput struct {
	Article       flashSourceArticle      `json:"article"`
	Translation   *flashSourceTranslation `json:"translation,omitempty"`
	Earliest      *flashWriteEarliest     `json:"earliest,omitempty"`
	OtherReports  []flashSourceReport     `json:"other_reports,omitempty"`
	JudgmentFlags []string                `json:"judgment_flags,omitempty"`
	SiteRecent    []string                `json:"site_recent,omitempty"`
	Today         string                  `json:"today"`
}

type flashWriteEarliest struct {
	Article     flashSourceArticle      `json:"article"`
	Translation *flashSourceTranslation `json:"translation,omitempty"`
}

type flashWriteOutput struct {
	Skip        string           `json:"skip"`
	DuplicateOf string           `json:"duplicate_of"`
	Title       string           `json:"title"`
	Body        []string         `json:"body"`
	Source      flashDraftSource `json:"source"`
	Hints       []string         `json:"hints"`
}

// flashWriteResult 写稿一轮的结果：要么 Skip/DuplicateOf，要么 Draft 加核对结果。
type flashWriteResult struct {
	Skip        string
	DuplicateOf string
	Draft       flashDraft
	Unsupported []string
	Avoid       []string
}

type flashWriter struct {
	ai  flashAI
	now func() time.Time
}

func flashSystemPrompt(task string, spec flashSpec) string {
	return strings.TrimSpace(task) + fmt.Sprintf("\n\n---\n\n以下是《FoodTalks网站快讯 AI编辑规范》（飞书文档第 %d 版）全文：\n\n", spec.Revision) + spec.Text
}

func (w flashWriter) input(src flashSourceDoc, earliest *flashSourceDoc, siteRecent []string) flashWriteInput {
	in := flashWriteInput{Article: src.Article, Translation: src.Translation, SiteRecent: siteRecent,
		Today: w.now().In(beijing).Format("2006年1月2日")}
	if src.Judgment != nil {
		in.JudgmentFlags = src.Judgment.Flags
	}
	if src.Event != nil {
		in.OtherReports = src.Event.Reports
	}
	if earliest != nil {
		in.Earliest = &flashWriteEarliest{Article: earliest.Article, Translation: earliest.Translation}
	}
	return in
}

// write 写一篇：写 → 格式和不推荐说法不对就改一次 → 对原文，对不上就改，最多改两次。
func (w flashWriter) write(ctx context.Context, spec flashSpec, src flashSourceDoc, earliest *flashSourceDoc, siteRecent []string) (flashWriteResult, error) {
	system := flashSystemPrompt(flashWriterTask, spec)
	inputJSON, _ := json.MarshalIndent(w.input(src, earliest, siteRecent), "", " ")
	user := string(inputJSON)
	raw, err := w.ai(ctx, system, user)
	if err != nil {
		return flashWriteResult{}, fmt.Errorf("写稿：%w", err)
	}
	out, err := parseFlashWriteOutput(raw)
	if err != nil {
		// 回的不是 JSON：带着原话让它重交一次。
		raw, err = w.ai(ctx, system, user+"\n\n你上次的回复不是要求的 JSON（"+err.Error()+"），请只输出那个 JSON 对象。")
		if err != nil {
			return flashWriteResult{}, fmt.Errorf("写稿：%w", err)
		}
		if out, err = parseFlashWriteOutput(raw); err != nil {
			return flashWriteResult{}, fmt.Errorf("写稿回复两次都不是要求的格式：%w", err)
		}
	}
	if out.Skip != "" || out.DuplicateOf != "" {
		return flashWriteResult{Skip: out.Skip, DuplicateOf: out.DuplicateOf}, nil
	}
	if problems := flashFormatProblems(out, spec); len(problems) > 0 {
		if fixed, err := w.revise(ctx, system, user, out, "这一版有这些问题，请改好后重新输出完整 JSON：", problems); err == nil {
			out = fixed
		}
	}
	draft := flashDraftFromOutput(out, src, earliest)
	unsupported, err := w.verify(ctx, spec, draft, src, earliest)
	if err != nil {
		return flashWriteResult{}, fmt.Errorf("对原文：%w", err)
	}
	for round := 0; round < 2 && len(unsupported) > 0; round++ {
		fixed, err := w.revise(ctx, system, user, out, "核对编辑对照原文，发现下面这些说法原文里找不到依据或和原文不一致。请删掉或改成原文的说法（不要换个说法保留），重新输出完整 JSON：", unsupported)
		if err != nil {
			break
		}
		out = fixed
		draft = flashDraftFromOutput(out, src, earliest)
		if unsupported, err = w.verify(ctx, spec, draft, src, earliest); err != nil {
			return flashWriteResult{}, fmt.Errorf("对原文：%w", err)
		}
	}
	return flashWriteResult{Draft: draft, Unsupported: unsupported, Avoid: flashAvoidHits(draft, spec)}, nil
}

func (w flashWriter) revise(ctx context.Context, system, user string, prev flashWriteOutput, lead string, problems []string) (flashWriteOutput, error) {
	prevJSON, _ := json.Marshal(prev)
	msg := user + "\n\n你上一版的稿子：\n" + string(prevJSON) + "\n\n" + lead + "\n- " + strings.Join(problems, "\n- ")
	raw, err := w.ai(ctx, system, msg)
	if err != nil {
		return prev, err
	}
	out, err := parseFlashWriteOutput(raw)
	if err != nil || out.Skip != "" || out.DuplicateOf != "" || out.Title == "" {
		return prev, errors.New("改稿回复不可用")
	}
	return out, nil
}

// verify 对原文：交回原文里找不到依据的说法（每条「原话：原因」）。
func (w flashWriter) verify(ctx context.Context, spec flashSpec, draft flashDraft, src flashSourceDoc, earliest *flashSourceDoc) ([]string, error) {
	sources := []flashWriteEarliest{{Article: src.Article, Translation: src.Translation}}
	if earliest != nil {
		sources = append(sources, flashWriteEarliest{Article: earliest.Article, Translation: earliest.Translation})
	}
	payload, _ := json.MarshalIndent(map[string]any{
		"flash":   map[string]any{"title": draft.Title, "body": draft.Body, "source": draft.Source},
		"sources": sources,
	}, "", " ")
	raw, err := w.ai(ctx, strings.TrimSpace(flashVerifyTask), string(payload))
	if err != nil {
		return nil, err
	}
	var out struct {
		Unsupported []struct {
			Quote string `json:"quote"`
			Why   string `json:"why"`
		} `json:"unsupported"`
	}
	if err := decodeFlashJSON(raw, &out); err != nil {
		return nil, fmt.Errorf("核对回复不是要求的格式：%w", err)
	}
	text := draft.Title + "\n" + strings.Join(draft.Body, "\n")
	var items []string
	for _, u := range out.Unsupported {
		quote := strings.TrimSpace(u.Quote)
		why := strings.TrimSpace(u.Why)
		if quote == "" {
			continue
		}
		// 引的话不在稿子里（模型自己编的）就不算，照发布线风险检查的同一个道理（groundPublishRiskEvidence）。
		if !strings.Contains(text, quote) {
			continue
		}
		items = append(items, "「"+quote+"」："+why)
	}
	return items, nil
}

func parseFlashWriteOutput(raw string) (flashWriteOutput, error) {
	var out flashWriteOutput
	if err := decodeFlashJSON(raw, &out); err != nil {
		return out, err
	}
	out.Skip = strings.TrimSpace(out.Skip)
	out.DuplicateOf = strings.TrimSpace(out.DuplicateOf)
	out.Title = strings.TrimSpace(out.Title)
	var body []string
	for _, p := range out.Body {
		if p = strings.TrimSpace(p); p != "" {
			body = append(body, p)
		}
	}
	out.Body = body
	var hints []string
	for _, h := range out.Hints {
		if h = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(h), "-•·")); h != "" {
			hints = append(hints, h)
		}
	}
	out.Hints = hints
	if out.Skip == "" && out.DuplicateOf == "" && (out.Title == "" || len(out.Body) == 0) {
		return out, errors.New("没有标题或正文")
	}
	return out, nil
}

// decodeFlashJSON 模型回复里取第一个 JSON 对象（可能包着 ``` 或前后有字）。
func decodeFlashJSON(raw string, out any) error {
	s := stripMarkdownFence(raw)
	if start, end := strings.Index(s, "{"), strings.LastIndex(s, "}"); start >= 0 && end > start {
		s = s[start : end+1]
	}
	return json.Unmarshal([]byte(s), out)
}

// flashBodyRunes 正文字数，照编辑平时的数法（Word 的“字数”）：汉字、标点一个算一个，
// 英文单词、数字连着的一串算一个，空白不算；标题不计（规范）。2026-11-29 试跑：按字母数时外文稿多算三成。
func flashBodyRunes(body []string) int {
	n := 0
	for _, p := range body {
		inWord := false
		for _, r := range p {
			switch {
			case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
				if !inWord {
					n++
				}
				inWord = true
			case inWord && r < 0x80 && strings.ContainsRune(".,'-&%", r):
				// 3.9%、Nestlé's、Coca-Cola 里的符号连在词里，不另算
			case unicode.IsSpace(r):
				inWord = false
			default:
				inWord = false
				n++
			}
		}
	}
	return n
}

// flashShareableURL 公众号长链只留打得开的四个参数（__biz、mid、idx、sn），换成 https；别的链接原样。
func flashShareableURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isWechatArticleURL(raw) || u.Path != "/s" {
		return strings.TrimSpace(raw)
	}
	q := u.Query()
	keep := url.Values{}
	for _, k := range []string{"__biz", "mid", "idx", "sn"} {
		if v := q.Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	if keep.Get("__biz") == "" || keep.Get("mid") == "" || keep.Get("sn") == "" {
		return strings.TrimSpace(raw)
	}
	return "https://mp.weixin.qq.com/s?__biz=" + url.QueryEscape(keep.Get("__biz")) + "&mid=" + url.QueryEscape(keep.Get("mid")) +
		"&idx=" + url.QueryEscape(keep.Get("idx")) + "&sn=" + url.QueryEscape(keep.Get("sn"))
}

func flashFormatProblems(out flashWriteOutput, spec flashSpec) []string {
	var problems []string
	if n := len(out.Body); n < 2 || n > 3 {
		problems = append(problems, fmt.Sprintf("正文要 2 到 3 段，这一版是 %d 段", n))
	}
	if n := flashBodyRunes(out.Body); n < flashBodyMinRunes || n > flashBodyMaxRunes {
		problems = append(problems, fmt.Sprintf("正文约 200 到 300 字（汉字、标点一个算一个，英文单词、数字连着的一串算一个），这一版 %d 字", n))
	}
	if n := len([]rune(out.Title)); n > flashTitleMaxRune {
		problems = append(problems, fmt.Sprintf("标题 %d 个字，太长", n))
	}
	draft := flashDraft{Title: out.Title, Body: out.Body}
	for _, hit := range flashAvoidHits(draft, spec) {
		problems = append(problems, "用了规范里不推荐的说法："+hit)
	}
	return problems
}

// flashAvoidHits 稿子里出现的规范表格「不推荐」说法（带上表里的改法）。
func flashAvoidHits(d flashDraft, spec flashSpec) []string {
	text := d.Title + "\n" + strings.Join(d.Body, "\n")
	var hits []string
	seen := map[string]bool{}
	for _, p := range spec.Avoid {
		if p.Avoid == "" || seen[p.Avoid] || !strings.Contains(text, p.Avoid) {
			continue
		}
		seen[p.Avoid] = true
		if p.Instead != "" {
			hits = append(hits, "「"+p.Avoid+"」（规范的改法："+p.Instead+"）")
		} else {
			hits = append(hits, "「"+p.Avoid+"」")
		}
	}
	return hits
}

// flashDraftFromOutput 模型的稿子 → 存下的一版。来源只认资讯订阅给的两篇：链接对不上就按名称认，都对不上用这篇自己的。
func flashDraftFromOutput(out flashWriteOutput, src flashSourceDoc, earliest *flashSourceDoc) flashDraft {
	pick := src.Article
	if earliest != nil && (out.Source.URL == earliest.Article.URL ||
		(out.Source.URL != src.Article.URL && strings.TrimSpace(out.Source.Name) == strings.TrimSpace(earliest.Article.SourceName))) {
		pick = earliest.Article
	}
	return flashDraft{
		Title: out.Title,
		Body:  out.Body,
		Source: flashDraftSource{
			Name:        pick.SourceName,
			Title:       pick.Title,
			URL:         flashShareableURL(pick.URL),
			PublishedAt: formatFlashSourceTime(pick.PublishedAt),
		},
		Hints: out.Hints,
	}
}

// formatFlashSourceTime 2026-11-28T10:03:00+08:00 → 2026-11-28 10:03（北京时间）；认不出原样留着。
func formatFlashSourceTime(raw string) string {
	t, ok := parseFlashTime(raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	return t.In(beijing).Format("2006-01-02 15:04")
}
