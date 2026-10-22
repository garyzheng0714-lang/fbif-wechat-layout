package main

// yunsuo_distribution_rules.go — 多平台分发（头条号经云梭、百家号经自己的分发服务）的通用版式规则；
// 各平台不同的参数与正文写法在 yunsuo_platform_profiles.go。
//
// 与 FoodTalks 网站规则物理隔离：外发正文虽取自网站已发布稿（保留编辑在 CMS 的手改），
// 但网站专用的版式在这里改回平台口径。金标准是编辑手发在头条号、百家号上的稿（2026-10-20
// 用户：「按照人工稿改，这应该是你的标准」；对照 10-15 起的 6 篇与 8 月老稿，两个平台一样）：
//   - 开头不放署名；署名（作者、编辑，编译稿的英文来源、中文编辑，含文末的署名行）按原文
//     顺序写进文末「本文为FBIF食品饮料创新原创，作者：X，编辑：Y，转载请联系授权。」；
//   - 两个以上的小标题编「一、二、」（原文已编号的不动）；
//   - 首图留在原位带图注，说明与「图片来源」用「；」并成一条、不截短；
//   - 段间不插空行，正文不带任何链接；
//   - 不放作者名片/二维码：平台会提示「图片含二维码」并限制推荐；
//   - 封面用公众号封面（与 FoodTalks 一致）；云梭会把封面插到正文最前面的平台（CoverPrepended，
//     百家号经云梭时是），文章以图开头就用这张图当封面并从正文拿掉，插回来正好在原位。

import (
	"bytes"
	"context"
	"image"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	yunsuoStatementPublisher = "FBIF食品饮料创新"
	// 作者名片是宽条图（生产实测 970×283、1080×304，宽高比 3.4～3.6）。
	yunsuoNameCardMinAspect = 2.5
	yunsuoBylineScanBlocks  = 6
)

var (
	// FoodTalks 署名会并入「编译/翻译/采访」等人员（rule-behavior-foodtalks.js CREDIT_LINE_RE），这里逐项拆开写进声明。
	// 角色与网站文末署名同一份（rule_defaults.go tail_credit_regex），只是不收「来源/出处/出品」：
	// 原创稿开头的「来源：」是数据出处，不是署名。
	yunsuoBylineSegment   = regexp.MustCompile(`^(作者|撰文|撰稿|文|编辑|责编|责任编辑|编审|主编|总编辑|新媒体编辑|中文编辑|英文来源|美编|视觉|新视觉|创意|策划|监制|排版|设计|校对|审校|审核|选题|翻译|编译|采访|统筹)\s*[：:｜|]\s*(\S.*)$`)
	yunsuoReferenceHeader = regexp.MustCompile(`^(参考来源|参考资料|参考文献|参考链接|资料来源)\s*[：:]?$`)
	yunsuoBareURL         = regexp.MustCompile(`https?://[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+`)
)

// yunsuoImageSize 读图片尺寸，只用于判断文末无图注的图是不是名片。测试可替换。
var yunsuoImageSize = fetchYunsuoImageSize

type yunsuoDistribution struct {
	Blocks  []yunsuoBlock
	Cover   string   // 推给云梭的封面（已映射的公网地址）
	Credits []string // 署名（「作者：Sage」「编辑：Bobo」），写进文末声明
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

// prepareYunsuoDistribution 按多平台规则整理正文块，顺序固定：
// 去名片 → 署名挪进声明 → 小标题编号 → 定封面。wechatCover 是公众号封面（已映射），
// chosen 是人给这个平台换的封面（publish_external_drafts.go），空表示没换。
func prepareYunsuoDistribution(blocks []yunsuoBlock, imageMap map[string]string, profile yunsuoPlatformProfile, wechatCover, chosen string) yunsuoDistribution {
	blocks = dropYunsuoNameCards(blocks, imageMap)
	blocks, credits := takeYunsuoCredits(blocks)
	blocks = numberYunsuoSections(blocks)
	blocks, cover := chooseYunsuoCover(blocks, imageMap, profile, strings.TrimSpace(wechatCover), strings.TrimSpace(chosen))
	return yunsuoDistribution{Blocks: blocks, Cover: cover, Credits: credits}
}

// yunsuoWechatCover 文章的公众号封面（FoodTalks 封面同一张），映射成可外发的公网地址。
func yunsuoWechatCover(job publishJob, imageMap map[string]string) string {
	return publicYunsuoMediaURL(mapYunsuoMedia(job.CoverURL, imageMap))
}

// chooseYunsuoCover 定封面，并按云梭在这个平台怎么放封面决定正文要不要让出那张图：
// 会把封面插到正文最前面的平台，封面若是正文里的图就从正文拿掉，否则同一张图出现两次；
// 不插的平台正文不动。顺序：人换的封面 → （会插的平台）开头那张图 → 公众号封面 → 正文第一张够大的图。
func chooseYunsuoCover(blocks []yunsuoBlock, imageMap map[string]string, profile yunsuoPlatformProfile, wechatCover, chosen string) ([]yunsuoBlock, string) {
	take := func(src string) []yunsuoBlock {
		if !profile.CoverPrepended {
			return blocks
		}
		return takeYunsuoImage(blocks, imageMap, src)
	}
	if chosen != "" {
		return take(chosen), chosen
	}
	if profile.CoverPrepended {
		if i := firstYunsuoContentBlock(blocks); i >= 0 && yunsuoBlockKind(blocks[i]) == "img" {
			if src := publicYunsuoMediaURL(mapYunsuoMedia(blocks[i].Src, imageMap)); src != "" {
				if _, _, small := profile.coverTooSmall(src); !small {
					return take(src), src
				}
			}
		}
	}
	if wechatCover != "" {
		return blocks, wechatCover
	}
	for _, block := range blocks {
		if yunsuoBlockKind(block) != "img" {
			continue
		}
		if src := publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap)); src != "" {
			if _, _, small := profile.coverTooSmall(src); !small {
				return take(src), src
			}
		}
	}
	return blocks, ""
}

// takeYunsuoImage 把做了封面的那张正文图从正文拿掉。图在正文开头时图注留着（封面插回来，
// 图注正好在它下面）；在正文中间时连图注一起拿掉。不在正文里就原样返回。
func takeYunsuoImage(blocks []yunsuoBlock, imageMap map[string]string, src string) []yunsuoBlock {
	for i, block := range blocks {
		if yunsuoBlockKind(block) != "img" || publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap)) != src {
			continue
		}
		end := i + 1
		if i != firstYunsuoContentBlock(blocks) {
			for end < len(blocks) && yunsuoBlockKind(blocks[end]) == "cap" {
				end++
			}
		}
		out := make([]yunsuoBlock, 0, len(blocks)-(end-i))
		out = append(out, blocks[:i]...)
		return append(out, blocks[end:]...)
	}
	return blocks
}

// firstYunsuoContentBlock 第一块有内容的块（跳过空段）。
func firstYunsuoContentBlock(blocks []yunsuoBlock) int {
	for i := range blocks {
		if yunsuoBlockHasContent(blocks[i]) {
			return i
		}
	}
	return -1
}

func yunsuoBlockHasContent(block yunsuoBlock) bool {
	return yunsuoBlockKind(block) == "img" || yunsuoBlockText(block) != "" || len(block.Rows) > 0 || len(block.Items) > 0
}

// yunsuoCoverCandidates 正文里够得上平台封面下限的图（去掉名片后，按正文顺序），给人挑封面用。
// 只看前 scan 张图、最多取 limit 张，读不到尺寸的不算。
func yunsuoCoverCandidates(blocks []yunsuoBlock, imageMap map[string]string, profile yunsuoPlatformProfile, scan, limit int) []publishExternalCoverCandidate {
	var out []publishExternalCoverCandidate
	for _, image := range yunsuoBodyImages(blocks, imageMap, scan) {
		if len(out) >= limit {
			break
		}
		if image.Width >= profile.CoverMinWidth && image.Height >= profile.CoverMinHeight {
			out = append(out, image)
		}
	}
	return out
}

// yunsuoBodyImages 正文前 scan 张图（去掉作者名片、去重）及其尺寸；读不到尺寸的不要。
func yunsuoBodyImages(blocks []yunsuoBlock, imageMap map[string]string, scan int) []publishExternalCoverCandidate {
	blocks = dropYunsuoNameCards(blocks, imageMap)
	var out []publishExternalCoverCandidate
	seen := map[string]bool{}
	for _, block := range blocks {
		if scan <= 0 {
			break
		}
		if yunsuoBlockKind(block) != "img" {
			continue
		}
		src := publicYunsuoMediaURL(mapYunsuoMedia(block.Src, imageMap))
		if src == "" || seen[src] {
			continue
		}
		seen[src] = true
		scan--
		if width, height, ok := yunsuoImageSize(src); ok {
			out = append(out, publishExternalCoverCandidate{URL: src, Width: width, Height: height})
		}
	}
	return out
}

// takeYunsuoCredits 把署名行从正文拿出来，按原文顺序返回（写进文末声明）。署名在两处：开头几块
// （网站并成「作者：Sage，编辑：Bobo。」），以及正文最后（编译稿的「英文来源：」「中文编辑：」，#289）。
// 整段每一节都是署名才算，正文里的「作者：」不动。
func takeYunsuoCredits(blocks []yunsuoBlock) ([]yunsuoBlock, []string) {
	drop := map[int][]string{}
	seen := 0
	for i, block := range blocks {
		if seen >= yunsuoBylineScanBlocks {
			break
		}
		if !yunsuoBlockHasContent(block) {
			continue
		}
		seen++
		if credits, ok := yunsuoCreditBlock(block); ok {
			drop[i] = credits
		}
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		if !yunsuoBlockHasContent(blocks[i]) || yunsuoBlockKind(blocks[i]) == "img" {
			continue
		}
		credits, ok := yunsuoCreditBlock(blocks[i])
		if !ok || drop[i] != nil {
			break
		}
		drop[i] = credits
	}
	if len(drop) == 0 {
		return blocks, nil
	}
	var credits []string
	out := make([]yunsuoBlock, 0, len(blocks)-len(drop))
	for i, block := range blocks {
		if found, ok := drop[i]; ok {
			credits = append(credits, found...)
			continue
		}
		out = append(out, block)
	}
	return out, credits
}

func yunsuoCreditBlock(block yunsuoBlock) ([]string, bool) {
	if yunsuoBlockKind(block) != "txt" {
		return nil, false
	}
	return parseYunsuoByline(yunsuoBlockText(block))
}

// parseYunsuoByline 「作者：Sage，编辑：Bobo。」拆成「作者：Sage」「编辑：Bobo」；有一节不是署名就不算。
func parseYunsuoByline(text string) ([]string, bool) {
	text = strings.TrimRight(strings.TrimSpace(text), "。.")
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == '，' || r == ',' })
	if len(parts) == 0 {
		return nil, false
	}
	credits := make([]string, 0, len(parts))
	for _, part := range parts {
		match := yunsuoBylineSegment.FindStringSubmatch(strings.TrimSpace(part))
		if match == nil {
			return nil, false
		}
		credits = append(credits, match[1]+"："+compactYunsuoText(match[2]))
	}
	return credits, true
}

var (
	// 原文已经编了号的小标题：「一、」「1、」「1.」「（一）」「第一部分」「①」「Part 1」；「3.9元」是小数不是序号。
	yunsuoNumberedHeading = regexp.MustCompile(`^\s*(?:[一二三四五六七八九十百]+\s*[、.．]|\d+\s*[、．]|\d+\.(?:$|[^\d])|[（(][一二三四五六七八九十\d]+[)）]|第[一二三四五六七八九十百\d]+[章节部篇]|[①-⑳]|(?i:part)\s*\d)`)
	yunsuoChineseDigits   = []string{"", "一", "二", "三", "四", "五", "六", "七", "八", "九"}
)

// numberYunsuoSections 两个以上的标题1 编「一、二、」（人工稿口径，「结语」也编）：进了平台
// 标题1 与标题2 都是加粗段，不编号就分不出层级。原文已有编号的、参考来源不动。
func numberYunsuoSections(blocks []yunsuoBlock) []yunsuoBlock {
	var sections []int
	for i, block := range blocks {
		if yunsuoBlockKind(block) != "h" || block.Level > 2 {
			continue
		}
		text := yunsuoBlockText(block)
		if text == "" || yunsuoReferenceHeader.MatchString(text) {
			continue
		}
		if yunsuoNumberedHeading.MatchString(text) {
			return blocks
		}
		sections = append(sections, i)
	}
	if len(sections) < 2 {
		return blocks
	}
	out := append([]yunsuoBlock(nil), blocks...)
	for n, i := range sections {
		out[i].Text = yunsuoChineseNumber(n+1) + "、" + yunsuoBlockText(out[i])
		out[i].Runs = nil
	}
	return out
}

// yunsuoChineseNumber 1–99 写成「一」…「十一」…「九十九」。
func yunsuoChineseNumber(n int) string {
	tens, ones := n/10, n%10
	switch {
	case tens == 0:
		return yunsuoChineseDigits[ones]
	case tens == 1:
		return "十" + yunsuoChineseDigits[ones]
	default:
		return yunsuoChineseDigits[tens] + "十" + yunsuoChineseDigits[ones]
	}
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
		if yunsuoBlockHasContent(blocks[i]) {
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

// yunsuoCaption 说明与图片来源并成一条（人工稿口径「说明；图片来源：X」，前一句以感叹号、问号
// 收尾就不再加分号），不截短：编辑手发的头条图注有 55 字的（#280）。
func yunsuoCaption(blocks []yunsuoBlock) string {
	caption := ""
	for _, block := range blocks {
		text := strings.TrimRight(strings.TrimSpace(stripYunsuoBareURLs(yunsuoBlockText(block))), "；;。")
		if text == "" {
			continue
		}
		if last, _ := utf8.DecodeLastRuneInString(caption); caption != "" && !strings.ContainsRune("！？!?…", last) {
			caption += "；"
		}
		caption += text
	}
	return caption
}

func stripYunsuoBareURLs(text string) string {
	if !strings.Contains(text, "http") {
		return text
	}
	return yunsuoBareURL.ReplaceAllString(text, "")
}

func yunsuoStatement(publisher string, credits []string) string {
	publisher = compactYunsuoText(publisher)
	if publisher == "" {
		publisher = yunsuoStatementPublisher
	}
	if len(credits) > 0 {
		return "本文为" + publisher + "原创，" + strings.Join(credits, "，") + "，转载请联系授权。"
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
