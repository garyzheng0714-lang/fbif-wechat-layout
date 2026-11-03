package main

// publish_external_captions.go — 图注超了平台上限，推之前缩写（2026-11-03 用户选「AI 缩写到 50 字内」）。
//
// 头条号图注最多 50 字（分发服务的平台文件，经 GET /v1/platforms 的 caption 给过来）：超了存草稿照存，点「预览并发布」
// 时编辑器才拦、不出「确认发布」，#340 两条 58、69 字的图注就这样没发出去。所以推之前（queueExternalDistribution）
// 把这个平台超了的图注缩写：「图片来源：X」原样留着，只缩前面的说明，交 AI（ai_models.go 的 Flash）缩到剩下的字数，
// 按平台的数法核对，超了告诉它超了多少再缩一次；还不成就在标点处截。缩好的存进这个平台的改稿记录
// （publishExternalDraft.Captions，原图注 → 缩写），同一份内容再推还是这段字、同一个幂等键；
// 只改推给这个平台的，公众号和网站上的不动。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

const (
	// externalCaptionTimeout 一个平台所有超长图注的缩写一共等多久；过了还没缩好的就在标点处截。
	externalCaptionTimeout = 2 * time.Minute
	// externalCaptionAttempts 一条图注问 AI 几次：第二次告诉它上次超了几个字。
	externalCaptionAttempts = 2
)

var captionSourcePattern = regexp.MustCompile(`(图片来源|图源)\s*[:：]`)

const externalCaptionPrompt = `你是中文新媒体编辑。把用户给的图片说明缩写到不超过 %d 个字（%s）%s。
要求：保留品牌名、产品名、地名和关键信息；不编造原文没有的内容；不加引号或解释。只输出改好的图片说明本身。`

// externalCaptionsFor 这个平台要换的图注：现在正文里超了上限的每一条，上次缩好、没超的照用，其余重新缩。
// 平台不限图注、或正文里没有超的，就是 nil。
func (p *publishPipeline) externalCaptionsFor(job publishJob, profile distributionPlatform, draft publishExternalDraft) map[string]string {
	if profile.CaptionMax <= 0 {
		return nil
	}
	original := draft
	original.Captions = nil
	content, err := buildDistributionContent(job, profile, original)
	if err != nil {
		return draft.Captions // 正文解不开，交给构造 payload 那一步报错
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalCaptionTimeout)
	defer cancel()
	var out map[string]string
	for _, block := range content.Blocks {
		if block.K != "cap" || profile.captionFits(block.Text) {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		if short, ok := draft.Captions[block.Text]; ok && profile.captionFits(short) {
			out[block.Text] = short
			continue
		}
		short, by := p.shortenExternalCaption(ctx, job.ID, profile, block.Text)
		log.Printf("publish: #%d %s图注 %d 字超了 %d 字的上限，%s成 %d 字：「%s」→「%s」", job.ID, profile.Name,
			profile.captionLength(block.Text), profile.CaptionMax, by, profile.captionLength(short), block.Text, short)
		out[block.Text] = short
	}
	return out
}

// shortenExternalCaption 把一条超长图注缩到平台上限以内；by 是「AI 缩写」或「在标点处截」。
func (p *publishPipeline) shortenExternalCaption(ctx context.Context, jobID int64, profile distributionPlatform, caption string) (string, string) {
	desc, source := splitCaptionSource(caption)
	// 「；」也算字：说明能占的字数按连上之后整条数，这里先按留一个字给它算。
	budget := profile.CaptionMax - profile.captionLength(source) - 1
	var err error
	for attempt := 0; attempt < externalCaptionAttempts && budget > 0 && desc != ""; attempt++ {
		var text string
		if text, err = p.askCaptionAI(ctx, desc, budget, profile.captionCountNote(), err); err != nil {
			continue
		}
		if short := joinCaption(text, source); profile.captionFits(short) {
			return short, "AI 缩写"
		}
		err = &captionTooLongError{got: profile.captionLength(text), max: budget}
	}
	if err != nil {
		log.Printf("publish: #%d %s图注 AI 没缩成，在标点处截：%v", jobID, profile.Name, err)
	}
	return cutCaption(desc, source, profile.captionFits), "在标点处截"
}

type captionTooLongError struct{ got, max int }

func (e *captionTooLongError) Error() string {
	return fmt.Sprintf("AI 给了 %d 字，要不超过 %d 字", e.got, e.max)
}

// askCaptionAI 让 AI 把图片说明缩到 max 字以内（按 countNote 的数法）；last 是上一次的结果超了，这次告诉它。
func (p *publishPipeline) askCaptionAI(ctx context.Context, desc string, max int, countNote string, last error) (string, error) {
	again := ""
	var over *captionTooLongError
	if errors.As(last, &over) {
		again = fmt.Sprintf("；上一次缩成了 %d 个字，超了，要再短一些", over.got)
	}
	var text string
	var err error
	if p.externalCaptionAI != nil {
		text, err = p.externalCaptionAI(ctx, desc, max, countNote+again)
	} else {
		prompt := fmt.Sprintf(externalCaptionPrompt, max, countNote, again)
		text, err = aiRewriteCall(ctx, aiModel(aiTierFlash), prompt, desc)
	}
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(strings.Split(strings.TrimSpace(text), "\n")[0])
	text = strings.TrimRight(compactSpaces(strings.Trim(text, "\"“”'‘’「」")), "；;，, ")
	switch {
	case text == "":
		return "", errors.New("AI 没有给出结果")
	case captionSourcePattern.MatchString(text):
		return "", fmt.Errorf("AI 给的说明里自己带了图片来源：%s", text)
	}
	return text, nil
}

// splitCaptionSource 把并好的图注拆成说明和「图片来源：X」（最后一处起到结尾）；没写来源的 source 是空的。
func splitCaptionSource(caption string) (desc, source string) {
	loc := captionSourcePattern.FindAllStringIndex(caption, -1)
	if len(loc) == 0 {
		return strings.TrimSpace(caption), ""
	}
	at := loc[len(loc)-1][0]
	return strings.TrimRight(strings.TrimSpace(caption[:at]), "；; "), strings.TrimSpace(caption[at:])
}

// joinCaption 说明和来源照 mergedCaption 的口径并回一条：说明以句号、感叹号、问号收尾就不加「；」。
func joinCaption(desc, source string) string {
	desc = strings.TrimRight(strings.TrimSpace(desc), "；;，, ")
	switch {
	case source == "":
		return desc
	case desc == "":
		return source
	case sentenceEnded(desc):
		return desc + source
	}
	return desc + "；" + source
}

// cutCaption AI 不成时的保底：说明从后往前在标点处截，截到连上来源不超；没有能截的标点就硬截加「…」；
// 光来源就超了才连来源一起截。
func cutCaption(desc, source string, fits func(string) bool) string {
	runes := []rune(desc)
	for cut := len(runes); cut > 0; cut-- {
		if !strings.ContainsRune("。！？!?；;，,、", runes[cut-1]) {
			continue
		}
		head := strings.TrimRightFunc(string(runes[:cut]), titlePause)
		if short := joinCaption(head, source); head != "" && titleBalanced(head) && fits(short) {
			return short
		}
	}
	for cut := len(runes) - 1; cut > 0; cut-- {
		if short := joinCaption(string(runes[:cut])+"…", source); fits(short) {
			return short
		}
	}
	whole := []rune(joinCaption(desc, source))
	for cut := len(whole) - 1; cut > 0; cut-- {
		if fits(string(whole[:cut])) {
			return string(whole[:cut])
		}
	}
	return ""
}

// captionsOverLimit 正文块里超了这个平台图注上限的那几条（推之前最后核一遍；冻结过的内容拿来对现在的上限）。
func captionsOverLimit(profile distributionPlatform, blocks []Block) []string {
	var over []string
	for _, block := range blocks {
		if block.K == "cap" && !profile.captionFits(block.Text) {
			over = append(over, block.Text)
		}
	}
	return over
}
