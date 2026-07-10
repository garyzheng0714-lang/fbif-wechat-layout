package main

// publish_risk.go — 发布前红线风险提示。它只生成结构化提示，不改正文，也不替代人工
// 审核；解析失败必须降级 warning 并保留原始返回，不能把“模型没看懂”误当 pass。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	publishRiskPass    = "pass"
	publishRiskWarning = "warning"
	publishRiskBlock   = "block"

	publishRiskCategoryEfficacy      = "efficacy"
	publishRiskCategoryRepublication = "republication"
	publishRiskCategoryCompetitor    = "competitor"
	publishRiskCategoryDataSource    = "data_source"
	publishRiskCategorySystem        = "system"
)

type publishRiskItem struct {
	Category string `json:"category"`
	Level    string `json:"level"`
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

type publishRiskAssessment struct {
	Level string            `json:"level"`
	Items []publishRiskItem `json:"items"`
	Notes string            `json:"notes,omitempty"`
	Raw   string            `json:"raw,omitempty"`
}

func publishRiskSystemPrompt() string {
	return `你是 FoodTalks 发布前风险审核助手。请用简体中文，只输出 JSON，不要 markdown。
你只做提示与分级，不拥有发布决策权，不得改写正文。

检查四类红线：
1. 功效表达 efficacy：食品健康、治疗、减肥、改善身体等承诺是否缺证据或可能违法。
2. 转载授权 republication：是否无法确认转载许可、原作者或原出处。
3. 竞品评价 competitor：是否含无证据的贬损、排名、比较或结论性评价。
4. 数据来源 data_source：关键数字、市场份额、销量、研究结论是否缺明确来源。

输出：{"level":"pass|warning|block","items":[{"category":"efficacy|republication|competitor|data_source","level":"warning|block","evidence":"正文原句","reason":"为什么需要人工处理"}],"notes":"给审核人的简短说明"}
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
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
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
		publishRiskCategoryEfficacy: true, publishRiskCategoryRepublication: true,
		publishRiskCategoryCompetitor: true, publishRiskCategoryDataSource: true,
	}
	for i := range result.Items {
		item := &result.Items[i]
		item.Category = strings.ToLower(strings.TrimSpace(item.Category))
		item.Level = strings.ToLower(strings.TrimSpace(item.Level))
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
	result.Notes = truncateRunes(strings.TrimSpace(result.Notes), 600)
	result.Raw = ""
	return result, nil
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
		Level: publishRiskWarning,
		Items: []publishRiskItem{{
			Category: publishRiskCategorySystem,
			Level:    publishRiskWarning,
			Evidence: "风险模型未能产出可验证的结构化结果",
			Reason:   reason,
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
	limit := len(job.RiskItems)
	if limit > 4 {
		limit = 4
	}
	for i := 0; i < limit; i++ {
		item := job.RiskItems[i]
		b.WriteString("\n- ")
		b.WriteString(publishRiskCategoryLabel(item.Category))
		b.WriteString("：")
		b.WriteString(truncateRunes(strings.Join(strings.Fields(item.Evidence), " "), 100))
		b.WriteString("（")
		b.WriteString(truncateRunes(strings.Join(strings.Fields(item.Reason), " "), 100))
		b.WriteString("）")
	}
	if len(job.RiskItems) > limit {
		b.WriteString(fmt.Sprintf("\n- 另有 %d 条，请在预览中查看", len(job.RiskItems)-limit))
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
