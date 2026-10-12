package main

// yunsuo_distribution_rules.go — 多平台分发（头条号/百家号，经云梭）的通用版式规则；
// 各平台不同的参数在 yunsuo_platform_profiles.go。
//
// 与 FoodTalks 网站规则物理隔离：外发正文虽取自网站已发布稿（保留编辑在 CMS 的手改），
// 但网站专用的版式在这里改回平台口径。2026-10-12 用户裁定，金标准是 2026-03～08 人工
// 发在头条号「FBIF食品饮料创新」上的 16 篇稿（TikHub 取回的正文）：
//   - 署名按原文分行（「作者：Sage」「编辑：Bobo」），不并成一行、不补句号；
//   - 文末统一「本文为FBIF食品饮料创新原创，作者：X，转载请联系授权。」；
//   - 图注用平台原生图片描述，说明与「图片来源」用「；」并成一条；
//   - 小标题用平台标题样式，段间不插空行，正文不带任何链接；
//   - 不放作者名片/二维码：平台会提示「图片含二维码」并限制推荐；
//   - 封面取正文第一张图，并从正文里拿掉：云梭会把 cover 插到正文顶部，
//     再留一张就重复了。第一张图不到平台封面下限（头条 452×352）时留在正文，退回公众号封面。

import (
	"bytes"
	"context"
	"image"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	yunsuoStatementPublisher = "FBIF食品饮料创新"
	// 作者名片是宽条图（生产实测 970×283、1080×304，宽高比 3.4～3.6）。
	yunsuoNameCardMinAspect = 2.5
	yunsuoBylineScanBlocks  = 6
)

var (
	yunsuoBylineSegment   = regexp.MustCompile(`^(来源|出处|出品|作者|撰文|撰稿|文|编辑|责编|责任编辑)\s*[：:]\s*(\S.*)$`)
	yunsuoReferenceHeader = regexp.MustCompile(`^(参考来源|参考资料|参考文献|参考链接|资料来源)\s*[：:]?$`)
	yunsuoBareURL         = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`)
)

// yunsuoImageSize 读图片尺寸，只用于判断文末无图注的图是不是名片。测试可替换。
var yunsuoImageSize = fetchYunsuoImageSize

type yunsuoDistribution struct {
	Blocks []yunsuoBlock
	Lead   string // 封面：正文第一张图（已映射的公网地址），已从 Blocks 拿掉
	Author string // 署名里的作者，写进文末声明
}

func yunsuoBlockKind(block yunsuoBlock) string {
	switch kind := strings.ToLower(strings.TrimSpace(block.K)); kind {
	case "image":
		return "img"
	case "p":
		return "txt"
	case "blockquote":
		return "bq"
	default:
		return kind
	}
}

func yunsuoBlockText(block yunsuoBlock) string {
	return yunsuoInlinePlainText(block.Text, block.Runs)
}

// prepareYunsuoDistributionBlocks 按多平台规则整理正文块，顺序固定：
// 拆署名 → 去名片 → 取封面（封面下限按平台）。
func prepareYunsuoDistributionBlocks(blocks []yunsuoBlock, imageMap map[string]string, profile yunsuoPlatformProfile) yunsuoDistribution {
	blocks, author := splitYunsuoBylines(blocks)
	blocks = dropYunsuoNameCards(blocks, imageMap)
	blocks, lead := takeYunsuoLeadImage(blocks, imageMap, profile)
	return yunsuoDistribution{Blocks: blocks, Lead: lead, Author: author}
}

// splitYunsuoBylines 网站把署名并成「作者：Sage，编辑：Bobo。」，外发按原文分行。
// 只看开头几块，且整段每一节都是署名才拆，正文里的「作者：」不动。
func splitYunsuoBylines(blocks []yunsuoBlock) ([]yunsuoBlock, string) {
	out := make([]yunsuoBlock, 0, len(blocks)+2)
	author := ""
	seen := 0
	for _, block := range blocks {
		text := yunsuoBlockText(block)
		if seen >= yunsuoBylineScanBlocks || yunsuoBlockKind(block) != "txt" || text == "" {
			if text != "" || yunsuoBlockKind(block) == "img" {
				seen++
			}
			out = append(out, block)
			continue
		}
		seen++
		lines, lineAuthor, ok := parseYunsuoByline(text)
		if !ok {
			out = append(out, block)
			continue
		}
		if author == "" {
			author = lineAuthor
		}
		for i, line := range lines {
			split := yunsuoBlock{K: "txt", Text: line}
			if i == 0 {
				split.BlankBefore = block.BlankBefore
			}
			out = append(out, split)
		}
	}
	return out, author
}

func parseYunsuoByline(text string) ([]string, string, bool) {
	text = strings.TrimRight(strings.TrimSpace(text), "。.")
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == '，' || r == ',' })
	if len(parts) == 0 {
		return nil, "", false
	}
	lines := make([]string, 0, len(parts))
	author := ""
	for _, part := range parts {
		match := yunsuoBylineSegment.FindStringSubmatch(strings.TrimSpace(part))
		if match == nil {
			return nil, "", false
		}
		value := strings.TrimSpace(match[2])
		lines = append(lines, match[1]+"："+value)
		switch match[1] {
		case "作者", "撰文", "撰稿", "文":
			if author == "" {
				author = value
			}
		}
	}
	return lines, author, true
}

// dropYunsuoNameCards 去掉作者名片（带个人微信二维码的宽条图）。生产上名片只出现在两处：
// Mote 稿紧贴「参考来源」上方，其他作者在参考来源之后；文中同样宽但带图注的配图不动。
// 没有参考来源时，只认文末无图注且宽高比够宽的图。
func dropYunsuoNameCards(blocks []yunsuoBlock, imageMap map[string]string) []yunsuoBlock {
	refs := -1
	for i, block := range blocks {
		if kind := yunsuoBlockKind(block); (kind == "txt" || kind == "h") && yunsuoReferenceHeader.MatchString(yunsuoBlockText(block)) {
			refs = i
			break
		}
	}
	drop := map[int]bool{}
	if refs >= 0 {
		for i := refs + 1; i < len(blocks); i++ {
			if yunsuoBlockKind(blocks[i]) == "img" {
				drop[i] = true
			}
		}
		if i := previousYunsuoContentBlock(blocks, refs); i >= 0 && yunsuoUncaptionedImage(blocks, i) {
			drop[i] = true
		}
	} else {
		for i := previousYunsuoContentBlock(blocks, len(blocks)); i >= 0 && yunsuoUncaptionedImage(blocks, i); i = previousYunsuoContentBlock(blocks, i) {
			src := publicYunsuoMediaURL(mapYunsuoMedia(blocks[i].Src, imageMap))
			width, height, ok := yunsuoImageSize(src)
			if !ok || height <= 0 || float64(width)/float64(height) < yunsuoNameCardMinAspect {
				break
			}
			drop[i] = true
		}
	}
	if len(drop) == 0 {
		return blocks
	}
	out := make([]yunsuoBlock, 0, len(blocks))
	for i, block := range blocks {
		if !drop[i] {
			out = append(out, block)
		}
	}
	return out
}

// previousYunsuoContentBlock 返回 before 之前最近一块有内容的块（跳过空段）。
func previousYunsuoContentBlock(blocks []yunsuoBlock, before int) int {
	for i := before - 1; i >= 0; i-- {
		if yunsuoBlockKind(blocks[i]) == "img" || yunsuoBlockText(blocks[i]) != "" || len(blocks[i].Rows) > 0 || len(blocks[i].Items) > 0 {
			return i
		}
	}
	return -1
}

func yunsuoUncaptionedImage(blocks []yunsuoBlock, i int) bool {
	if yunsuoBlockKind(blocks[i]) != "img" {
		return false
	}
	return i+1 >= len(blocks) || yunsuoBlockKind(blocks[i+1]) != "cap"
}

// takeYunsuoLeadImage 正文第一张可外发的图做封面，连同它的图注从正文拿掉。
// 这张图不够平台封面下限就原样留在正文，封面交给调用方退回公众号封面（不挪后面的图，免得打乱顺序）。
func takeYunsuoLeadImage(blocks []yunsuoBlock, imageMap map[string]string, profile yunsuoPlatformProfile) ([]yunsuoBlock, string) {
	for i, block := range blocks {
		if yunsuoBlockKind(block) != "img" {
			continue
		}
		src := publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap))
		if src == "" {
			continue
		}
		if _, _, small := profile.coverTooSmall(src); small {
			return blocks, ""
		}
		end := i + 1
		for end < len(blocks) && yunsuoBlockKind(blocks[end]) == "cap" {
			end++
		}
		out := make([]yunsuoBlock, 0, len(blocks)-(end-i))
		out = append(out, blocks[:i]...)
		out = append(out, blocks[end:]...)
		return out, src
	}
	return blocks, ""
}

// yunsuoCaption 说明与图片来源并成一条（人工稿口径「说明；图片来源：X」），
// 头条图片描述最多 50 字。
func yunsuoCaption(blocks []yunsuoBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(stripYunsuoBareURLs(yunsuoBlockText(block))); text != "" {
			parts = append(parts, strings.TrimRight(text, "；;。"))
		}
	}
	return truncateYunsuoTitle(strings.Join(parts, "；"), 10, 50)
}

func stripYunsuoBareURLs(text string) string {
	if !strings.Contains(text, "http") {
		return text
	}
	return yunsuoBareURL.ReplaceAllString(text, "")
}

func yunsuoStatement(publisher, author string) string {
	publisher = compactYunsuoText(publisher)
	if publisher == "" {
		publisher = yunsuoStatementPublisher
	}
	if author = compactYunsuoText(author); author != "" {
		return "本文为" + publisher + "原创，作者：" + author + "，转载请联系授权。"
	}
	return "本文为" + publisher + "原创，转载请联系授权。"
}

func fetchYunsuoImageSize(src string) (int, int, bool) {
	if src == "" {
		return 0, 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return 0, 0, false
	}
	req.Header.Set("Range", "bytes=0-65535")
	resp, err := doOutboundRequest(articleFetchHTTPClient, req, publicWebURLPolicy)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, 0, false
	}
	// 只要文件头：服务端不认 Range 时也只读前 64KB。
	head, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return 0, 0, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(head))
	if err != nil {
		return 0, 0, false
	}
	return config.Width, config.Height, true
}
