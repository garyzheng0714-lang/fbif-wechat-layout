package main

// publish_external_fidelity.go — 外发保真核对。
//
// 云梭不是原样转发正文，而是重新解析再生成平台格式（2026-10-20 拿 14 篇头条稿的 payload
// 与线上正文逐块比出来的，见 yunsuo_render.go）。这套「云梭的输入 → 平台上的样子」是推断的，
// 云梭随时可能改；所以每条 lane 推送成功、拿到文章链接后，取一次平台上的正文，与这条 lane
// 冻结的 payload 比结构：小标题（含整段加粗）几处、图片几张、带图注的图片几张、文末转载
// 声明在不在、正文开头是不是多出一张封面图。对不上就在审核卡这个平台的行里写一句灰字，
// GET /api/publish/monitoring 给 warning（不是 P0），云梭行为一变当天就能发现。
//
// 只核一次，结果记在 lane 上，不重复取；这是外发流程本身对自家文章的一次读取，不是健康
// 探针（红线 30）。头条号用 m.toutiao.com 的文章接口，免费、不走 TikHub；百家号有验证码，
// 不取、不绕——百家号经自己的分发服务存草稿，分发服务存完当场读回核对，结果直接记进同一个
// Fidelity（publish_external_robot.go），这里不取。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	externalFidelityFetchToutiao = "toutiao_info"

	defaultToutiaoArticleInfoBase = "https://m.toutiao.com"
	toutiaoArticleInfoMaxBytes    = 8 << 20
	externalFidelityMaxAttempts   = 3
	externalFidelityMaxPerSweep   = 5
	// 对不上或一直取不到的，monitoring 报 7 天。
	externalFidelityWarnFor = 7 * 24 * time.Hour

	// 2026-10-20 之前推的稿用的是已知会丢小标题与图注的旧写法（f60e572 已改），不核对。
	externalFidelitySince = "2026-10-19T16:00:00Z"
)

// toutiaoArticleInfoBase 测试可替换成本地桩。
var toutiaoArticleInfoBase = defaultToutiaoArticleInfoBase

// externalFidelityRetryAfter 取不到时（平台还在审、网络错）第 n 次重试距上次至少隔多久。
var externalFidelityRetryAfter = []time.Duration{0, 30 * time.Minute, 2 * time.Hour}

// externalFidelityShape 一篇正文看上去的结构。
type externalFidelityShape struct {
	// Headings 小标题与整段加粗的段（平台上两者都是加粗段）。
	Headings        int  `json:"headings"`
	Images          int  `json:"images"`
	CaptionedImages int  `json:"captioned_images"`
	Statement       bool `json:"statement"` // 「转载请联系授权」声明
	// LeadingImages 正文开头、第一段字之前连着几张图。
	LeadingImages int `json:"leading_images"`
}

// publishExternalFidelity 一条 lane 的核对结果。CheckedAt 有值就是核过了，不再取。
type publishExternalFidelity struct {
	CheckedAt   string                `json:"checked_at,omitempty"`
	Attempts    int                   `json:"attempts,omitempty"`
	LastTriedAt string                `json:"last_tried_at,omitempty"`
	Error       string                `json:"error,omitempty"`
	Expected    externalFidelityShape `json:"expected,omitzero"`
	Online      externalFidelityShape `json:"online,omitzero"`
	// Note 对不上的地方（「平台上小标题少了 1 处」）；空表示一致。
	Note string `json:"note,omitempty"`
}

// fetchToutiaoArticleContent 测试可替换；生产走 m.toutiao.com 文章接口。
var fetchToutiaoArticleContent = defaultFetchToutiaoArticleContent

// fetchExternalArticleContent 按平台规则取线上正文 HTML。
func fetchExternalArticleContent(ctx context.Context, profile yunsuoPlatformProfile, articleURL string) (string, error) {
	switch profile.ContentFetch {
	case externalFidelityFetchToutiao:
		match := toutiaoGroupIDRe.FindStringSubmatch(articleURL)
		if match == nil {
			return "", fmt.Errorf("认不出头条文章 id：%s", articleURL)
		}
		return fetchToutiaoArticleContent(ctx, match[1])
	default:
		return "", fmt.Errorf("%s不取线上正文", profile.Name)
	}
}

func defaultFetchToutiaoArticleContent(ctx context.Context, groupID string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	reqCtx, cancel := context.WithTimeout(ctx, tikhubRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, toutiaoArticleInfoBase+"/i"+groupID+"/info/v2/", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("读头条正文失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := readBoundedBody(resp.Body, toutiaoArticleInfoMaxBytes)
	if err != nil {
		return "", fmt.Errorf("读头条正文失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("读头条正文失败：HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("头条正文接口返回的不是 JSON：%w", err)
	}
	if strings.TrimSpace(parsed.Data.Content) == "" {
		return "", errors.New("头条正文接口没有返回正文")
	}
	return parsed.Data.Content, nil
}

// externalFidelityShapeOf 数正文的结构，只按「看上去是什么」数：推给云梭的写法是 <h2>/<h3>、
// <p><img></p> 后跟 <p><small>图注</small></p>；头条线上是 <p><strong>整段加粗</strong></p>、
// <div class="pgc-img"><img><p class="pgc-img-caption">图注</p></div>。两种写法数出来可以直接比。
func externalFidelityShapeOf(raw string) externalFidelityShape {
	var shape externalFidelityShape
	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return shape
	}
	blocks := externalFidelityBlocks(nodes)
	textSeen := false
	for i, block := range blocks {
		if images := externalFidelityCount(block, "img"); images > 0 {
			shape.Images += images
			if !textSeen {
				shape.LeadingImages += images
			}
			if externalFidelityInnerCaption(block) || (i+1 < len(blocks) && externalFidelitySmallCaption(blocks[i+1])) {
				shape.CaptionedImages++
			}
			continue
		}
		text := strings.TrimSpace(externalFidelityText(block))
		if text == "" || externalFidelitySmallCaption(block) {
			continue
		}
		textSeen = true
		if strings.Contains(text, "转载请联系授权") {
			shape.Statement = true
		}
		if externalFidelityHeading(block) {
			shape.Headings++
		}
	}
	return shape
}

// externalFidelityBlocks 正文的段落序列：普通 div/section 是外壳，往里拆；图片容器
// （class 含 pgc-img 的 div）整个算一段。
func externalFidelityBlocks(nodes []*html.Node) []*html.Node {
	var out []*html.Node
	for _, n := range nodes {
		switch {
		case n.Type == html.TextNode && strings.TrimSpace(n.Data) == "":
		case n.Type != html.ElementNode && n.Type != html.TextNode:
		case n.Type == html.ElementNode && (n.Data == "div" || n.Data == "section" || n.Data == "article") &&
			!strings.Contains(externalFidelityAttr(n, "class"), "pgc-img"):
			var children []*html.Node
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				children = append(children, c)
			}
			out = append(out, externalFidelityBlocks(children)...)
		default:
			out = append(out, n)
		}
	}
	return out
}

func externalFidelityAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func externalFidelityCount(n *html.Node, tag string) int {
	count := 0
	if n.Type == html.ElementNode && n.Data == tag {
		count++
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		count += externalFidelityCount(c, tag)
	}
	return count
}

func externalFidelityText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(externalFidelityText(c))
	}
	return b.String()
}

// externalFidelityInnerCaption 图片容器里有字的图注（头条 pgc-img-caption）。
func externalFidelityInnerCaption(n *html.Node) bool {
	if n.Type == html.ElementNode && strings.Contains(externalFidelityAttr(n, "class"), "pgc-img-caption") {
		return strings.TrimSpace(externalFidelityText(n)) != ""
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if externalFidelityInnerCaption(c) {
			return true
		}
	}
	return false
}

// externalFidelitySmallCaption 紧跟图片的独立图注段：字全在 <small> 里（推给云梭的写法）。
func externalFidelitySmallCaption(n *html.Node) bool {
	return n.Type == html.ElementNode && externalFidelityWholly(n, func(e *html.Node) bool { return e.Data == "small" })
}

// externalFidelityHeading 小标题，或字全是加粗的段。
func externalFidelityHeading(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}
	switch n.Data {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	}
	return externalFidelityWholly(n, func(e *html.Node) bool { return e.Data == "strong" || e.Data == "b" })
}

// externalFidelityWholly 段里有字，且每个字都在 match 的元素里。
func externalFidelityWholly(n *html.Node, match func(*html.Node) bool) bool {
	seen, all := false, true
	var walk func(*html.Node, bool)
	walk = func(c *html.Node, inside bool) {
		if c.Type == html.TextNode {
			if strings.TrimSpace(c.Data) != "" {
				seen = true
				all = all && inside
			}
			return
		}
		if c.Type == html.ElementNode && match(c) {
			inside = true
		}
		for child := c.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inside)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, false)
	}
	return seen && all
}

// externalFidelityExpected 这条 lane 推出去的正文在平台上应有的结构：云梭会把封面插到正文
// 最前面的平台（CoverPrepended）多一张开头的图。
func externalFidelityExpected(payloadHTML string, profile yunsuoPlatformProfile) externalFidelityShape {
	shape := externalFidelityShapeOf(payloadHTML)
	if profile.CoverPrepended {
		shape.Images++
		shape.LeadingImages++
	}
	return shape
}

// externalFidelityCompare 平台上与推出去的哪里对不上，写成一句话；一致返回空串。
func externalFidelityCompare(expected, online externalFidelityShape) string {
	var parts []string
	extraLead := max(online.LeadingImages-expected.LeadingImages, 0)
	switch {
	case extraLead == 1:
		parts = append(parts, "开头多了一张封面图")
	case extraLead > 1:
		parts = append(parts, fmt.Sprintf("开头多了 %d 张图", extraLead))
	}
	diff := func(name string, delta int, unit string) {
		switch {
		case delta < 0:
			parts = append(parts, fmt.Sprintf("%s少了 %d %s", name, -delta, unit))
		case delta > 0:
			parts = append(parts, fmt.Sprintf("%s多了 %d %s", name, delta, unit))
		}
	}
	diff("小标题", online.Headings-expected.Headings, "处")
	diff("图片", online.Images-extraLead-expected.Images, "张")
	diff("图注", online.CaptionedImages-expected.CaptionedImages, "处")
	if expected.Statement && !online.Statement {
		parts = append(parts, "文末转载声明没了")
	}
	if len(parts) == 0 {
		return ""
	}
	return "平台上" + strings.Join(parts, "、")
}

// externalLaneNeedsFidelity 系统推送成功、有文章链接和冻结的 payload、平台能取正文、还没核过、
// 没取满次数，且距上次失败已过重试间隔。
func externalLaneNeedsFidelity(lane publishExternalLane, profile yunsuoPlatformProfile, now time.Time) bool {
	if lane.Driver != externalDriverYunsuo || lane.State != publishExternalDistributionSucceeded || strings.TrimSpace(lane.URL) == "" ||
		strings.TrimSpace(lane.PayloadJSON) == "" || profile.ContentFetch == "" {
		return false
	}
	check := lane.Fidelity
	if check.CheckedAt != "" || check.Attempts >= externalFidelityMaxAttempts {
		return false
	}
	requested, err := time.Parse(time.RFC3339, lane.RequestedAt)
	since, _ := time.Parse(time.RFC3339, externalFidelitySince)
	if err != nil || requested.Before(since) {
		return false
	}
	if last, err := time.Parse(time.RFC3339, check.LastTriedAt); err == nil {
		return now.Sub(last) >= externalFidelityRetryAfter[min(check.Attempts, len(externalFidelityRetryAfter)-1)]
	}
	return true
}

// externalLaneFidelityNote 卡片与侧边栏的一句：平台上对不上的地方（经分发服务的草稿见 robotPlatformState）。
func externalLaneFidelityNote(lane publishExternalLane) string {
	if lane.State != publishExternalDistributionSucceeded || lane.Fidelity.CheckedAt == "" {
		return ""
	}
	return lane.Fidelity.Note
}

// refreshExternalDistributionFidelity 兜底循环里核对拿到文章链接的外发稿；每轮最多几篇。
// 对不上就刷卡。
func (p *publishPipeline) refreshExternalDistributionFidelity() {
	if p == nil || p.store == nil || publishWorkersPaused() {
		return
	}
	cfg, err := loadExternalTargets()
	if err != nil {
		return
	}
	jobs, err := p.store.all()
	if err != nil {
		log.Printf("publish: 外发保真核对读取任务失败：%v", err)
		return
	}
	now := pipelineNow(p)
	stamp := now.Format(time.RFC3339)
	budget := externalFidelityMaxPerSweep
	for _, job := range jobs {
		for _, lane := range job.ExternalDistributionLanes {
			if budget <= 0 {
				return
			}
			target, ok := cfg.target(lane.AccountID)
			if !ok {
				continue
			}
			profile := yunsuoProfileForTarget(target)
			if !externalLaneNeedsFidelity(lane, profile, now) {
				continue
			}
			budget--
			var online string
			payloadHTML, fetchErr := externalLaneFrozenHTML(lane)
			if fetchErr == nil {
				online, fetchErr = fetchExternalArticleContent(context.Background(), profile, lane.URL)
			}
			result := lane.Fidelity
			result.Attempts++
			result.LastTriedAt = stamp
			if fetchErr != nil {
				result.Error = fetchErr.Error()
			} else {
				result.CheckedAt, result.Error = stamp, ""
				result.Expected = externalFidelityExpected(payloadHTML, profile)
				result.Online = externalFidelityShapeOf(online)
				result.Note = externalFidelityCompare(result.Expected, result.Online)
			}
			_, swapped, updateErr := p.updateExternalLane(job.ID, lane.AccountID, func(current publishExternalLane) bool {
				return current.State == lane.State && current.URL == lane.URL && current.Fidelity == lane.Fidelity
			}, func(current *publishJob, l *publishExternalLane) {
				l.Fidelity = result
				if result.Note != "" {
					enqueueExternalDistributionCardUpdate(current)
				}
			})
			switch {
			case updateErr != nil:
				log.Printf("publish: 外发保真核对落盘失败（任务 #%d %s）：%v", job.ID, lane.Name, updateErr)
			case !swapped:
			case fetchErr != nil:
				log.Printf("publish: 外发保真核对取不到线上正文（任务 #%d %s，第 %d 次）：%v", job.ID, lane.Name, result.Attempts, fetchErr)
			case result.Note != "":
				log.Printf("publish: 外发保真核对对不上（任务 #%d %s）：%s", job.ID, lane.Name, result.Note)
				p.scheduleExternalDistributionCardUpdate(job.ID)
			}
		}
	}
}

// externalFidelityMonitoring 7 天内核对出对不上的，与取满次数仍取不到线上正文的 lane 数。
func externalFidelityMonitoring(job publishJob, now time.Time) (mismatched, unavailable int) {
	for _, lane := range job.ExternalDistributionLanes {
		check := lane.Fidelity
		switch {
		case check.CheckedAt != "" && check.Note != "" && externalFidelityRecent(now, check.CheckedAt):
			mismatched++
		case check.CheckedAt == "" && check.Attempts >= externalFidelityMaxAttempts && externalFidelityRecent(now, check.LastTriedAt):
			unavailable++
		}
	}
	return mismatched, unavailable
}

func externalFidelityRecent(now time.Time, value string) bool {
	at, err := time.Parse(time.RFC3339, value)
	return err == nil && now.Sub(at) <= externalFidelityWarnFor
}
