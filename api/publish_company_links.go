package main

// publish_company_links.go — 正文公司库自动关联（2026-08-17 用户裁定；规则来源
// 飞书《FoodTalks 网站排版规范（新）》step2-(5) 公司库链接）：
//
//	1. 候选词：AI 预填 job.Tags 里的内容词（AI 只打「文章主体的品牌/公司名 + 品类词」，
//	   与规范「只有文章主体添加公司库」口径重合；栏目黑名单词先剔除）。
//	2. 检索：GET /company/companies/search（前台公开接口，2026-08-17 生产实测字段
//	   id/cnName/shortCnName/urlCode；文档记载的 /company/admin/companies/page 对
//	   服务账号 Access denied）。
//	3. 匹配：候选词与 cnName（全称）或 shortCnName（官方简称表，逗号分隔）规范化等值。
//	   多条命中优先 cnName 含「中国」的境内主体（规范：国内场景优先中国子公司）；
//	   仍歧义则放弃该词（宁缺勿错——规范要求无法判断时交人工确认）。
//	4. 注入：正文中该词首次出现处包 mc-company-link（新窗口，形态对齐 CMS「小工具箱」
//	   成品，见 news/48190 实测 DOM）；跳过标题、图注、已有链接内文本；每家公司只链
//	   一次（规范：首次出现加链接；同一名称一屏 0-1 个）。
//	5. relatedCompanyIds 同步写入命中公司 id（CMS 公司页「相关资讯」依赖此关联，
//	   规范明确普通超链接不产生关联，必须走关联字段）。
//
// 检索失败/无命中只降级（log WARN），不 fail job——公司关联是增强不是发布闸门。

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
)

// siteCompanyMatch 一个确认命中的公司与其在正文中的锚文本（即候选词本身）。
type siteCompanyMatch struct {
	Company foodTalksCompany
	Anchor  string
}

// publishSiteMaxCompanyLinks 每篇最多关联公司数。规范无硬上限，但要求「只加文章主体」；
// 候选来自 AI 标签（≤3），再设一道防线防异常输入。
const publishSiteMaxCompanyLinks = 3

// splitFoodTalksShortNames 拆简称表：官方数据以英文逗号为主，容错中文逗号/顿号。
func splitFoodTalksShortNames(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == '、'
	})
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// matchFoodTalksCompany 在检索结果中找候选词的精确归属：全称或官方简称规范化等值。
// 多条命中时优先境内主体（cnName 含「中国」）；仍多条则返回 nil（歧义交人工）。
func matchFoodTalksCompany(records []foodTalksCompany, word string) *foodTalksCompany {
	norm := normalizeFoodTalksSourceName(word)
	if norm == "" {
		return nil
	}
	var hits []foodTalksCompany
	for _, r := range records {
		if strings.TrimSpace(r.URLCode) == "" || r.ID <= 0 {
			continue // 没有前台地址段的记录构不成链接，跳过
		}
		if normalizeFoodTalksSourceName(r.CnName) == norm {
			hits = append(hits, r)
			continue
		}
		for _, s := range splitFoodTalksShortNames(r.ShortCnName) {
			if normalizeFoodTalksSourceName(s) == norm {
				hits = append(hits, r)
				break
			}
		}
	}
	if len(hits) == 1 {
		return &hits[0]
	}
	if len(hits) > 1 {
		var domestic []foodTalksCompany
		for _, h := range hits {
			if strings.Contains(h.CnName, "中国") {
				domestic = append(domestic, h)
			}
		}
		if len(domestic) == 1 {
			return &domestic[0]
		}
	}
	return nil
}

// siteCompanyMatchesFor 解析本篇应关联的公司：AI 标签逐词检索并精确匹配。
// 任何一步失败只记 WARN 并继续下一个词。
func (p *publishPipeline) siteCompanyMatchesFor(ctx context.Context, job publishJob) []siteCompanyMatch {
	if p == nil || p.foodTalks == nil {
		return nil
	}
	var words []string
	for _, t := range job.Tags {
		t = strings.TrimSpace(t)
		if t == "" || publishSiteTagBlocked(t) {
			continue
		}
		words = append(words, t)
	}
	if len(words) == 0 {
		return nil
	}
	ft, err := p.foodTalks()
	if err != nil {
		log.Printf("publish: 公司库检索不可用（任务 #%d）：%v", job.ID, err)
		return nil
	}
	var out []siteCompanyMatch
	seen := map[int64]bool{}
	for _, word := range words {
		if len(out) >= publishSiteMaxCompanyLinks {
			break
		}
		records, err := ft.SearchCompanies(ctx, word)
		if err != nil {
			log.Printf("publish: 公司库检索 %q 失败（任务 #%d）：%v", word, job.ID, err)
			continue
		}
		hit := matchFoodTalksCompany(records, word)
		if hit == nil {
			continue
		}
		if seen[hit.ID] {
			continue
		}
		seen[hit.ID] = true
		out = append(out, siteCompanyMatch{Company: *hit, Anchor: word})
	}
	return out
}

// publishSiteCompanyLinkForbiddenTags 注入禁区：这些元素内的文本不加公司链接
// （规范：标题处不添加；图注是来源说明；已有链接不能套链接）。
var publishSiteCompanyLinkForbiddenTags = map[string]bool{
	"a": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"figcaption": true,
}

// injectCompanyLinks 在正文 HTML 中为每个命中公司包 mc-company-link（每家只链首次
// 出现）。逐公司扫描：已注入的 <a> 自动成为下一轮禁区，词与词嵌套不会互相污染。
// 返回改写后的 HTML 与实际注入成功的公司 id 列表（正文找不到词的公司仍保留关联 id——
// CMS 关联与正文链接语义独立，规范里日报目录类正文不链但关联仍要建）。
func injectCompanyLinks(contentHTML string, matches []siteCompanyMatch) string {
	out := contentHTML
	for _, m := range matches {
		out = injectOneCompanyLink(out, m)
	}
	return out
}

func injectOneCompanyLink(contentHTML string, m siteCompanyMatch) string {
	anchor := strings.TrimSpace(m.Anchor)
	code := strings.TrimSpace(m.Company.URLCode)
	if anchor == "" || code == "" {
		return contentHTML
	}
	var b strings.Builder
	b.Grow(len(contentHTML) + 256)
	forbidden := map[string]int{} // 禁区元素嵌套深度
	forbiddenDepth := 0
	i, n := 0, len(contentHTML)
	injected := false
	for i < n {
		if contentHTML[i] == '<' {
			end := strings.IndexByte(contentHTML[i:], '>')
			if end < 0 {
				b.WriteString(contentHTML[i:])
				break
			}
			tag := contentHTML[i : i+end+1]
			name, closing := parseTagName(tag)
			if name != "" && publishSiteCompanyLinkForbiddenTags[name] {
				if closing {
					if forbidden[name] > 0 {
						forbidden[name]--
						forbiddenDepth--
					}
				} else if !strings.HasSuffix(tag, "/>") {
					forbidden[name]++
					forbiddenDepth++
				}
			}
			b.WriteString(tag)
			i += end + 1
			continue
		}
		next := strings.IndexByte(contentHTML[i:], '<')
		var text string
		if next < 0 {
			text = contentHTML[i:]
			i = n
		} else {
			text = contentHTML[i : i+next]
			i += next
		}
		if !injected && forbiddenDepth == 0 {
			if idx := strings.Index(text, anchor); idx >= 0 {
				b.WriteString(text[:idx])
				b.WriteString(companyLinkHTML(m))
				b.WriteString(text[idx+len(anchor):])
				injected = true
				continue
			}
		}
		b.WriteString(text)
	}
	return b.String()
}

// parseTagName 从 "<tag attr...>" / "</tag>" 里取小写标签名；注释/doctype 返回空。
func parseTagName(tag string) (name string, closing bool) {
	inner := strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">")
	inner = strings.TrimSpace(inner)
	if strings.HasPrefix(inner, "!") || strings.HasPrefix(inner, "?") {
		return "", false
	}
	if strings.HasPrefix(inner, "/") {
		closing = true
		inner = strings.TrimSpace(inner[1:])
	}
	for j := 0; j < len(inner); j++ {
		c := inner[j]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '/' {
			inner = inner[:j]
			break
		}
	}
	return strings.ToLower(inner), closing
}

// companyLinkHTML CMS「小工具箱」同款链接形态（news/48190 成品 DOM 逐属性对齐）：
// class/data-* 供前台识别公司关联，target=_blank 满足规范「一定要新窗口打开」。
func companyLinkHTML(m siteCompanyMatch) string {
	return fmt.Sprintf(
		`<a class="mc-company-link" title="%s" href="https://www.foodtalks.cn/company/%s" target="_blank" rel="noopener" data-id="%d" data-name="%s" data-code="%s" data-type="company">%s</a>`,
		html.EscapeString(m.Anchor),
		html.EscapeString(strings.TrimSpace(m.Company.URLCode)),
		m.Company.ID,
		html.EscapeString(strings.TrimSpace(m.Company.CnName)),
		html.EscapeString(strings.TrimSpace(m.Company.URLCode)),
		html.EscapeString(m.Anchor),
	)
}
