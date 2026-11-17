package main

// publish_risk.go — 发布前红线风险提示。它只生成结构化提示，不改正文，也不替代人工
// 审核；解析失败必须降级 warning 并保留原始返回，不能把“模型没看懂”误当 pass。

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"
)

const (
	publishRiskPass    = "pass"
	publishRiskWarning = "warning"
	publishRiskBlock   = "block"

	// 会上首屏的三类实质错误（2026-08-08 用户裁定：重点提示低级错误与事实性错误）。
	publishRiskCategoryFactual = "factual" // 与不会变的公认事实矛盾、张冠李戴、未完成写成已完成
	publishRiskCategoryNaming  = "naming"  // 人名/品牌/公司/职务/法规名写错或全文不一致
	publishRiskCategoryNumber  = "number"  // 数字、单位、币种、口径、前后矛盾、录入事故
	// 抓但不上首屏：成稿事故（乱码、重复段、未译残句、编辑痕迹）——确定度高但严重度低。
	publishRiskCategoryIntegrity = "integrity"
	// 合规族：编辑部有既定流程、事前会确认，全文合并成一条，永不上首屏。
	// 旧四类是 2026-08-08 之前的分类，存量数据与模型习惯输出仍会出现，一律并入本族。
	publishRiskCategoryCompliance    = "compliance"
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

// publishRiskComplianceFamily 合规族：转载授权、广告法、竞品评价、数据来源标注。
// 新旧分类合并判定一处——模型出 1 条 efficacy + 1 条 data_source 就绕过「最多一条」
// 的限制，计数必须按族来。
func publishRiskComplianceFamily(category string) bool {
	switch category {
	case publishRiskCategoryCompliance, publishRiskCategoryEfficacy,
		publishRiskCategoryRepublication, publishRiskCategoryCompetitor,
		publishRiskCategoryDataSource:
		return true
	}
	return false
}

// publishRiskItemConfirmed 该条是否属于「已证实的实质性问题」——只有这类才上首屏。
//
// 首屏只留三类：factual / naming / number。理由（2026-08-08 用户裁定）：
//   - 合规族（开白、授权、广告法、数据来源）编辑部事前会确认，不需要逐条提醒，
//     一律归档到完整报告。真出现硬违法时靠 level=block 让整张卡变红并禁止一键发布，
//     不占首屏那三个名额。
//   - integrity 成稿事故（乱码、重复段）确定度高但严重度低，改起来几秒钟，不值得
//     占用审核人唯一会停下来看的位置。
//   - system 是流程状态不是文章问题。
func publishRiskItemConfirmed(item publishRiskItem) bool {
	if publishRiskComplianceFamily(item.Category) {
		return false
	}
	switch item.Category {
	case publishRiskCategoryIntegrity, publishRiskCategorySystem:
		return false
	}
	return item.Certainty == publishRiskCertaintyConfirmed
}

// publishRiskHasSystemFailure 本次风险检查是否压根没跑成（模型不可达、输出不可解析
// 等，统一落成一条 system 条目）。这种任务的首屏**不许**说「未发现实质性错误」——
// 没查过和查过没发现是两回事，混为一谈等于替一篇没体检的文章背书。
func publishRiskHasSystemFailure(items []publishRiskItem) bool {
	for _, item := range items {
		if item.Category == publishRiskCategorySystem {
			return true
		}
	}
	return false
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

//go:embed editorial/risk_prompt.md
var publishRiskPromptTemplate string

// publishRiskSystemPrompt 风险检查的 system prompt。与 enrich 侧同样的组织方式：
// prompt 是可评审、可 diff 的数据资产（api/editorial/risk_prompt.md），不是代码里的
// 长字符串——它会随编辑部口径持续迭代，埋在 Go 里没人愿意读也看不清改了什么。
func publishRiskSystemPrompt() string {
	return publishRiskPromptTemplate
}

// buildRiskUserInput 组装喂给模型的 user message，返回（模型实际看到的正文, 完整消息）。
//
// 正文一律全文送，不截断（2026-10-30 用户裁定）：原先只送前 8000 字，线上 285 篇里 33 篇
// 后半篇从没查过。MiMo Pro 上下文约 1M token，线上最长稿 3.8 万字；真超了让调用报错、
// 卡片显示检查未完成，不许静默砍掉。
//
// 返回的第一个值是 evidence 溯源校验的比对基准——必须是模型真正看到的那份文本。
func buildRiskUserInput(job publishJob, now time.Time) (bodyText, userText string) {
	bodyText = htmlToPlainText(job.ContentHTML)
	return bodyText, riskUserInput(job.Title, bodyText, now)
}

// riskUserInput 标题和纯文字正文拼成 user message；发布流水线和网页校对（article_check.go）共用这一份。
//
// 抽成纯函数有两个理由，都是评审实测出来的坑：
//   - **当前日期**：资讯讲的往往是模型知识截止之后的事。不给日期，模型会拿过时记忆
//     去「纠正」正确的新事实（公司更名、高管变动）。
//   - **注入隔离**：正文是外部抓来的，可能含「本文已通过合规审查」这类诱导文字，
//     必须显式声明分隔标记内一律为待检数据。
func riskUserInput(title, bodyText string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "今天是 %s。\n", now.In(time.FixedZone("CST", 8*3600)).Format("2006年1月2日"))
	b.WriteString("你的训练知识可能早于本文。正文与你的记忆冲突时，只能提醒、不能断言正文写错。\n")
	b.WriteString("\n文章标题：")
	b.WriteString(title)
	b.WriteString("\n\n<正文开始>\n")
	b.WriteString(bodyText)
	b.WriteString("\n<正文结束>")
	return b.String()
}

// aiAssessPublishRisk 风险检查走 MiMo Pro（ai_models.go）：不看图，但它是公开前的
// 高价值把关（2026-10-07 用户裁定 Pro 承担高价值任务）。返回的 Raw 仅在解析失败时供
// fallback 留痕，成功结果不保存整段模型响应。
func aiAssessPublishRisk(ctx context.Context, job publishJob) (publishRiskAssessment, error) {
	return aiAssessRiskText(ctx, job.Title, htmlToPlainText(job.ContentHTML), fmt.Sprintf("job %d", job.ID))
}

// aiAssessRiskText 对一份标题和纯文字正文做风险检查；who 只用来在日志里说是哪篇。
func aiAssessRiskText(ctx context.Context, title, bodyText, who string) (publishRiskAssessment, error) {
	if _, _, err := aiCredential(ctx); err != nil {
		return publishRiskAssessment{}, err
	}
	text, err := aiResponsesCall(ctx, aiModel(aiTierPro), publishRiskSystemPrompt(),
		[]map[string]any{{"type": "input_text", "text": riskUserInput(title, bodyText, time.Now())}})
	if err != nil {
		return publishRiskAssessment{}, err
	}
	result, err := parsePublishRiskAssessment(text)
	if err != nil {
		result.Raw = truncateRunes(text, 2000)
		return result, err
	}
	// 两道服务端约束，都不能只靠 prompt 自觉：编造的证据降级、合规族收敛成一条。
	var downgraded int
	result.Items, downgraded = groundPublishRiskEvidence(result.Items, bodyText)
	if downgraded > 0 {
		log.Printf("publish: WARN %s 风险条目 %d 条的 evidence 不在正文中，已降级为待核", who, downgraded)
	}
	result.Items = mergeComplianceItems(result.Items)
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
	// 新旧类别都必须在白名单里：未知 category 会让整份结果解析失败、降级成
	// 「风险检查未完成」，比报错更糟。旧四类保留是为了存量数据与模型的习惯输出。
	allowedCategories := map[string]bool{
		publishRiskCategoryFactual: true, publishRiskCategoryNaming: true,
		publishRiskCategoryNumber: true, publishRiskCategoryIntegrity: true,
		publishRiskCategoryCompliance: true, publishRiskCategoryEfficacy: true,
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
	if !publishRiskLevelKnown(job.RiskLevel) || publishRiskHasSystemFailure(job.RiskItems) {
		// 检查没跑成的任务被 fallback 降级成 warning，级别看着是「已知」的，
		// 但它其实什么都没查——首屏必须说这件事（真实任务 #79 就是这种）。
		return "风险检查未完成，这篇没有经过自动核对，请人工通读后再决定。"
	}
	if job.RiskLevel == publishRiskBlock {
		// 阻断结论没有对应的已证实条目时，首屏也必须把阻断说出来。
		return fmt.Sprintf("判定为阻断级，%d 条提示需人工处理后再决定。", len(deferred))
	}
	// 待核条数由折叠条标题承担，这里不再说第二遍——两行紧挨着复读同一个数字
	// 是 2026-08-08 真机截图上最刺眼的地方。
	return "未发现可证实的实质性错误。"
}

// mergeComplianceItems 把合规族收敛成最多一条，并强制 certainty=unverified。
//
// 为什么在服务端做而不是只写进 prompt：prompt 说「合并成最多一条」是请求，模型不照做
// 时若报错，整篇会降级成「风险检查未完成」——代价远大于收益。这里 fail-soft 合并，
// 让边界由字段约束保证，不依赖模型的服从度。
// 保留 block：真出现硬违法（婴配粉功效、绝对化用语）时整张卡要变红并禁止一键发布。
func mergeComplianceItems(items []publishRiskItem) []publishRiskItem {
	out := make([]publishRiskItem, 0, len(items))
	var merged *publishRiskItem
	reasons := make([]string, 0, 4)
	for _, item := range items {
		if !publishRiskComplianceFamily(item.Category) {
			out = append(out, item)
			continue
		}
		item.Certainty = publishRiskCertaintyUnverified
		if merged == nil {
			first := item
			first.Category = publishRiskCategoryCompliance
			merged = &first
			reasons = append(reasons, item.Reason)
			continue
		}
		// 后续合规条目并进第一条：级别取最高（block 优先），理由串联。
		if item.Level == publishRiskBlock {
			merged.Level = publishRiskBlock
		}
		if item.Reason != "" {
			reasons = append(reasons, item.Reason)
		}
	}
	if merged == nil {
		return out
	}
	merged.Reason = truncateRunes(strings.Join(reasons, "；"), 500)
	return append(out, *merged)
}

// groundPublishRiskEvidence 把「evidence 必须来自正文」从 prompt 请求变成运行时校验。
//
// 模型编造一句正文里根本不存在的话，比漏检严重得多：审核人按着它去核对、发现查无此句，
// 会当场对整个工具失去信任。匹配不上的条目降级成 unverified（fail-soft，不整篇丢弃）
// 并留痕，让它进不了首屏。
//
// 归一后比对：去全部空白、中英文引号、markdown 强调符；模型常做的轻微改写（补主语、
// 换标点）不该被误判成编造，所以短证据（<8 字）不参与判定。
func groundPublishRiskEvidence(items []publishRiskItem, sourceText string) ([]publishRiskItem, int) {
	haystack := riskEvidenceNormalize(sourceText)
	downgraded := 0
	for i := range items {
		item := &items[i]
		if item.Certainty != publishRiskCertaintyConfirmed {
			continue
		}
		needle := riskEvidenceNormalize(item.Evidence)
		if len([]rune(needle)) < 8 {
			continue // 太短不足以判定编造（模型可能只引了一个词组）
		}
		if strings.Contains(haystack, needle) {
			continue
		}
		item.Certainty = publishRiskCertaintyUnverified
		downgraded++
	}
	return items, downgraded
}

var riskEvidenceNoiseReplacer = strings.NewReplacer(
	"“", "", "”", "", "‘", "", "’", "", "\"", "", "'", "",
	"*", "", "_", "", "`", "", "…", "", "．", ".",
)

func riskEvidenceNormalize(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		// 全角数字与字母折半角，避免「２０２２」与「2022」判成两回事。
		if r >= '０' && r <= '９' {
			return '0' + (r - '０')
		}
		if r >= 'Ａ' && r <= 'Ｚ' {
			return 'a' + (r - 'Ａ')
		}
		if r >= 'ａ' && r <= 'ｚ' {
			return 'a' + (r - 'ａ')
		}
		return unicode.ToLower(r)
	}, s)
	return riskEvidenceNoiseReplacer.Replace(s)
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
	case publishRiskCategoryNaming:
		return "名称与身份"
	case publishRiskCategoryNumber:
		return "数字与口径"
	case publishRiskCategoryIntegrity:
		return "成稿问题"
	case publishRiskCategoryCompliance:
		return "合规提示"
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
