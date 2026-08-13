package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// publishHTMLWechatTrailerLeak 发布前最后一道切尾闸门。编辑器预览、自动发布和
// 云梭外发一旦再分叉，含这些公众号导流标记的 HTML 不得出门。不用「加入微信群 /
// 关注公众号 / 点击获取更多」——那些是网站页脚合法文案。
var publishWechatTrailerLeakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`欢迎转发朋友圈`),
	regexp.MustCompile(`预览时标签不可点`),
	regexp.MustCompile(`/\s*更多文章\s*/`),
	regexp.MustCompile(`/\s*1000\s*个食品创新案例\s*/`),
	regexp.MustCompile(`后台回复.{0,8}转载`),
	regexp.MustCompile(`(?i)fbifmedia`),
}

func publishHTMLWechatTrailerLeak(html string) string {
	for _, re := range publishWechatTrailerLeakPatterns {
		if re.MatchString(html) {
			return re.String()
		}
	}
	return ""
}

// Go 侧切尾与 public/js/rule-behavior-foodtalks.js 的 stripWechatTrailer 对齐：
// 命中最早导流标记即切掉其后全部块。云梭从 BlocksJSON 直渲，不走网站渲染器，
// 必须在这里切，不能再靠 isRepost。
var (
	wechatTrailerTipLineRe = regexp.MustCompile(`^\s*提示[：:]\s*$`)
	wechatTrailerRefHeadRe = regexp.MustCompile(`(?i)^\s*(?:[（(]?[一二三四五六七八九十百千0-9]+[)）、.．]?)?\s*参考(?:来源|资料)[：:]?\s*$`)
	wechatTrailerTipSigs   = []*regexp.Regexp{
		regexp.MustCompile(`欢迎转发朋友圈`),
		regexp.MustCompile(`后台回复.{0,4}转载`),
		regexp.MustCompile(`投稿与商务合作`),
		regexp.MustCompile(`(?i)fbifmedia`),
		regexp.MustCompile(`微信[：:]\s*FBIF_`),
	}
	wechatTrailerMarkers = []*regexp.Regexp{
		regexp.MustCompile(`/\s*更多文章\s*/`),
		regexp.MustCompile(`/\s*1000\s*个食品创新案例\s*/`),
		regexp.MustCompile(`预览时标签不可点`),
		regexp.MustCompile(`加入微信群`),
		regexp.MustCompile(`关注公众号`),
		regexp.MustCompile(`点击获取更多`),
		regexp.MustCompile(`长按.{0,4}识别.{0,2}二维码`),
		regexp.MustCompile(`扫码.{0,4}(?:加群|关注|进群)`),
	}
	wechatExternalTrailerMarkers = []*regexp.Regexp{
		regexp.MustCompile(`^\s*延伸阅读\s*$`),
		regexp.MustCompile(`(?i)^\s*END\s*$`),
		regexp.MustCompile(`^\s*版权声明[：:]?\s*$`),
		regexp.MustCompile(`红餐网已同步入驻`),
		regexp.MustCompile(`内容交流\s*[\/／]\s*采访\s*[\/／]\s*转载`),
		regexp.MustCompile(`品牌几何资讯矩阵`),
		regexp.MustCompile(`商务合作\s*[|｜]\s*(?:电话|微信)`),
	}
	wechatExternalTailConfirmRe = regexp.MustCompile(`(?:来源[：:]|作者[：:]|版权声明|红餐网已同步入驻|内容交流\s*[\/／]\s*采访\s*[\/／]\s*转载|品牌几何资讯矩阵|商务合作\s*[|｜]\s*(?:电话|微信))`)
)

func wechatTrailerBlockText(block Block) string {
	if len(block.Runs) == 0 {
		return strings.TrimSpace(block.Text)
	}
	var b strings.Builder
	for _, run := range block.Runs {
		if run.Type != "img" && run.Type != "br" {
			b.WriteString(run.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

func wechatTrailerIsTextKind(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "p", "txt", "h", "cap":
		return true
	default:
		return false
	}
}

func wechatTrailerIsParagraphKind(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "p", "txt":
		return true
	default:
		return false
	}
}

func wechatTrailerCollectFollowing(blocks []Block, i int) string {
	var b strings.Builder
	b.WriteString(wechatTrailerBlockText(blocks[i]))
	for j := i + 1; j < len(blocks) && j < i+5; j++ {
		if !wechatTrailerIsTextKind(blocks[j].K) {
			continue
		}
		b.WriteByte('\n')
		b.WriteString(wechatTrailerBlockText(blocks[j]))
	}
	return b.String()
}

func wechatTrailerHasRefAfter(blocks []Block, i int) bool {
	for j := i + 1; j < len(blocks); j++ {
		k := strings.ToLower(strings.TrimSpace(blocks[j].K))
		if k != "p" && k != "txt" && k != "h" {
			continue
		}
		if wechatTrailerRefHeadRe.MatchString(wechatTrailerBlockText(blocks[j])) {
			return true
		}
	}
	return false
}

func wechatTrailerIsNearTail(n, i int) bool {
	if n <= 0 {
		return false
	}
	if n <= 8 {
		min := n - 3
		if min < 1 {
			min = 1
		}
		return i >= min
	}
	return i >= n*65/100
}

func anyWechatTrailerRegexp(text string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

func wechatTrailerIsExternalMedia(blocks []Block, i int, text string) bool {
	if text == "" || !wechatTrailerIsNearTail(len(blocks), i) {
		return false
	}
	if !anyWechatTrailerRegexp(text, wechatExternalTrailerMarkers) {
		return false
	}
	if regexp.MustCompile(`(?i)^\s*END\s*$`).MatchString(text) {
		return wechatExternalTailConfirmRe.MatchString(wechatTrailerCollectFollowing(blocks, i))
	}
	return true
}

func stripWechatTrailerBlocks(blocks []Block) []Block {
	for i, block := range blocks {
		if !wechatTrailerIsTextKind(block.K) {
			continue
		}
		text := wechatTrailerBlockText(block)
		if wechatTrailerIsParagraphKind(block.K) && wechatTrailerTipLineRe.MatchString(text) &&
			anyWechatTrailerRegexp(wechatTrailerCollectFollowing(blocks, i), wechatTrailerTipSigs) {
			if wechatTrailerHasRefAfter(blocks, i) {
				continue
			}
			return append([]Block(nil), blocks[:i]...)
		}
		if text != "" && anyWechatTrailerRegexp(text, wechatTrailerMarkers) {
			if wechatTrailerHasRefAfter(blocks, i) {
				continue
			}
			return append([]Block(nil), blocks[:i]...)
		}
		if wechatTrailerIsExternalMedia(blocks, i, text) {
			if wechatTrailerHasRefAfter(blocks, i) {
				continue
			}
			return append([]Block(nil), blocks[:i]...)
		}
	}
	return blocks
}

func stripWechatTrailerBlocksJSON(blocksJSON string) (string, error) {
	rawJSON := strings.TrimSpace(blocksJSON)
	if rawJSON == "" {
		return "", fmt.Errorf("缺少可切尾的正文块")
	}
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
		return "", fmt.Errorf("正文块 JSON 无效：%w", err)
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("正文块为空")
	}
	blocks := make([]Block, len(raw))
	for i, item := range raw {
		if err := json.Unmarshal(item, &blocks[i]); err != nil {
			return "", fmt.Errorf("正文块 JSON 无效：%w", err)
		}
	}
	cut := len(stripWechatTrailerBlocks(blocks))
	if cut == 0 {
		return "", fmt.Errorf("切掉公众号导流尾后正文为空")
	}
	if cut == len(raw) {
		return blocksJSON, nil
	}
	out, err := json.Marshal(raw[:cut])
	if err != nil {
		return "", err
	}
	return string(out), nil
}
