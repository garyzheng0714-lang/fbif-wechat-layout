package main

import (
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/url"
	"strings"
	"unicode"
)

// renderYunsuoHTML 从中立 Block[] 直接生成外部平台可移植 HTML。
// 它不读 FoodTalks ContentHTML，也不引用任何网站渲染器或页脚。
// 外发前先切公众号导流尾：外发与网站同一口径，不看是不是原创。
// 版式按多平台规则（yunsuo_distribution_rules.go），标签写法按平台 profile 的 markup。
func renderYunsuoHTML(blocksJSON, imageMapJSON string) (string, error) {
	rendered, err := renderYunsuoDistribution(blocksJSON, imageMapJSON, "", yunsuoGenericProfile, "", "")
	return rendered.HTML, err
}

// distributionMarkup 正文里小标题（标题1）、加粗行（标题2 与整段加粗）、图与图注怎么写。
// 写法看稿子经谁填进平台编辑器，只用填得进去还留得住的标签；参数都已转义。
type distributionMarkup struct {
	Heading  func(text string) string
	BoldLine func(text string) string
	Image    func(src, caption string) string
}

// yunsuoMarkup 云梭吃得进去的写法（2026-10-20 拿 14 篇头条稿的 payload 与线上正文逐块比出来的）：
// 云梭把正文重新解析再生成平台格式，h2/h3 → 加粗段，<p><img></p> 后跟 <p><small> → 平台
// 原生图注，链接只剩字、空段丢掉；h1、<strong>、平台原生的 pgc-img-caption 进去就丢（10-12
// 改成头条原生写法后小标题与图注都没了）。段内局部加粗照写 <strong>，云梭目前不留。
var yunsuoMarkup = distributionMarkup{
	Heading:  func(text string) string { return "<h2>" + text + "</h2>" },
	BoldLine: func(text string) string { return "<h3>" + text + "</h3>" },
	Image: func(src, caption string) string {
		out := `<p><img src="` + src + `" alt=""></p>`
		if caption != "" {
			out += "<p><small>" + caption + "</small></p>"
		}
		return out
	},
}

type yunsuoRendered struct {
	HTML  string
	Cover string
}

// decodeYunsuoDistributionContent 外发用的正文块：先切掉公众号导流尾。
func decodeYunsuoDistributionContent(blocksJSON, imageMapJSON string) ([]Block, map[string]string, error) {
	strippedJSON, err := stripWechatTrailerBlocksJSON(blocksJSON)
	if err != nil {
		return nil, nil, err
	}
	return decodeYunsuoContent(strippedJSON, imageMapJSON)
}

// renderYunsuoDistribution wechatCover 是公众号封面（已映射），chosen 是人给这个平台换的封面。
func renderYunsuoDistribution(blocksJSON, imageMapJSON, publisher string, profile yunsuoPlatformProfile, wechatCover, chosen string) (yunsuoRendered, error) {
	decoded, imageMap, err := decodeYunsuoDistributionContent(blocksJSON, imageMapJSON)
	if err != nil {
		return yunsuoRendered{}, err
	}
	dist := prepareYunsuoDistribution(decoded, imageMap, profile, wechatCover, chosen)
	body, hasStatement := writeYunsuoBlocks(dist.Blocks, imageMap, profile.markup())
	if strings.TrimSpace(body) == "" {
		return yunsuoRendered{}, errors.New("外发正文为空")
	}
	if !hasStatement {
		body += "<p>" + escapeYunsuoText(yunsuoStatement(publisher, dist.Credits)) + "</p>"
	}
	return yunsuoRendered{HTML: strings.TrimSpace(body), Cover: dist.Cover}, nil
}

// writeYunsuoBlocks 把整理好的块按平台的写法写成正文 HTML；hasStatement 是正文里已经有转载声明。
func writeYunsuoBlocks(blocks []Block, imageMap map[string]string, markup distributionMarkup) (string, bool) {
	var out strings.Builder
	hasStatement := false
	// 段间不插空行：人工稿没有空段，平台自带段距（BlankBefore 只是公众号的留白）。
	for i := 0; i < len(blocks); i++ {
		block := blocks[i]
		if strings.Contains(yunsuoBlockText(block), "转载请联系授权") {
			hasStatement = true
		}
		switch canonicalBlockKind(block.K) {
		case "h":
			text := escapeYunsuoText(strings.TrimSpace(stripYunsuoBareURLs(yunsuoBlockText(block))))
			if text == "" {
				continue
			}
			if block.Level <= 2 {
				out.WriteString(markup.Heading(text))
			} else {
				out.WriteString(markup.BoldLine(text))
			}
		case "txt":
			if yunsuoWholeBold(block) {
				out.WriteString(markup.BoldLine(escapeYunsuoText(strings.TrimSpace(stripYunsuoBareURLs(yunsuoBlockText(block))))))
				continue
			}
			writeYunsuoTextElement(&out, "p", block.Text, block.Runs)
		case "cap":
			// 前面没有图的说明文字（图被丢掉或排在图前）按普通段落保留。
			writeYunsuoTextElement(&out, "p", block.Text, block.Runs)
		case "bq":
			writeYunsuoTextElement(&out, "blockquote", block.Text, block.Runs)
		case "img":
			end := i + 1
			for end < len(blocks) && canonicalBlockKind(blocks[end].K) == "cap" {
				end++
			}
			captions := blocks[i+1 : end]
			i = end - 1
			src := publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap))
			if src == "" {
				continue
			}
			out.WriteString(markup.Image(stdhtml.EscapeString(src), escapeYunsuoText(yunsuoCaption(captions))))
		case "video":
			src := publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap))
			poster := publicYunsuoMediaURL(mapYunsuoMedia(block.Poster, imageMap))
			if src != "" {
				out.WriteString("<p><video controls src=\"")
				out.WriteString(stdhtml.EscapeString(src))
				out.WriteString("\"")
				if poster != "" {
					out.WriteString(" poster=\"")
					out.WriteString(stdhtml.EscapeString(poster))
					out.WriteString("\"")
				}
				out.WriteString("></video></p>")
			} else if poster != "" {
				out.WriteString("<p><img src=\"")
				out.WriteString(stdhtml.EscapeString(poster))
				out.WriteString("\" alt=\"视频封面\"></p>")
			}
		case "table":
			writeYunsuoTable(&out, block.Rows)
		case "li":
			out.WriteString("<ul>")
			for i < len(blocks) && canonicalBlockKind(blocks[i].K) == "li" {
				writeYunsuoListItem(&out, blocks[i].Text, blocks[i].Runs)
				i++
			}
			i--
			out.WriteString("</ul>")
		default:
			// 对未来新增的纯文本 block 做无样式降级，不吞正文。
			writeYunsuoTextElement(&out, "p", block.Text, block.Runs)
		}
	}
	return out.String(), hasStatement
}

// yunsuoWholeBold 整段（有字的部分）都是加粗。
func yunsuoWholeBold(block Block) bool {
	bold := false
	for _, run := range block.Runs {
		if strings.TrimSpace(run.Text) == "" {
			continue
		}
		if !run.Bold {
			return false
		}
		bold = true
	}
	return bold
}

// buildYunsuoPushPayload 组装一次不可变的推送快照。外发按平台分开推（每条 lane 一个
// 账号），封面与标题按这个账号的平台规则；多个账号时用通用规则。
func buildYunsuoPushPayload(job publishJob, targets []externalAccount, categories []string) (yunsuoPushPayload, error) {
	return buildYunsuoPushPayloadWithDraft(job, targets, categories, publishExternalDraft{})
}

// buildYunsuoPushPayloadWithDraft draft 是人给这个平台改过的标题、摘要、封面（只对这个平台生效，
// 公众号与网站不动）；空字段用文章原值。
func buildYunsuoPushPayloadWithDraft(job publishJob, targets []externalAccount, categories []string, draft publishExternalDraft) (yunsuoPushPayload, error) {
	profile := yunsuoGenericProfile
	if len(targets) == 1 {
		profile = yunsuoProfileForTarget(targets[0])
	}
	blocks, rendered, err := renderExternalBody(job, profile, draft)
	if err != nil {
		return yunsuoPushPayload{}, err
	}
	bodyHTML := rendered.HTML
	if leak := publishHTMLWechatTrailerLeak(bodyHTML); leak != "" {
		return yunsuoPushPayload{}, fmt.Errorf("云梭正文仍含公众号导流尾（%s），禁止外发", leak)
	}
	title := compactYunsuoText(job.Title)
	if title == "" {
		return yunsuoPushPayload{}, errors.New("云梭分发缺少标题")
	}
	description := compactYunsuoText(job.Summary)
	if summary := compactYunsuoText(draft.Summary); summary != "" {
		description = summary
	}
	if description == "" {
		description = yunsuoBlocksPlainText(blocks)
	}
	description = truncateYunsuoRunes(description, 200)
	if profile.SummaryMax > 0 && len([]rune(description)) > profile.SummaryMax {
		// 推送前检查已拦下人写的超长摘要；走到这里的只会是用正文补的摘要，在标点处截短。
		description = truncateYunsuoTitle(description, 1, profile.SummaryMax)
	}

	cover := rendered.Cover
	if cover == "" {
		return yunsuoPushPayload{}, errors.New("云梭分发缺少公开可访问的封面")
	}
	if width, height, small := profile.coverTooSmall(cover); small {
		return yunsuoPushPayload{}, fmt.Errorf("云梭分发封面图尺寸过小（%dx%d），%s要求不小于 %dx%d，请换封面后再同步",
			width, height, profile.Name, profile.CoverMinWidth, profile.CoverMinHeight)
	}

	accountIDs := make([]string, 0, len(targets))
	seenAccounts := map[string]bool{}
	for _, target := range targets {
		id := strings.TrimSpace(target.ID)
		if id == "" || seenAccounts[id] {
			continue
		}
		seenAccounts[id] = true
		accountIDs = append(accountIDs, id)
	}
	if len(accountIDs) == 0 {
		return yunsuoPushPayload{}, errors.New("云梭分发没有目标账号")
	}
	categories = uniqueYunsuoValues(categories)
	if len(categories) < 5 {
		return yunsuoPushPayload{}, errors.New("云梭分发至少需要 5 个不同分类")
	}

	supplement := description
	if supplement == "" {
		supplement = compactYunsuoText(job.SourceName)
	}
	override := compactYunsuoText(draft.Title)
	if override != "" {
		title = override
	}
	payload := yunsuoPushPayload{
		Type: 0,
		// 三档都按云梭文档的字数范围：放得下就是原标题，不足才补、超了才截（平台用的那一档
		// 超了推送前检查已拦下）。
		ShortTitle:  yunsuoTitleVariant(title, supplement, yunsuoTitleShort),
		Title:       yunsuoTitleVariant(title, supplement, yunsuoTitleMedium),
		LongTitle:   yunsuoTitleVariant(title, supplement, yunsuoTitleLong),
		AccountIDs:  accountIDs,
		Keywords:    yunsuoKeywords(job.Tags),
		Description: description,
		Categories:  strings.Join(categories, ","),
		Cover:       cover,
		HTML:        bodyHTML,
	}
	if override != "" {
		// 人给这个平台改的标题原样写进平台用的那一档，不补字、不截断。
		if min, max := yunsuoTitleFieldRange(profile.TitleField); len([]rune(override)) >= min && len([]rune(override)) <= max {
			payload.setTitleField(profile.TitleField, override)
		}
	}
	return payload, nil
}

// renderExternalBody 推给这个平台的正文与封面：各平台同一套规则，写法按 profile.markup()；
// draft 里人给这个平台换的封面优先。还返回解出的正文块（补摘要用）。
func renderExternalBody(job publishJob, profile yunsuoPlatformProfile, draft publishExternalDraft) ([]Block, yunsuoRendered, error) {
	blocks, imageMap, err := decodeYunsuoContent(job.BlocksJSON, job.ImageMap)
	if err != nil {
		return nil, yunsuoRendered{}, err
	}
	coverOverride := publicYunsuoMediaURL(strings.TrimSpace(draft.CoverURL))
	rendered, err := renderYunsuoDistribution(job.BlocksJSON, job.ImageMap, job.SourceName, profile, yunsuoWechatCover(job, imageMap), coverOverride)
	return blocks, rendered, err
}

func (p *yunsuoPushPayload) setTitleField(field, value string) {
	switch field {
	case yunsuoTitleShort:
		p.ShortTitle = value
	case yunsuoTitleLong:
		p.LongTitle = value
	default:
		p.Title = value
	}
}

// titleField 平台实际用的那一档标题（卡片显示用）。
func (p yunsuoPushPayload) titleField(field string) string {
	switch field {
	case yunsuoTitleShort:
		return p.ShortTitle
	case yunsuoTitleLong:
		return p.LongTitle
	default:
		return p.Title
	}
}

func decodeYunsuoContent(blocksJSON, imageMapJSON string) ([]Block, map[string]string, error) {
	if strings.TrimSpace(blocksJSON) == "" {
		return nil, nil, errors.New("外发缺少中立 BlocksJSON")
	}
	var blocks []Block
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return nil, nil, errors.New("外发的 BlocksJSON 格式无效")
	}
	if len(blocks) == 0 {
		return nil, nil, errors.New("外发的 BlocksJSON 为空")
	}
	imageMap := map[string]string{}
	if strings.TrimSpace(imageMapJSON) != "" {
		if err := json.Unmarshal([]byte(imageMapJSON), &imageMap); err != nil {
			return nil, nil, errors.New("外发的 ImageMap 格式无效")
		}
	}
	return blocks, imageMap, nil
}

// renderYunsuoInline 只保留文字与加粗。外部平台正文不带链接：人工稿一个链接都没有，
// 站外链接（含裸露的网址）会被判导流限流；链接只留锚文字。
func renderYunsuoInline(text string, runs []Run) string {
	if len(runs) == 0 {
		return escapeYunsuoText(stripYunsuoBareURLs(text))
	}
	var out strings.Builder
	for _, run := range runs {
		content := escapeYunsuoText(stripYunsuoBareURLs(run.Text))
		if content == "" {
			continue
		}
		if run.Bold {
			content = "<strong>" + content + "</strong>"
		}
		out.WriteString(content)
	}
	return out.String()
}

func escapeYunsuoText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.ReplaceAll(stdhtml.EscapeString(text), "\n", "<br>")
}

func writeYunsuoTextElement(out *strings.Builder, tag, text string, runs []Run) {
	content := renderYunsuoInline(text, runs)
	if content == "" {
		return
	}
	out.WriteByte('<')
	out.WriteString(tag)
	out.WriteByte('>')
	out.WriteString(content)
	out.WriteString("</")
	out.WriteString(tag)
	out.WriteByte('>')
}

func writeYunsuoTable(out *strings.Builder, rows [][]TableCell) {
	if len(rows) == 0 {
		return
	}
	out.WriteString("<table border=\"1\" cellspacing=\"0\" cellpadding=\"6\"><tbody>")
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		out.WriteString("<tr>")
		for _, cell := range row {
			tag := "td"
			if cell.Header {
				tag = "th"
			}
			out.WriteByte('<')
			out.WriteString(tag)
			out.WriteByte('>')
			out.WriteString(renderYunsuoInline("", cell.Runs))
			out.WriteString("</")
			out.WriteString(tag)
			out.WriteByte('>')
		}
		out.WriteString("</tr>")
	}
	out.WriteString("</tbody></table>")
}

func writeYunsuoListItem(out *strings.Builder, text string, runs []Run) {
	content := renderYunsuoInline(text, runs)
	if content == "" {
		return
	}
	out.WriteString("<li>")
	out.WriteString(content)
	out.WriteString("</li>")
}

// yunsuoPublicIdentityImageMap 把已经公开可访问的媒体登记成「原样映射」。
// 网站已发布稿的图在 CMS 里就是 OSS / static.foodtalks.cn，没有流水线 ImageMap；
// 不登记的话现有出站策略会把它们当未镜像丢掉。
func yunsuoPublicIdentityImageMap(blocksJSON string, extras ...string) string {
	blocks, _, err := decodeYunsuoContent(blocksJSON, "{}")
	if err != nil {
		blocks = nil
	}
	mapped := map[string]string{}
	add := func(raw string) {
		if src := publicYunsuoMediaURL(raw); src != "" {
			mapped[src] = src
		}
	}
	for _, extra := range extras {
		add(extra)
	}
	for _, block := range blocks {
		add(block.Src)
		add(block.Poster)
	}
	raw, err := json.Marshal(mapped)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func mapYunsuoMedia(raw string, imageMap map[string]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if mapped := strings.TrimSpace(imageMap[raw]); mapped != "" {
		return mapped
	}
	// mirroring 阶段会把原图 URL 映射到 OSS；CoverURL 在之后可能已经
	// 直接是某个 value，所以同时允许已镜像值。任何既不是 key 也不是
	// value 的媒体都丢弃：把原文中未镜像 URL 交给云梭回源，会形成
	// 不受我们出站策略约束的二次 SSRF。
	for _, mapped := range imageMap {
		if raw == strings.TrimSpace(mapped) {
			return raw
		}
	}
	return ""
}

func publicYunsuoMediaURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || publicWebURLPolicy(parsed) != nil {
		return ""
	}
	return parsed.String()
}

func yunsuoBlocksPlainText(blocks []Block) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := yunsuoInlinePlainText(block.Text, block.Runs); text != "" {
			parts = append(parts, text)
		}
		for _, row := range block.Rows {
			for _, cell := range row {
				if text := yunsuoInlinePlainText("", cell.Runs); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return truncateYunsuoRunes(compactYunsuoText(strings.Join(parts, " ")), 200)
}

func yunsuoInlinePlainText(text string, runs []Run) string {
	if len(runs) == 0 {
		return compactYunsuoText(text)
	}
	var out strings.Builder
	for _, run := range runs {
		out.WriteString(run.Text)
	}
	return compactYunsuoText(out.String())
}

func yunsuoTitleVariant(base, supplement, field string) string {
	minRunes, maxRunes := yunsuoTitleFieldRange(field)
	base = compactYunsuoText(base)
	supplement = compactYunsuoText(supplement)
	candidate := base
	for _, addition := range []string{supplement, "FBIF食品饮料创新行业观察与趋势资讯"} {
		if len([]rune(candidate)) >= minRunes || addition == "" {
			break
		}
		if candidate != "" {
			candidate += "｜"
		}
		candidate += addition
	}
	// 极短标题在上述语义补足后理论上已足够；仍不足时循环补入
	// 中性的“行业观察”，保证契约字符数而不伪造具体信息。
	for len([]rune(candidate)) < minRunes {
		candidate += "行业观察"
	}
	return truncateYunsuoTitle(candidate, minRunes, maxRunes)
}

// truncateYunsuoTitle 超长时在标点处断开，不截在半个词上。2026-10-12：头条号用的是
// short_title（≤20 字），硬截出了「三得利卖出100亿瓶绿茶，背后藏着一家2」。
// 取不短于 minRunes、引号括号配平的最长断点；找不到才硬截。
func truncateYunsuoTitle(value string, minRunes, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	for cut := maxRunes; cut >= minRunes && cut > 0; cut-- {
		// 句末标点留在标题里；逗号、分号、竖线、空格等停顿处断开时丢掉。
		if !strings.ContainsRune("！!？?。", runes[cut-1]) && !yunsuoTitlePause(runes[cut]) {
			continue
		}
		head := strings.TrimRightFunc(string(runes[:cut]), yunsuoTitlePause)
		if len([]rune(head)) >= minRunes && yunsuoTitleBalanced(head) {
			return head
		}
	}
	return string(runes[:maxRunes])
}

func yunsuoTitlePause(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("，,；;：:、｜|", r)
}

func yunsuoTitleBalanced(value string) bool {
	for _, pair := range []string{"“”", "‘’", "《》", "（）", "()", "【】", "「」"} {
		open, close := []rune(pair)[0], []rune(pair)[1]
		if strings.Count(value, string(open)) != strings.Count(value, string(close)) {
			return false
		}
	}
	return strings.Count(value, "\"")%2 == 0
}

func yunsuoKeywords(tags []string) string {
	values := uniqueYunsuoValues(tags)
	for _, fallback := range []string{"食品饮料", "行业资讯", "品牌", "消费", "创新"} {
		if len(values) >= 5 {
			break
		}
		if !containsYunsuoValue(values, fallback) {
			values = append(values, fallback)
		}
	}
	return truncateYunsuoRunes(strings.Join(values, ","), 255)
}

func uniqueYunsuoValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = compactYunsuoText(strings.Trim(value, ",，、;；"))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func containsYunsuoValue(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func compactYunsuoText(value string) string {
	return strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")
}

func truncateYunsuoRunes(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}
