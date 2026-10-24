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

// distributionMarkup 正文里小标题（标题1）、加粗行（标题2 与整段加粗）、图与图注怎么写。
// 写法看平台编辑器，只用填得进去还留得住的标签；参数都已转义。
// 外发正文从中立 Block[] 直接生成，不读 FoodTalks ContentHTML，也不引用任何网站渲染器或页脚；
// 先切公众号导流尾（与网站同一口径，不看是不是原创），版式按多平台规则（yunsuo_distribution_rules.go）。
type distributionMarkup struct {
	Heading  func(text string) string
	BoldLine func(text string) string
	Image    func(src, caption string) string
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
	if profile.Markup.Heading == nil || profile.Markup.BoldLine == nil || profile.Markup.Image == nil {
		return yunsuoRendered{}, fmt.Errorf("%s没有正文写法，不能生成外发正文", profile.Name)
	}
	dist := prepareYunsuoDistribution(decoded, imageMap, profile, wechatCover, chosen)
	body, hasStatement := writeYunsuoBlocks(dist.Blocks, imageMap, profile.Markup)
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

// renderExternalBody 推给这个平台的正文与封面：各平台同一套规则，写法按 profile.Markup；
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
	// value 的媒体都丢弃：把原文中未镜像 URL 交给分发服务回源，会形成
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

func compactYunsuoText(value string) string {
	return strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")
}
