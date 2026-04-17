package main

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// extractArticle extracts the main article content from a WeChat HTML page
// and converts it to clean Markdown.
func extractArticle(rawHTML string) (markdown, title string) {
	// Extract title from <meta property="og:title"> or <title>
	title = extractMeta(rawHTML, "og:title")
	if title == "" {
		if m := regexp.MustCompile(`<title[^>]*>([^<]+)</title>`).FindStringSubmatch(rawHTML); len(m) > 1 {
			title = strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}

	// Extract js_content div (WeChat article body)
	contentHTML := extractJsContent(rawHTML)
	if contentHTML == "" {
		// Not a WeChat page, try <article> or <main>
		contentHTML = extractTag(rawHTML, "article")
		if contentHTML == "" {
			contentHTML = extractTag(rawHTML, "main")
		}
		if contentHTML == "" {
			// Last resort: just strip all tags
			contentHTML = rawHTML
		}
	}

	// Parse HTML and convert to Markdown
	markdown = htmlToMarkdown(contentHTML)

	// Prepend title as heading
	if title != "" && !strings.HasPrefix(markdown, "# ") {
		markdown = "# " + title + "\n\n" + markdown
	}

	return markdown, title
}

func extractMeta(rawHTML, property string) string {
	re := regexp.MustCompile(`<meta\s+[^>]*property="` + regexp.QuoteMeta(property) + `"[^>]*content="([^"]*)"`)
	if m := re.FindStringSubmatch(rawHTML); len(m) > 1 {
		return html.UnescapeString(m[1])
	}
	// Try reversed order (content before property)
	re2 := regexp.MustCompile(`<meta\s+[^>]*content="([^"]*)"[^>]*property="` + regexp.QuoteMeta(property) + `"`)
	if m := re2.FindStringSubmatch(rawHTML); len(m) > 1 {
		return html.UnescapeString(m[1])
	}
	return ""
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

func htmlToMarkdown(h string) string {
	// Remove <script> and <style> blocks
	h = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(h, "")
	h = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(h, "")
	// Remove HTML comments
	h = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(h, "")

	doc, err := html.Parse(strings.NewReader(h))
	if err != nil {
		// Fallback: strip all tags
		return stripTags(h)
	}

	ctx := &walkCtx{}
	var sb strings.Builder
	walkNode(doc, &sb, ctx)
	result := sb.String()

	// Clean up excessive blank lines. Don't collapse asterisk runs —
	// parseMdRuns on the frontend splits on **, so adjacent bold fragments
	// like **A****B** correctly alternate (empty parts get skipped).
	result = regexp.MustCompile(`\n{3,}`).ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}

// walkCtx tracks nesting depth so nested <strong>/<b> emit a single **...**
// pair instead of stacking markers.
type walkCtx struct {
	boldDepth int
}

func walkNode(n *html.Node, sb *strings.Builder, ctx *walkCtx) {
	if n == nil {
		return
	}

	switch n.Type {
	case html.TextNode:
		text := strings.TrimSpace(n.Data)
		if text != "" {
			sb.WriteString(text)
		}
		return

	case html.ElementNode:
		tag := strings.ToLower(n.Data)

		switch tag {
		case "br":
			sb.WriteString("\n")
			return
		case "hr":
			sb.WriteString("\n\n---\n\n")
			return
		case "img":
			src := getAttr(n, "data-src")
			if src == "" {
				src = getAttr(n, "src")
			}
			if src != "" && !strings.Contains(src, "data:image") {
				alt := getAttr(n, "alt")
				// Preserve WeChat's data-w hint via URL fragment so the
				// frontend can render decorative narrow images at their
				// natural size instead of stretching them to 100%.
				if dw := getAttr(n, "data-w"); dw != "" {
					sep := "#"
					if strings.Contains(src, "#") {
						sep = "&"
					}
					src = src + sep + "dataW=" + dw
				}
				sb.WriteString("\n\n![" + alt + "](" + src + ")\n\n")
			}
			return
		case "h1":
			sb.WriteString("\n\n# ")
			walkChildren(n, sb, ctx)
			sb.WriteString("\n\n")
			return
		case "h2":
			sb.WriteString("\n\n## ")
			walkChildren(n, sb, ctx)
			sb.WriteString("\n\n")
			return
		case "h3":
			sb.WriteString("\n\n### ")
			walkChildren(n, sb, ctx)
			sb.WriteString("\n\n")
			return
		case "p", "section":
			sb.WriteString("\n\n")
			walkChildren(n, sb, ctx)
			sb.WriteString("\n\n")
			return
		case "strong", "b":
			// Only emit ** on the outermost bold boundary — avoids the
			// ****/****** runs that come from WeChat's nested <b><b>...
			if ctx.boldDepth == 0 {
				sb.WriteString("**")
			}
			ctx.boldDepth++
			walkChildren(n, sb, ctx)
			ctx.boldDepth--
			if ctx.boldDepth == 0 {
				sb.WriteString("**")
			}
			return
		case "em", "i":
			// Drop italic emphasis entirely. WeChat frequently mixes <i>
			// with <b> as pure visual styling; emitting *...* leaves stray
			// asterisks that the frontend's **-only parser renders literally.
			walkChildren(n, sb, ctx)
			return
		case "a":
			href := getAttr(n, "href")
			if href != "" && !strings.HasPrefix(href, "javascript:") {
				sb.WriteString("[")
				walkChildren(n, sb, ctx)
				sb.WriteString("](" + href + ")")
			} else {
				walkChildren(n, sb, ctx)
			}
			return
		case "li":
			sb.WriteString("\n- ")
			walkChildren(n, sb, ctx)
			return
		case "blockquote":
			sb.WriteString("\n\n> ")
			walkChildren(n, sb, ctx)
			sb.WriteString("\n\n")
			return
		case "script", "style", "noscript", "iframe":
			return // skip entirely
		case "span":
			// Just pass through spans, they're styling only
			walkChildren(n, sb, ctx)
			return
		}
	}

	// Default: walk children
	walkChildren(n, sb, ctx)
}

func walkChildren(n *html.Node, sb *strings.Builder, ctx *walkCtx) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNode(c, sb, ctx)
	}
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func stripTags(s string) string {
	return regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
}
