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

type yunsuoBlock struct {
	K           string            `json:"k"`
	Level       int               `json:"level,omitempty"`
	Text        string            `json:"text,omitempty"`
	Runs        []Run             `json:"runs,omitempty"`
	Src         string            `json:"src,omitempty"`
	Poster      string            `json:"poster,omitempty"`
	Ordered     bool              `json:"ordered,omitempty"`
	Rows        [][]TableCell     `json:"rows,omitempty"`
	Items       []json.RawMessage `json:"items,omitempty"`
	BlankBefore bool              `json:"blankBefore,omitempty"`
}

type yunsuoInline struct {
	Text string `json:"text,omitempty"`
	Runs []Run  `json:"runs,omitempty"`
}

// renderYunsuoHTML 从中立 Block[] 直接生成外部平台可移植 HTML。
// 它不读 FoodTalks ContentHTML，也不引用任何网站渲染器或页脚。
func renderYunsuoHTML(blocksJSON, imageMapJSON string) (string, error) {
	blocks, imageMap, err := decodeYunsuoContent(blocksJSON, imageMapJSON)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for i := 0; i < len(blocks); i++ {
		block := blocks[i]
		if block.BlankBefore && out.Len() > 0 {
			out.WriteString("<p><br></p>")
		}
		switch strings.ToLower(strings.TrimSpace(block.K)) {
		case "h":
			level := block.Level
			if level < 2 || level > 4 {
				level = 2
			}
			content := renderYunsuoInline(block.Text, block.Runs)
			if content != "" {
				fmt.Fprintf(&out, "<h%d>%s</h%d>", level, content, level)
			}
		case "p", "txt":
			writeYunsuoTextElement(&out, "p", block.Text, block.Runs)
		case "cap":
			content := renderYunsuoInline(block.Text, block.Runs)
			if content != "" {
				out.WriteString("<p><small>")
				out.WriteString(content)
				out.WriteString("</small></p>")
			}
		case "bq", "blockquote":
			writeYunsuoTextElement(&out, "blockquote", block.Text, block.Runs)
		case "img", "image":
			if src := publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap)); src != "" {
				out.WriteString("<p><img src=\"")
				out.WriteString(stdhtml.EscapeString(src))
				out.WriteString("\" alt=\"\"></p>")
			}
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
			ordered := block.Ordered
			tag := "ul"
			if ordered {
				tag = "ol"
			}
			out.WriteByte('<')
			out.WriteString(tag)
			out.WriteByte('>')
			for i < len(blocks) && strings.EqualFold(strings.TrimSpace(blocks[i].K), "li") && blocks[i].Ordered == ordered {
				writeYunsuoListItem(&out, blocks[i].Text, blocks[i].Runs)
				i++
			}
			i--
			out.WriteString("</")
			out.WriteString(tag)
			out.WriteByte('>')
		case "list":
			writeYunsuoList(&out, block)
		default:
			// 对未来新增的纯文本 block 做无样式降级，不吞正文。
			writeYunsuoTextElement(&out, "p", block.Text, block.Runs)
		}
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		return "", errors.New("云梭分发正文为空")
	}
	return result, nil
}

// buildYunsuoPushPayload 组装一次不可变的推送快照。targets 可传全量，
// 也可在安全重试时只传已明确失败的账号。
func buildYunsuoPushPayload(job publishJob, targets []yunsuoAccountTarget, categories []string) (yunsuoPushPayload, error) {
	blocks, imageMap, err := decodeYunsuoContent(job.BlocksJSON, job.ImageMap)
	if err != nil {
		return yunsuoPushPayload{}, err
	}
	bodyHTML, err := renderYunsuoHTML(job.BlocksJSON, job.ImageMap)
	if err != nil {
		return yunsuoPushPayload{}, err
	}
	title := compactYunsuoText(job.Title)
	if title == "" {
		return yunsuoPushPayload{}, errors.New("云梭分发缺少标题")
	}
	description := compactYunsuoText(job.Summary)
	if description == "" {
		description = yunsuoBlocksPlainText(blocks)
	}
	description = truncateYunsuoRunes(description, 200)

	cover := publicYunsuoMediaURL(mapYunsuoMedia(job.CoverURL, imageMap))
	if cover == "" {
		cover = firstYunsuoImage(blocks, imageMap)
	}
	if cover == "" {
		return yunsuoPushPayload{}, errors.New("云梭分发缺少公开可访问的封面")
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
	return yunsuoPushPayload{
		Type:        0,
		ShortTitle:  yunsuoTitleVariant(title, supplement, 8, 20),
		Title:       yunsuoTitleVariant(title, supplement, 21, 40),
		LongTitle:   yunsuoTitleVariant(title, supplement, 41, 60),
		AccountIDs:  accountIDs,
		Keywords:    yunsuoKeywords(job.Tags),
		Description: description,
		Categories:  strings.Join(categories, ","),
		Cover:       cover,
		HTML:        bodyHTML,
	}, nil
}

func decodeYunsuoContent(blocksJSON, imageMapJSON string) ([]yunsuoBlock, map[string]string, error) {
	if strings.TrimSpace(blocksJSON) == "" {
		return nil, nil, errors.New("云梭分发缺少中立 BlocksJSON")
	}
	var blocks []yunsuoBlock
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return nil, nil, errors.New("云梭分发的 BlocksJSON 格式无效")
	}
	if len(blocks) == 0 {
		return nil, nil, errors.New("云梭分发的 BlocksJSON 为空")
	}
	imageMap := map[string]string{}
	if strings.TrimSpace(imageMapJSON) != "" {
		if err := json.Unmarshal([]byte(imageMapJSON), &imageMap); err != nil {
			return nil, nil, errors.New("云梭分发的 ImageMap 格式无效")
		}
	}
	return blocks, imageMap, nil
}

func renderYunsuoInline(text string, runs []Run) string {
	if len(runs) == 0 {
		return escapeYunsuoText(text)
	}
	var out strings.Builder
	for _, run := range runs {
		content := escapeYunsuoText(run.Text)
		if content == "" {
			continue
		}
		if run.Bold {
			content = "<strong>" + content + "</strong>"
		}
		if strings.EqualFold(run.Type, "link") {
			if href := safeYunsuoLink(run.Href); href != "" {
				content = "<a href=\"" + stdhtml.EscapeString(href) + "\">" + content + "</a>"
			}
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

func writeYunsuoList(out *strings.Builder, block yunsuoBlock) {
	tag := "ul"
	if block.Ordered {
		tag = "ol"
	}
	items := make([]yunsuoInline, 0, len(block.Items)+1)
	for _, raw := range block.Items {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			items = append(items, yunsuoInline{Text: text})
			continue
		}
		var item yunsuoInline
		if json.Unmarshal(raw, &item) == nil {
			items = append(items, item)
		}
	}
	if len(items) == 0 && (strings.TrimSpace(block.Text) != "" || len(block.Runs) > 0) {
		items = append(items, yunsuoInline{Text: block.Text, Runs: block.Runs})
	}
	if len(items) == 0 {
		return
	}
	out.WriteByte('<')
	out.WriteString(tag)
	out.WriteByte('>')
	for _, item := range items {
		writeYunsuoListItem(out, item.Text, item.Runs)
	}
	out.WriteString("</")
	out.WriteString(tag)
	out.WriteByte('>')
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

func safeYunsuoLink(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		if publicWebURLPolicy(parsed) != nil {
			return ""
		}
	case "mailto":
		if parsed.Opaque == "" && parsed.Path == "" {
			return ""
		}
	default:
		return ""
	}
	return parsed.String()
}

func firstYunsuoImage(blocks []yunsuoBlock, imageMap map[string]string) string {
	for _, block := range blocks {
		if strings.EqualFold(strings.TrimSpace(block.K), "img") || strings.EqualFold(strings.TrimSpace(block.K), "image") {
			if src := publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap)); src != "" {
				return src
			}
		}
	}
	return ""
}

func yunsuoBlocksPlainText(blocks []yunsuoBlock) string {
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

func yunsuoTitleVariant(base, supplement string, minRunes, maxRunes int) string {
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
	return truncateYunsuoRunes(candidate, maxRunes)
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
