package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// 外发通用版正文块（分发框架边界 1，docs/adr/2026-10-24-distribution-framework.md）：排版工具把第三方
// 平台通用的规则全落进块里，分发服务只把块写成各平台编辑器的标签（契约见分发服务 README「正文块」）。
// 外发正文从中立 Block[] 直接整理，不读 FoodTalks ContentHTML，也不引用任何网站渲染器或页脚；
// 先切公众号导流尾（与网站同一口径，不看是不是原创），通用规则见 distribution_rules.go。
//
// 整理出来的块只有这几种、只带这几个字段：h（text，level 2 是标题1、3 是标题2）、txt/bq/li（runs，只有文字与加粗）、
// img（src，已是公网地址）后面最多跟一个 cap（text，说明与图片来源已并成一条）、table（rows）、video（src、poster）。
// 链接只留锚文字，裸网址去掉，空的块不要；整段加粗的段落并成一个加粗片段；文末补转载声明（正文里已有就不补）。

// distributionContent 推给一个平台的正文块与封面（封面单独上传、不进正文）。
type distributionContent struct {
	Blocks []Block
	Cover  string
}

// decodeDistributionSource 外发用的正文块：先切掉公众号导流尾。
func decodeDistributionSource(blocksJSON, imageMapJSON string) ([]Block, map[string]string, error) {
	strippedJSON, err := stripWechatTrailerBlocksJSON(blocksJSON)
	if err != nil {
		return nil, nil, err
	}
	return decodeDistributionBlocks(strippedJSON, imageMapJSON)
}

// buildDistributionContent 推给这个平台的正文块与封面：各平台同一套规则，platform 只用来挑封面
// （正文里够得上平台下限的图）；draft 里人给这个平台换的封面优先。
func buildDistributionContent(job publishJob, platform distributionPlatform, draft publishExternalDraft) (distributionContent, error) {
	decoded, imageMap, err := decodeDistributionSource(job.BlocksJSON, job.ImageMap)
	if err != nil {
		return distributionContent{}, err
	}
	chosen := publicMediaURL(strings.TrimSpace(draft.CoverURL))
	dist := prepareDistribution(decoded, imageMap, platform, wechatCoverURL(job, imageMap), chosen)
	blocks, hasStatement := outgoingDistributionBlocks(dist.Blocks, imageMap)
	if len(blocks) == 0 {
		return distributionContent{}, errors.New("外发正文为空")
	}
	if !hasStatement {
		blocks = append(blocks, Block{K: "txt", Runs: []Run{{Type: "txt", Text: distributionStatement(job.SourceName, dist.Credits)}}})
	}
	return distributionContent{Blocks: blocks, Cover: dist.Cover}, nil
}

// outgoingDistributionBlocks 把整理好的块收成外发通用版；hasStatement 是正文里已经有转载声明。
// 图注按原文里紧跟图片的那几块并成一条，映射不到公网地址的图连同图注不要；连着的列表项是一个列表。
func outgoingDistributionBlocks(blocks []Block, imageMap map[string]string) ([]Block, bool) {
	var out []Block
	hasStatement := false
	addRuns := func(kind, text string, runs []Run) {
		if cleaned := cleanDistributionRuns(text, runs); len(cleaned) > 0 {
			out = append(out, Block{K: kind, Runs: cleaned})
		}
	}
	for i := 0; i < len(blocks); i++ {
		block := blocks[i]
		if strings.Contains(blockPlainText(block), "转载请联系授权") {
			hasStatement = true
		}
		switch canonicalBlockKind(block.K) {
		case "h":
			text := strings.TrimSpace(stripBareURLs(blockPlainText(block)))
			if text == "" {
				continue
			}
			level := 2
			if block.Level > 2 {
				level = 3
			}
			out = append(out, Block{K: "h", Level: level, Text: text})
		case "txt":
			if wholeBoldBlock(block) {
				if text := strings.TrimSpace(stripBareURLs(blockPlainText(block))); text != "" {
					out = append(out, Block{K: "txt", Runs: []Run{{Type: "txt", Text: text, Bold: true}}})
				}
				continue
			}
			addRuns("txt", block.Text, block.Runs)
		case "bq":
			addRuns("bq", block.Text, block.Runs)
		case "img":
			end := i + 1
			for end < len(blocks) && canonicalBlockKind(blocks[end].K) == "cap" {
				end++
			}
			captions := blocks[i+1 : end]
			i = end - 1
			src := publicMediaURL(mapMirroredMedia(block.Src, imageMap))
			if src == "" {
				continue
			}
			out = append(out, Block{K: "img", Src: src})
			if caption := mergedCaption(captions); caption != "" {
				out = append(out, Block{K: "cap", Text: caption})
			}
		case "video":
			src := publicMediaURL(mapMirroredMedia(block.Src, imageMap))
			poster := publicMediaURL(mapMirroredMedia(block.Poster, imageMap))
			if src != "" {
				out = append(out, Block{K: "video", Src: src, Poster: poster})
			} else if poster != "" {
				// 拿不到视频地址就放封面图（块格式说明书的口径）。
				out = append(out, Block{K: "img", Src: poster})
			}
		case "table":
			if len(block.Rows) == 0 {
				continue
			}
			rows := make([][]TableCell, len(block.Rows))
			for r, row := range block.Rows {
				rows[r] = make([]TableCell, len(row))
				for c, cell := range row {
					rows[r][c] = TableCell{Runs: cleanDistributionRuns("", cell.Runs), Header: cell.Header}
				}
			}
			out = append(out, Block{K: "table", Rows: rows})
		case "li":
			for ; i < len(blocks) && canonicalBlockKind(blocks[i].K) == "li"; i++ {
				addRuns("li", blocks[i].Text, blocks[i].Runs)
			}
			i--
		default:
			// 前面没有图的说明文字（图被丢掉或排在图前）、以后新增的纯文本块，都按普通段落保留，不吞正文。
			addRuns("txt", block.Text, block.Runs)
		}
	}
	return out, hasStatement
}

// cleanDistributionRuns 只留文字与加粗：外部平台正文不带链接（人工稿一个链接都没有，站外链接与裸网址
// 会被判导流限流），链接只留锚文字；去掉裸网址后没字的片段不要。
func cleanDistributionRuns(text string, runs []Run) []Run {
	if len(runs) == 0 {
		if text = stripBareURLs(text); text != "" {
			return []Run{{Type: "txt", Text: text}}
		}
		return nil
	}
	var out []Run
	for _, run := range runs {
		if t := stripBareURLs(run.Text); t != "" {
			out = append(out, Run{Type: "txt", Text: t, Bold: run.Bold})
		}
	}
	return out
}

// distributionBlocksText 外发正文块里的全部文字与媒体地址，查公众号导流尾有没有漏切用。
func distributionBlocksText(blocks []Block) string {
	var out strings.Builder
	for _, block := range blocks {
		out.WriteString(block.Text)
		for _, run := range block.Runs {
			out.WriteString(run.Text)
		}
		for _, row := range block.Rows {
			for _, cell := range row {
				for _, run := range cell.Runs {
					out.WriteString(run.Text + " ")
				}
			}
		}
		out.WriteString(block.Src + block.Poster + "\n")
	}
	return out.String()
}

// wholeBoldBlock 整段（有字的部分）都是加粗。
func wholeBoldBlock(block Block) bool {
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

func decodeDistributionBlocks(blocksJSON, imageMapJSON string) ([]Block, map[string]string, error) {
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

// publicIdentityImageMap 把已经公开可访问的媒体登记成「原样映射」。
// 网站已发布稿的图在 CMS 里就是 OSS / static.foodtalks.cn，没有流水线 ImageMap；
// 不登记的话现有出站策略会把它们当未镜像丢掉。
func publicIdentityImageMap(blocksJSON string, extras ...string) string {
	blocks, _, err := decodeDistributionBlocks(blocksJSON, "{}")
	if err != nil {
		blocks = nil
	}
	mapped := map[string]string{}
	add := func(raw string) {
		if src := publicMediaURL(raw); src != "" {
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

func mapMirroredMedia(raw string, imageMap map[string]string) string {
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

func publicMediaURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || publicWebURLPolicy(parsed) != nil {
		return ""
	}
	return parsed.String()
}

func inlinePlainText(text string, runs []Run) string {
	if len(runs) == 0 {
		return compactSpaces(text)
	}
	var out strings.Builder
	for _, run := range runs {
		out.WriteString(run.Text)
	}
	return compactSpaces(out.String())
}

// truncateTitleAtPause 超长时在标点处断开，不截在半个词上。2026-10-12：头条号用的是
// short_title（≤20 字），硬截出了「三得利卖出100亿瓶绿茶，背后藏着一家2」。
// 取不短于 minRunes、引号括号配平的最长断点；找不到才硬截。
func truncateTitleAtPause(value string, minRunes, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	for cut := maxRunes; cut >= minRunes && cut > 0; cut-- {
		// 句末标点留在标题里；逗号、分号、竖线、空格等停顿处断开时丢掉。
		if !strings.ContainsRune("！!？?。", runes[cut-1]) && !titlePause(runes[cut]) {
			continue
		}
		head := strings.TrimRightFunc(string(runes[:cut]), titlePause)
		if len([]rune(head)) >= minRunes && titleBalanced(head) {
			return head
		}
	}
	return string(runes[:maxRunes])
}

func titlePause(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("，,；;：:、｜|", r)
}

func titleBalanced(value string) bool {
	for _, pair := range []string{"“”", "‘’", "《》", "（）", "()", "【】", "「」"} {
		open, close := []rune(pair)[0], []rune(pair)[1]
		if strings.Count(value, string(open)) != strings.Count(value, string(close)) {
			return false
		}
	}
	return strings.Count(value, "\"")%2 == 0
}

func compactSpaces(value string) string {
	return strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")
}
