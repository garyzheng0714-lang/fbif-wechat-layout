package main

// 网站发布增强（2026-08-14 用户裁定，金标准 news/62765 正确版）：
//  1. 目录：Mote/原创文章一级标题（h2）数 > 5 时，正文 h2 注入 mctoc_* 锚点 id，
//     CMS payload 的 toc 字段写 [{id,level:1,title}] JSON——FoodTalks 前台 toc-menu
//     只认这个字段，API 直发的文章后台不会自己生成（渲染器侧 mctoc 仍保持关闭，
//     锚点在发布组装层注入，保证 render output_hash 可复现）。
//  2. 标签：Mote 文章挂「Mote莫特」+ 正文声明的内容系列标签（如「这是我茶叶系列的
//     第29篇」→「茶叶系列」）。标签只在 tag_type_id=6 新闻标签库中按名精确解析，
//     不自动创建，解析不到就跳过——宁缺勿错，不往全局标签库里写正则抽取物。

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"regexp"
	"strings"
)

// ---- 目录（toc） ----

const publishSiteTocMinHeadings = 6 // 一级标题数 > 5 才生成目录

var publishSiteH2Re = regexp.MustCompile(`(?is)<h2(\s[^>]*)?>(.*?)</h2>`)
var publishSiteTagStripRe = regexp.MustCompile(`(?s)<[^>]*>`)

type publishSiteTocEntry struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	Title string `json:"title"`
}

// publishSiteTocForContent 给正文 h2 注入确定性锚点并生成 toc JSON。
// 锚点 id 由正文内容哈希派生：同一份正文重复组装（staging→publicize→repair）产出
// 字节一致的 content 与 toc，PUT 全量覆盖语义下不会来回抖动。
// 不满足条件（h2 ≤ 5、或 h2 已带 id）时原文返回、toc 为空串。
func publishSiteTocForContent(contentHTML string) (string, string) {
	matches := publishSiteH2Re.FindAllStringSubmatchIndex(contentHTML, -1)
	if len(matches) < publishSiteTocMinHeadings {
		return contentHTML, ""
	}
	sum := sha256.Sum256([]byte(contentHTML))
	seed := fmt.Sprintf("%x", sum[:4]) // 8 个十六进制字符，足够避免同文重复
	var out strings.Builder
	entries := make([]publishSiteTocEntry, 0, len(matches))
	prev := 0
	for i, m := range matches {
		attrRaw := ""
		if m[2] >= 0 {
			attrRaw = contentHTML[m[2]:m[3]]
		}
		inner := contentHTML[m[4]:m[5]]
		title := strings.TrimSpace(html.UnescapeString(publishSiteTagStripRe.ReplaceAllString(inner, "")))
		if strings.Contains(attrRaw, "id=") || title == "" {
			// 已有锚点或空标题：保守起见整篇放弃注入，避免半套目录
			return contentHTML, ""
		}
		id := fmt.Sprintf("mctoc_%s%d", seed, i)
		out.WriteString(contentHTML[prev:m[0]])
		out.WriteString(`<h2 id="` + id + `"` + attrRaw + ">")
		out.WriteString(inner)
		out.WriteString("</h2>")
		prev = m[1]
		entries = append(entries, publishSiteTocEntry{ID: id, Level: 1, Title: title})
	}
	out.WriteString(contentHTML[prev:])
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

// ---- 新闻标签解析（tag_type_id=6，只解析不创建） ----

const foodTalksTagTypeNews int64 = 6 // CMS 新闻标签库（newsTags.tagId 必须来自这里，契约 §9）

const publishSiteMoteTagName = "Mote莫特"

const publishSiteMaxNewsTags = 3 // 7 月 256 篇实证：编辑习惯每篇 1-2 个标签，最多 3

// siteNewsTagsFor 文章自动标签（2026-08-14 用户裁定「标签都要改」+ 7 月实证）：
// Mote 文章＝「Mote莫特」+ 正文声明的内容系列（与运营 7 月给 Mote 稿手工挂
// Mote莫特/茶叶系列的习惯一致）；所有文章再叠加 AI 预填标签（job.Tags，品牌/
// 品类关键词，与 7 月高频标签「瑞幸咖啡/零食/新茶饮」同型）。
// 只按名字在新闻标签库（tag_type_id=6）精确解析，解析不到就跳过；不自动创建，
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
		if t = strings.TrimSpace(t); t != "" {
			wanted = append(wanted, t)
		}
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
