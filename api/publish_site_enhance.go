package main

// 网站发布增强（2026-08-14 用户裁定，金标准 news/62765；2026-08-17 按排版规范修订）：
//  1. 目录：所有文章（不分原创/转载，2026-08-17 用户裁定）正文非空白字符 ≥3000 且
//     h2+h3 合计 ≥5 时，正文一级 h2 注入 mctoc_* 锚点 id，CMS payload 的 toc 字段写
//     [{id,level:1,title}] JSON——FoodTalks 前台 toc-menu 只认这个字段，API 直发的
//     文章后台不会自己生成（渲染器侧 mctoc 仍保持关闭，锚点在发布组装层注入，
//     保证 render output_hash 可复现）。二级标题（（一）/ 1、2、3）不进 toc；
//     Mote 文章例外，二级以 level=2 进 toc（2026-09-29 用户裁定）。
//  2. 标签：所有文章解析 AI 预填标签；Mote 文章优先挂「Mote莫特」+ 正文声明的
//     内容系列标签（如「这是我茶叶系列的第29篇」→「茶叶系列」）。标签只在
//     tag_type_id=6 新闻标签库中按规范化名称等值解析，总数最多 3 个；不自动创建；
//     栏目/活动标签黑名单双侧拦截（publishSiteTagBlocked）。
//  3. 系列标签链接：只把开头「这是我 XX 系列（的）第 N 篇」声明中的系列名
//     链到已解析的 FoodTalks 标签页；后文同词不加链。
//  4. Mote 系列链接：网站发布组装时把正文公众号互推链换成已发布站内稿
//     （见 publish_mote_series_links.go；金标准 62285 Tazo→/news/62236）。

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"regexp"
	"strings"
	"unicode"
)

// ---- 目录（toc） ----

// 触发口径对齐网站排版规范（2026-08-17，飞书《FoodTalks 网站排版规范（新）》step2-3）：
// 「文章字数超 3000 且一二级标题 5 个及以上」→ 正文纯文本 ≥ 3000 字 且 h2+h3 合计 ≥ 5。
// 旧口径只数 h2 且要 >5，运营反馈原创长文经常不出目录。目录条目默认只收一级 h2
// （62765 金标准；2026-08-24：「（一）」与「1、2、3」是二级，不进目录）。
// Mote 文章例外（2026-09-29 用户裁定，63116 小罐茶）：二级标题也进目录，level=2，
// FoodTalks 前台 toc-menu 按 .level-2 缩进小圆点显示。
const publishSiteTocMinHeadings = 5 // 一二级标题（h2+h3）合计 ≥ 5
const publishSiteTocMinChars = 3000 // 正文纯文本（剥标签）字符数下限

var publishSiteHeadingRe = regexp.MustCompile(`(?is)<(h[23])(\s[^>]*)?>(.*?)</h[23]>`)
var publishSiteTagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)

type publishSiteTocEntry struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	Title string `json:"title"`
}

// publishSiteTocForContent 给正文标题注入确定性锚点并生成 toc JSON。
// 锚点 id 由正文内容哈希派生：同一份正文重复组装（staging→publicize→repair）产出
// 字节一致的 content 与 toc，PUT 全量覆盖语义下不会来回抖动。
// withSecondary=false 只收一级（h2 且非「1、」「（一）」开头）；true 时二级（h3，或误渲成
// h2 的二级序号句）也以 level=2 进目录。
// 不满足条件（字数 < 3000、h2+h3 < 5、无 h2、或标题已带 id）时原文返回、toc 为空串。
func publishSiteTocForContent(contentHTML string, withSecondary bool) (string, string) {
	matches := publishSiteHeadingRe.FindAllStringSubmatchIndex(contentHTML, -1)
	h2Count := 0
	for _, m := range matches {
		if strings.EqualFold(contentHTML[m[2]:m[3]], "h2") {
			h2Count++
		}
	}
	if h2Count == 0 || len(matches) < publishSiteTocMinHeadings {
		return contentHTML, ""
	}
	plain := html.UnescapeString(publishSiteTagStripRe.ReplaceAllString(contentHTML, ""))
	charCount := 0
	for _, r := range plain {
		if !unicode.IsSpace(r) {
			charCount++
		}
	}
	if charCount < publishSiteTocMinChars {
		return contentHTML, ""
	}
	sum := sha256.Sum256([]byte(contentHTML))
	seed := fmt.Sprintf("%x", sum[:4]) // 8 个十六进制字符，足够避免同文重复
	var out strings.Builder
	entries := make([]publishSiteTocEntry, 0, len(matches))
	prev := 0
	anchorN := 0
	for _, m := range matches {
		tag := strings.ToLower(contentHTML[m[2]:m[3]])
		attrRaw := ""
		if m[4] >= 0 {
			attrRaw = contentHTML[m[4]:m[5]]
		}
		inner := contentHTML[m[6]:m[7]]
		title := strings.TrimSpace(html.UnescapeString(publishSiteTagStripRe.ReplaceAllString(inner, "")))
		level := 1
		if tag == "h3" || isSiteSecondaryHeadingText(title) {
			// 二级标题即使被渲成 h2 也按二级处理（62873「2、第二大难点」）。
			level = 2
		}
		if level == 2 && !withSecondary {
			continue
		}
		if strings.Contains(attrRaw, "id=") || title == "" {
			// 已有锚点或空标题：保守起见整篇放弃注入，避免半套目录
			return contentHTML, ""
		}
		id := fmt.Sprintf("mctoc_%s%d", seed, anchorN)
		anchorN++
		out.WriteString(contentHTML[prev:m[0]])
		out.WriteString("<" + tag + ` id="` + id + `"` + attrRaw + ">")
		out.WriteString(inner)
		out.WriteString("</" + tag + ">")
		prev = m[1]
		entries = append(entries, publishSiteTocEntry{ID: id, Level: level, Title: title})
	}
	out.WriteString(contentHTML[prev:])
	if len(entries) == 0 {
		return contentHTML, ""
	}
	tocJSON, err := json.Marshal(entries)
	if err != nil {
		return contentHTML, ""
	}
	return out.String(), string(tocJSON)
}

// ---- Mote / 内容系列识别 ----

func publishSiteNormalizeAuthor(v string) string {
	v = strings.ToLower(v)
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '　', ' ':
			return -1
		}
		return r
	}, v)
}

var publishSiteAuthorLineRe = regexp.MustCompile(`^(?:作者|撰文|撰稿|文)\s*[:：]\s*(.+)$`)

// publishSiteIsMoteArticle 判定 Mote 文章：任务作者字段或正文作者行（作者：Mote莫特）
// 命中 mote/莫特。与公众号规则 13 的 is_mote_author 同口径（includes mote|莫特）。
func publishSiteIsMoteArticle(job publishJob) bool {
	if v := publishSiteNormalizeAuthor(job.Author); v != "" &&
		(strings.Contains(v, "mote") || strings.Contains(v, "莫特")) {
		return true
	}
	var blocks []Block
	if err := json.Unmarshal([]byte(job.BlocksJSON), &blocks); err != nil {
		return false
	}
	for _, block := range blocks {
		if block.K == "cap" || block.K == "img" || block.K == "video" || block.K == "table" {
			continue
		}
		line := strings.TrimSpace(publishExternalDistributionBlockText(block))
		m := publishSiteAuthorLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		v := publishSiteNormalizeAuthor(m[1])
		if strings.Contains(v, "mote") || strings.Contains(v, "莫特") {
			return true
		}
	}
	return false
}

var publishSiteSeriesRe = regexp.MustCompile(`系列的?第[0-9零一二三四五六七八九十百两]+篇`)

// publishSiteSeriesNameStopRunes 系列名向前收集时的停止字符（助词/常见叙述动词/标点边界）。
var publishSiteSeriesNameStopRunes = map[rune]bool{
	'的': true, '我': true, '在': true, '是': true, '写': true, '了': true,
	'看': true, '读': true, '篇': true,
}

// publishSiteSeriesNames 从正文声明里抽内容系列名：「这是我茶叶系列的第29篇」→「茶叶系列」。
// 从「系列第N篇」锚点向前收集连续的汉字/字母/数字（跳过助词与叙述动词），名称至少 2 字。
// 返回按出现顺序去重的列表。
func publishSiteSeriesNames(blocksJSON string) []string {
	var blocks []Block
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, block := range blocks {
		if block.K == "cap" || block.K == "img" || block.K == "video" || block.K == "table" {
			continue
		}
		text := publishExternalDistributionBlockText(block)
		if text == "" || !strings.Contains(text, "系列") {
			continue
		}
		runes := []rune(text)
		for _, loc := range publishSiteSeriesRe.FindAllStringIndex(text, -1) {
			// loc 是字节偏移，转 rune 下标
			start := len([]rune(text[:loc[0]]))
			var nameRunes []rune
			for i := start - 1; i >= 0; i-- {
				r := runes[i]
				if publishSiteSeriesNameStopRunes[r] {
					break
				}
				if !isSeriesNameRune(r) {
					break
				}
				nameRunes = append([]rune{r}, nameRunes...)
				if len(nameRunes) >= 10 {
					break
				}
			}
			if len(nameRunes) < 2 {
				continue
			}
			name := string(nameRunes) + "系列"
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	return names
}

func isSeriesNameRune(r rune) bool {
	if r >= 0x4e00 && r <= 0x9fff {
		return true
	}
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	return r == '·'
}

var publishSiteOpeningSeriesRe = regexp.MustCompile(`^这是我([\p{Han}A-Za-z0-9·]{2,10}系列)(?:的)?第[0-9零〇一二三四五六七八九十百千两]+篇[。.]?$`)

// 系列名在公众号原文里常已链到公众号合集（63116 小罐茶「茶叶系列」→ appmsgalbum），
// 此时组 1 是整个既有 <a>、组 2 是其中的名字；纯文本时组 3 是名字。两种都整段替换成标签链。
var publishSiteOpeningSeriesParagraphRe = regexp.MustCompile(`(?is)<p\b[^>]*>\s*这是我(?:(<a\b[^>]*>\s*([\p{Han}A-Za-z0-9·]{2,10}系列)\s*</a>)|([\p{Han}A-Za-z0-9·]{2,10}系列))(?:的)?第[0-9零〇一二三四五六七八九十百千两]+篇[。.]?\s*</p>`)

func publishSiteOpeningSeriesCandidateText(block Block) string {
	if block.K != "txt" {
		return ""
	}
	text := strings.TrimSpace(publishExternalDistributionBlockText(block))
	if publishSiteAuthorLineRe.MatchString(text) {
		return ""
	}
	return text
}

// publishSiteOpeningSeriesName 只读开头首个正文段；署名、空段和头图不算正文。
// 若普通正文已经开始，后面再出现「XX 系列第 N 篇」也不认。
func publishSiteOpeningSeriesName(blocksJSON string) string {
	var blocks []Block
	if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
		return ""
	}
	for _, block := range blocks {
		text := publishSiteOpeningSeriesCandidateText(block)
		if text == "" {
			continue
		}
		match := publishSiteOpeningSeriesRe.FindStringSubmatch(text)
		if match == nil {
			return ""
		}
		return match[1]
	}
	return ""
}

func publishSiteNewsTagID(tags []foodTalksNewsTag, name string) int64 {
	want := normalizeFoodTalksSourceName(name)
	for _, tag := range tags {
		if tag.TagID > 0 && normalizeFoodTalksSourceName(tag.TagName) == want {
			return tag.TagID
		}
	}
	return 0
}

// publishSiteLinkOpeningSeriesTag 由开头首个正文段确定系列名；该名字的每个「这是我 XX 系列
// （的）第 N 篇」独立声明段都链到标签页——Mote 文末常重复一次同一声明并链公众号合集
// （63149 T9 茶，2026-10-08 用户反馈），也要换成标签链。散落在正文句子里的系列名不加链。
func publishSiteLinkOpeningSeriesTag(contentHTML, blocksJSON string, tags []foodTalksNewsTag) string {
	name := publishSiteOpeningSeriesName(blocksJSON)
	if name == "" {
		return contentHTML
	}
	tagID := publishSiteNewsTagID(tags, name)
	if tagID == 0 {
		return contentHTML
	}
	link := fmt.Sprintf(`<a href="%s/news/tag/%d" target="_blank" rel="noopener">%s</a>`, escapeHTMLAttr(siteFrontURL()), tagID, html.EscapeString(name))
	var b strings.Builder
	last := 0
	for _, match := range publishSiteOpeningSeriesParagraphRe.FindAllStringSubmatchIndex(contentHTML, -1) {
		// 替换区间：既有 <a> 整体（组 1，名字在组 2），或纯文本名字（组 3）。
		start, end, nameStart, nameEnd := match[6], match[7], match[6], match[7]
		if match[2] >= 0 {
			start, end, nameStart, nameEnd = match[2], match[3], match[4], match[5]
		}
		if contentHTML[nameStart:nameEnd] != name {
			continue
		}
		b.WriteString(contentHTML[last:start])
		b.WriteString(link)
		last = end
	}
	if last == 0 {
		return contentHTML
	}
	b.WriteString(contentHTML[last:])
	return b.String()
}

// ---- 新闻标签解析（tag_type_id=6，只解析不创建） ----

const foodTalksTagTypeNews int64 = 6 // CMS 新闻标签库（newsTags.tagId 必须来自这里，契约 §9）

// 栏目/活动类标签黑名单（排版规范：「不添加首发栏目为标签」；编辑规范 §4：栏目·活动
// 标签由编辑手工体系管理，AI 不打）。只拦纯栏目/活动专名——品类词（乳品/饮料/咖啡…）
// 是合法标签不拦。词表注入 prompt 前与发布解析时双侧过滤（2026-08-17 运营反馈）。
var publishSiteColumnTagNames = map[string]bool{
	"快讯": true, "热门关注": true, "深度专题": true, "创新周报": true,
	"食品新闻": true, "独家专栏": true, "品类动态": true, "行业观点": true,
	"企业资讯": true, "新探": true, "食装": true, "有料": true, "设记": true,
	"酒零后": true, "马克兔": true, "vfood": true,
}

var publishSiteColumnTagPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^FBIF\s*\d{4}`), // FBIF2026 / FBIF 2026 / FBIF2026会后报告 / FBIF2026参展企业
	regexp.MustCompile(`会后报告`),
	regexp.MustCompile(`参展企业`),
	regexp.MustCompile(`(?i)Wow\s*Food\s*Awards?`),
	regexp.MustCompile(`Wow食品创新奖`),
}

// publishSiteTagBlocked 判定标签名是否属于栏目/活动专名（不应作为文章内容标签）。
func publishSiteTagBlocked(name string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		return true
	}
	if publishSiteColumnTagNames[trimmed] {
		return true
	}
	for _, re := range publishSiteColumnTagPatterns {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

const publishSiteMoteTagName = "Mote莫特"

const publishSiteMaxNewsTags = 3 // 7 月 256 篇实证：编辑习惯每篇 1-2 个标签，最多 3

// siteNewsTagsFor 文章自动标签（2026-08-14 用户裁定「标签都要改」+ 7 月实证）：
// Mote 文章＝「Mote莫特」+ 正文声明的内容系列（与运营 7 月给 Mote 稿手工挂
// Mote莫特/茶叶系列的习惯一致）；所有文章再叠加 AI 预填标签（job.Tags，品牌/
// 品类关键词，与 7 月高频标签「瑞幸咖啡/零食/新茶饮」同型）。
// 只按规范化名称在新闻标签库（tag_type_id=6）等值解析，解析不到就跳过；不自动创建，
// 避免把模型输出/正则抽取物写进全局标签库。总数上限 3。
func (p *publishPipeline) siteNewsTagsFor(ctx context.Context, job publishJob) []foodTalksNewsTag {
	if p == nil || p.foodTalks == nil {
		return nil
	}
	var wanted []string
	if publishSiteIsMoteArticle(job) {
		wanted = append([]string{publishSiteMoteTagName}, publishSiteSeriesNames(job.BlocksJSON)...)
	}
	for _, t := range job.Tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		// AI 标签过栏目/活动黑名单；Mote/系列标签不经过此过滤（上方显式添加）
		if publishSiteTagBlocked(t) {
			log.Printf("publish: AI 标签 %q 是栏目/活动名（任务 #%d），按规范跳过", t, job.ID)
			continue
		}
		wanted = append(wanted, t)
	}
	if len(wanted) == 0 {
		return nil
	}
	ft, err := p.foodTalks()
	if err != nil {
		log.Printf("publish: 新闻标签解析不可用（任务 #%d）：%v", job.ID, err)
		return nil
	}
	var out []foodTalksNewsTag
	seen := map[int64]bool{}
	seenName := map[string]bool{}
	for _, name := range wanted {
		if len(out) >= publishSiteMaxNewsTags {
			break
		}
		norm := normalizeFoodTalksSourceName(name)
		if seenName[norm] {
			continue
		}
		seenName[norm] = true
		tags, err := ft.ListTagsByType(ctx, foodTalksTagTypeNews, name)
		if err != nil {
			log.Printf("publish: 查询新闻标签 %q 失败（任务 #%d）：%v", name, job.ID, err)
			continue
		}
		id := matchFoodTalksSourceID(tags, name)
		if id == 0 {
			log.Printf("publish: 新闻标签 %q 不在 CMS 标签库（任务 #%d），跳过不自动创建", name, job.ID)
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, foodTalksNewsTag{TagID: id, TagName: name})
	}
	return out
}
