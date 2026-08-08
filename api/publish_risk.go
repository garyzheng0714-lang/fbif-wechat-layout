package main

// publish_risk.go — 发布前红线风险提示。它只生成结构化提示，不改正文，也不替代人工
// 审核；解析失败必须降级 warning 并保留原始返回，不能把“模型没看懂”误当 pass。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const (
	publishRiskPass    = "pass"
	publishRiskWarning = "warning"
	publishRiskBlock   = "block"

	publishRiskCategoryFactual       = "factual"
	publishRiskCategoryEfficacy      = "efficacy"
	publishRiskCategoryRepublication = "republication"
	publishRiskCategoryCompetitor    = "competitor"
	publishRiskCategoryDataSource    = "data_source"
	publishRiskCategorySystem        = "system"

	// certainty 决定这条提示出现在审核卡的哪一层（2026-08-08 用户裁定）：
	// confirmed = 能凭常识或正文自身矛盾指认的实质性错误，直接推到卡片首屏；
	// unverified = 存疑但证明不了，收进折叠的完整报告，不占审核人注意力。
	// 模型拿不准一律 unverified；缺失/未知值按 unverified 处理（fail-soft 到折叠层，
	// 但首屏文案不会因此谎称「检查通过」，见 reviewCardHeadlineBlock）。
	publishRiskCertaintyConfirmed  = "confirmed"
	publishRiskCertaintyUnverified = "unverified"
)

type publishRiskItem struct {
	Category  string `json:"category"`
	Level     string `json:"level"`
	Certainty string `json:"certainty,omitempty"`
	Evidence  string `json:"evidence"`
	Reason    string `json:"reason"`
}

type publishRiskAssessment struct {
	Level string `json:"level"`
	// Headline 给审核人的一句话结论（≤100 字），卡片首屏唯一的长文本。
	Headline string            `json:"headline,omitempty"`
	Items    []publishRiskItem `json:"items"`
	Notes    string            `json:"notes,omitempty"`
	Raw      string            `json:"raw,omitempty"`
}

// publishRiskHeadlineMaxRunes 首屏一句话结论的硬上限（用户裁定 100 字内）。
const publishRiskHeadlineMaxRunes = 100

// publishRiskItemConfirmed 该条是否属于「已证实的实质性问题」——只有这类才上首屏。
// 转载授权无论确不确定都归档到完整报告（用户裁定：那些不太需要知道）；
// 风险检查异常（system）是流程状态不是文章问题，也不上首屏。
func publishRiskItemConfirmed(item publishRiskItem) bool {
	switch item.Category {
	case publishRiskCategoryRepublication, publishRiskCategorySystem:
		return false
	}
	return item.Certainty == publishRiskCertaintyConfirmed
}

// splitPublishRiskItems 把风险条目分成「首屏必看」与「完整报告」两层。
func splitPublishRiskItems(items []publishRiskItem) (confirmed, deferred []publishRiskItem) {
	for _, item := range items {
		if publishRiskItemConfirmed(item) {
			confirmed = append(confirmed, item)
			continue
		}
		deferred = append(deferred, item)
	}
	return confirmed, deferred
}

func publishRiskSystemPrompt() string {
	return `你是 FoodTalks 发布前风险审核助手。请用简体中文，只输出 JSON，不要 markdown。
你只做提示与分级，不拥有发布决策权，不得改写正文。

审核编辑最需要你抓出来的是**文章里写错了的事实**，其次才是合规红线。一篇文章十有八九没有实质性错误，那就干脆地说没有——不要为了显得尽责而把常规现象包装成问题。

检查五类：
1. 事实与常识 factual（最高优先级）：年份、日期、地点、数字、人名、公司名、机构名、职务、事件经过、单位换算，是否与公认常识相矛盾，或与本文自身前后矛盾。例：正文写「2018 年的北京冬奥会」，而北京冬奥会实际在 2022 年举办。这类错误必须报。
2. 功效表达 efficacy：食品健康、治疗、减肥、改善身体等承诺是否缺证据或可能违法。
3. 转载授权 republication：是否无法确认转载许可、原作者或原出处。
4. 竞品评价 competitor：是否含无证据的贬损、排名、比较或结论性评价。
5. 数据来源 data_source：关键数字、市场份额、销量、研究结论是否缺明确来源。

每一条都必须标 certainty，它决定这条会出现在审核卡的哪一层：
- confirmed：你能凭公认常识或正文自身的前后矛盾**直接指认它是错的**，并说得出正确的应该是什么。这类会直接推到审核人眼前。
- unverified：你觉得可疑，但凭手上的正文和常识证明不了，只能提醒人去核。这类收进折叠的完整报告。
拿不准一律填 unverified。宁可漏一条 confirmed，也不许把猜测标成 confirmed。
「文中数据没写来源」「未标注转载授权」这类**普遍现象**永远是 unverified，除非正文自身就自相矛盾。

headline：给审核人的一句话结论，**不超过 100 个字**，用大白话直接说事，不要「经检查」「本文」这类套话。
- 有 confirmed 条目时：只讲最要紧的那一条，写清错在哪里、正确的是什么。例：「正文把北京冬奥会写成 2018 年举办，实际是 2022 年。」
- 没有 confirmed 条目时：写「未发现可证实的实质性错误」，可再补一句还有几条待核提示。

输出：{"level":"pass|warning|block","headline":"...","items":[{"category":"factual|efficacy|republication|competitor|data_source","level":"warning|block","certainty":"confirmed|unverified","evidence":"正文原句","reason":"为什么需要人工处理；confirmed 的必须写清正确的应该是什么"}],"notes":"给审核人的简短补充说明"}
规则：pass 时 items 必须为空；有疑点至少 warning；存在明显违法/授权硬缺口才 block；证据必须引用输入正文，禁止编造。`
}

// arkAssessPublishRisk 调用与元数据预填相同的 ARK Responses API。返回的 Raw 仅在
// 解析失败时供 fallback 留痕，成功结果不保存整段模型响应。
func arkAssessPublishRisk(ctx context.Context, job publishJob) (publishRiskAssessment, error) {
	apiKey := strings.TrimSpace(os.Getenv(arkAPIKeyEnv))
	if apiKey == "" {
		return publishRiskAssessment{}, errors.New("ARK_API_KEY 未配置")
	}
	bodyText := truncateRunes(htmlToPlainText(job.ContentHTML), 8000)
	payload, err := json.Marshal(map[string]any{
		"model": arkModelID(),
		"input": []any{
			map[string]any{"role": "system", "content": []map[string]any{{"type": "input_text", "text": publishRiskSystemPrompt()}}},
			map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": "文章标题：" + job.Title + "\n\n正文：\n" + bodyText}}},
		},
	})
	if err != nil {
		return publishRiskAssessment{}, fmt.Errorf("序列化风险审核请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, arkBaseURL()+arkResponsesPath, bytes.NewReader(payload))
	if err != nil {
		return publishRiskAssessment{}, fmt.Errorf("构造风险审核请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := arkHTTPClient.Do(req)
	if err != nil {
		return publishRiskAssessment{}, fmt.Errorf("风险审核接口不可达: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBoundedBody(resp.Body, 4<<20)
	if err != nil {
		return publishRiskAssessment{}, fmt.Errorf("读取风险审核响应失败: %w", err)
	}
	text, err := arkExtractOutputText(respBody, resp.StatusCode)
	if err != nil {
		return publishRiskAssessment{}, err
	}
	result, err := parsePublishRiskAssessment(text)
	if err != nil {
		result.Raw = truncateRunes(text, 2000)
		return result, err
	}
	return result, nil
}

func assessPublishRiskWithFallback(ctx context.Context, job publishJob, assess func(context.Context, publishJob) (publishRiskAssessment, error)) publishRiskAssessment {
	if assess == nil {
		return fallbackPublishRisk("", errors.New("风险审核未装配"))
	}
	result, err := assess(ctx, job)
	if err != nil {
		return fallbackPublishRisk(result.Raw, err)
	}
	// 即使替换了 assessor，也必须经过同一结构校验；未知 level 绝不能以「非 block」
	// 身份流入审核门。生产 arkAssess 已解析一次，这里是便宜的纵深防御。
	raw, err := json.Marshal(result)
	if err != nil {
		return fallbackPublishRisk(result.Raw, err)
	}
	normalized, err := parsePublishRiskAssessment(string(raw))
	if err != nil {
		return fallbackPublishRisk(result.Raw, err)
	}
	return normalized
}

func parsePublishRiskAssessment(text string) (publishRiskAssessment, error) {
	cleaned := stripMarkdownFence(text)
	if start, end := strings.Index(cleaned, "{"), strings.LastIndex(cleaned, "}"); start >= 0 && end > start {
		cleaned = cleaned[start : end+1]
	}
	var result publishRiskAssessment
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return publishRiskAssessment{}, fmt.Errorf("风险审核输出不是合法 JSON: %w", err)
	}
	result.Level = strings.ToLower(strings.TrimSpace(result.Level))
	if !validPublishRiskLevel(result.Level, true) {
		return publishRiskAssessment{}, fmt.Errorf("未知风险级别 %q", result.Level)
	}
	if len(result.Items) > 12 {
		return publishRiskAssessment{}, errors.New("风险条目超过 12 条")
	}
	highest := publishRiskPass
	allowedCategories := map[string]bool{
		publishRiskCategoryFactual: true, publishRiskCategoryEfficacy: true,
		publishRiskCategoryRepublication: true,
		publishRiskCategoryCompetitor:    true, publishRiskCategoryDataSource: true,
	}
	for i := range result.Items {
		item := &result.Items[i]
		item.Category = strings.ToLower(strings.TrimSpace(item.Category))
		item.Level = strings.ToLower(strings.TrimSpace(item.Level))
		// certainty 不做严格校验而是收敛：只有明确的 confirmed 才升到首屏，
		// 其余（拼写错、缺失、未来新值）一律落回 unverified。模型漏填不该把
		// 一条待核提示误升成「已证实的错误」。
		if strings.ToLower(strings.TrimSpace(item.Certainty)) == publishRiskCertaintyConfirmed {
			item.Certainty = publishRiskCertaintyConfirmed
		} else {
			item.Certainty = publishRiskCertaintyUnverified
		}
		item.Evidence = truncateRunes(strings.TrimSpace(item.Evidence), 500)
		item.Reason = truncateRunes(strings.TrimSpace(item.Reason), 500)
		if !allowedCategories[item.Category] {
			return publishRiskAssessment{}, fmt.Errorf("风险条目 %d 的 category=%q 不在允许集", i+1, item.Category)
		}
		if !validPublishRiskLevel(item.Level, false) {
			return publishRiskAssessment{}, fmt.Errorf("风险条目 %d 的 level=%q 必须是 warning/block", i+1, item.Level)
		}
		if item.Evidence == "" || item.Reason == "" {
			return publishRiskAssessment{}, fmt.Errorf("风险条目 %d 缺少 evidence/reason", i+1)
		}
		if item.Level == publishRiskBlock {
			highest = publishRiskBlock
		} else if highest == publishRiskPass {
			highest = publishRiskWarning
		}
	}
	if result.Level != publishRiskPass && len(result.Items) == 0 {
		return publishRiskAssessment{}, errors.New("warning/block 必须给出至少一条证据")
	}
	if highest == publishRiskBlock || (highest == publishRiskWarning && result.Level == publishRiskPass) {
		result.Level = highest
	}
	result.Headline = truncateRunes(strings.Join(strings.Fields(result.Headline), " "), publishRiskHeadlineMaxRunes)
	result.Notes = truncateRunes(strings.TrimSpace(result.Notes), 600)
	result.Raw = ""
	return result, nil
}

// publishRiskHeadlineOrFallback 卡片首屏那句话的唯一取值口径。模型没给 headline、
// 旧任务没有该字段时，代码自己合成一句——首屏永远有话说，且绝不在有已证实问题时
// 显示成「未发现」。
func publishRiskHeadlineOrFallback(job publishJob) string {
	if h := strings.TrimSpace(job.RiskHeadline); h != "" {
		return truncateRunes(h, publishRiskHeadlineMaxRunes)
	}
	confirmed, deferred := splitPublishRiskItems(job.RiskItems)
	if len(confirmed) > 0 {
		lead := strings.Join(strings.Fields(confirmed[0].Reason), " ")
		if lead == "" {
			lead = strings.Join(strings.Fields(confirmed[0].Evidence), " ")
		}
		return truncateRunes(publishRiskCategoryLabel(confirmed[0].Category)+"："+lead, publishRiskHeadlineMaxRunes)
	}
	if !publishRiskLevelKnown(job.RiskLevel) {
		return "风险检查未完成，发布前需人工核对。"
	}
	if job.RiskLevel == publishRiskBlock {
		// 阻断结论没有对应的已证实条目时，首屏也必须把阻断说出来。
		return fmt.Sprintf("判定为阻断级，%d 条提示需人工处理后再决定。", len(deferred))
	}
	// 待核条数由折叠条标题承担，这里不再说第二遍——两行紧挨着复读同一个数字
	// 是 2026-08-08 真机截图上最刺眼的地方。
	return "未发现可证实的实质性错误。"
}

func validPublishRiskLevel(level string, allowPass bool) bool {
	return (allowPass && level == publishRiskPass) || level == publishRiskWarning || level == publishRiskBlock
}

func fallbackPublishRisk(raw string, cause error) publishRiskAssessment {
	reason := "未知原因"
	if cause != nil {
		reason = truncateRunes(strings.Join(strings.Fields(cause.Error()), " "), 300)
	}
	return publishRiskAssessment{
		Level:    publishRiskWarning,
		Headline: "风险检查未完成，这篇没有经过任何自动核对，请人工通读后再决定。",
		Items: []publishRiskItem{{
			Category:  publishRiskCategorySystem,
			Level:     publishRiskWarning,
			Certainty: publishRiskCertaintyUnverified,
			Evidence:  "风险模型未能产出可验证的结构化结果",
			Reason:    reason,
		}},
		Notes: "风险检查未完成，必须由审核人手工核对后再决定。原因：" + reason,
		Raw:   truncateRunes(strings.TrimSpace(raw), 2000),
	}
}

func publishRiskLevelLabel(level string) string {
	switch level {
	case publishRiskPass:
		return "通过"
	case publishRiskBlock:
		return "阻断"
	case publishRiskWarning:
		return "需人工核对"
	default:
		return "未检查"
	}
}

// publishRiskAllowsManualApprove 采用 allowlist 而不是「非 block」判断：空值、拼写错误、
// 未来新增级别都 fail-closed，只有已经完成检查的 pass/warning 可由人审核通过。
func publishRiskAllowsManualApprove(level string) bool {
	return level == publishRiskPass || level == publishRiskWarning
}

func publishRiskLevelKnown(level string) bool {
	return publishRiskAllowsManualApprove(level) || level == publishRiskBlock
}

func publishRiskCategoryLabel(category string) string {
	switch category {
	case publishRiskCategoryFactual:
		return "事实与常识"
	case publishRiskCategoryEfficacy:
		return "功效表达"
	case publishRiskCategoryRepublication:
		return "转载授权"
	case publishRiskCategoryCompetitor:
		return "竞品评价"
	case publishRiskCategoryDataSource:
		return "数据来源"
	case publishRiskCategorySystem:
		return "风险检查异常"
	default:
		return "其他风险"
	}
}

// formatPublishRiskText 是飞书卡片降级为纯文本时的风险摘要；限制条目与长度，避免
// 通知过长被平台拒收。完整证据仍持久化在 job 详情和预览页。
func formatPublishRiskText(job publishJob) string {
	var b strings.Builder
	b.WriteString("风险审核：")
	b.WriteString(publishRiskLevelLabel(job.RiskLevel))
	b.WriteString("\n")
	b.WriteString(publishRiskHeadlineOrFallback(job))
	// 纯文本降级同样先给已证实的实质性问题，再给待核提示（与卡片同一优先级）。
	confirmed, deferred := splitPublishRiskItems(job.RiskItems)
	ordered := append(append([]publishRiskItem(nil), confirmed...), deferred...)
	limit := len(ordered)
	if limit > 4 {
		limit = 4
	}
	for i := 0; i < limit; i++ {
		item := ordered[i]
		b.WriteString("\n- ")
		b.WriteString(publishRiskCategoryLabel(item.Category))
		b.WriteString("：")
		b.WriteString(truncateRunes(strings.Join(strings.Fields(item.Evidence), " "), 100))
		b.WriteString("（")
		b.WriteString(truncateRunes(strings.Join(strings.Fields(item.Reason), " "), 100))
		b.WriteString("）")
	}
	if len(ordered) > limit {
		b.WriteString(fmt.Sprintf("\n- 另有 %d 条，请在预览中查看", len(ordered)-limit))
	}
	if strings.TrimSpace(job.RiskNotes) != "" {
		b.WriteString("\n说明：")
		b.WriteString(truncateRunes(strings.Join(strings.Fields(job.RiskNotes), " "), 160))
	}
	if job.RiskLevel == publishRiskBlock {
		b.WriteString("\n当前为阻断级，禁止一键通过发布。")
	}
	return b.String()
}
