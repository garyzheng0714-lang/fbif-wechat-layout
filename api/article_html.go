package main

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// resolveContentHTML 按 WeChat js_content → <article> → <main> → 整页兜底的
// 顺序取出文章正文 HTML。WeChat 文章页正文在 js_content div；非 WeChat 页退到
// 语义标签；都取不到时返回 rawHTML，由调用方自己 strip。
func resolveContentHTML(rawHTML string) string {
	contentHTML := extractJsContent(rawHTML)
	if contentHTML == "" {
		// Not a WeChat page, try <article> or <main>
		contentHTML = extractTag(rawHTML, "article")
		if contentHTML == "" {
			contentHTML = extractTag(rawHTML, "main")
		}
		if contentHTML == "" {
			// Last resort: hand back the whole page
			contentHTML = rawHTML
		}
	}
	return contentHTML
}

func extractMeta(rawHTML, property string) string {
	target := strings.ToLower(strings.TrimSpace(property))
	if target == "" {
		return ""
	}
	if v := extractMetaParsed(rawHTML, target); v != "" {
		return v
	}

	re := regexp.MustCompile(`(?is)<meta\s+[^>]*(?:property|name|itemprop)\s*=\s*["']` + regexp.QuoteMeta(target) + `["'][^>]*content\s*=\s*["']([^"']*)["']`)
	if m := re.FindStringSubmatch(rawHTML); len(m) > 1 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}
	// Try reversed order (content before property/name/itemprop).
	re2 := regexp.MustCompile(`(?is)<meta\s+[^>]*content\s*=\s*["']([^"']*)["'][^>]*(?:property|name|itemprop)\s*=\s*["']` + regexp.QuoteMeta(target) + `["']`)
	if m := re2.FindStringSubmatch(rawHTML); len(m) > 1 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}
	return ""
}

func extractMetaParsed(rawHTML, target string) string {
	doc, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return ""
	}
	var walk func(*html.Node) string
	walk = func(n *html.Node) string {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "meta") {
			var content string
			matched := false
			for _, a := range n.Attr {
				key := strings.ToLower(a.Key)
				val := strings.TrimSpace(a.Val)
				switch key {
				case "content":
					content = val
				case "property", "name", "itemprop":
					if strings.EqualFold(val, target) {
						matched = true
					}
				}
			}
			if matched && content != "" {
				return strings.TrimSpace(html.UnescapeString(content))
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if v := walk(c); v != "" {
				return v
			}
		}
		return ""
	}
	return walk(doc)
}

func extractJsContent(rawHTML string) string {
	// WeChat puts article content in <div class="rich_media_content" id="js_content">
	re := regexp.MustCompile(`(?is)<div[^>]+id="js_content"[^>]*>(.*?)</div>\s*(?:<div[^>]+class="(?:ct_mpda_wrp|qr_code_pc|rich_media_tool)")`)
	if m := re.FindStringSubmatch(rawHTML); len(m) > 1 {
		return m[1]
	}
	// Simpler fallback
	re2 := regexp.MustCompile(`(?is)<div[^>]+id="js_content"[^>]*>(.*?)$`)
	if m := re2.FindStringSubmatch(rawHTML); len(m) > 1 {
		// Try to find a reasonable end point
		content := m[1]
		// Cut at common WeChat footer patterns
		for _, marker := range []string{"<div class=\"rich_media_tool\"", "<div id=\"js_pc_qr_code\"", "<div class=\"qr_code_pc\"", "<!-- 文章底部 -->"} {
			if idx := strings.Index(content, marker); idx > 0 {
				content = content[:idx]
				break
			}
		}
		return content
	}
	return ""
}

func extractTag(rawHTML, tag string) string {
	re := regexp.MustCompile(`(?is)<` + tag + `[^>]*>(.*?)</` + tag + `>`)
	if m := re.FindStringSubmatch(rawHTML); len(m) > 1 {
		return m[1]
	}
	return ""
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
