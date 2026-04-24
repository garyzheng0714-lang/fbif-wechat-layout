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

// reStyleBold matches CSS font-weight values that render bold: the `bold`
// keyword, `bolder`, or numeric 600–900. WeChat wraps bold text in
// <span style="font-weight: bold;"> rather than <strong>/<b>, so we sniff
// this at walk time to avoid losing bold on copied articles.
var reStyleBold = regexp.MustCompile(`font-weight\s*:\s*(bold|bolder|[6-9]\d\d)\b`)

func isStyleBold(style string) bool {
	if style == "" {
		return false
	}
	return reStyleBold.MatchString(strings.ToLower(style))
}

// containsBlockDescendant reports whether the subtree contains any element
// that produces a Markdown block structure. Wrapping such a subtree in
// `**...**` mangles the output (e.g. bold around a bare <img> defeats the
// frontend's image-paragraph detector; bold around <li> corrupts list
// markers).
func containsBlockDescendant(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			switch strings.ToLower(c.Data) {
			case "img", "h1", "h2", "h3", "h4", "h5", "h6",
				"p", "section", "li", "blockquote", "hr",
				"table", "tr", "td", "th", "ul", "ol", "div":
				return true
			}
			if containsBlockDescendant(c) {
				return true
			}
		}
	}
	return false
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
		// Inline-style bold guard. Only wrap when the subtree is safe
		// (no block-level descendants) so we don't corrupt images or
		// lists that happen to sit inside a font-weight:bold span.
		styleBold := isStyleBold(getAttr(n, "style")) && !containsBlockDescendant(n)
		if styleBold {
			if ctx.boldDepth == 0 {
				sb.WriteString("**")
			}
			ctx.boldDepth++
		}
		emitTag(n, sb, ctx)
		if styleBold {
			ctx.boldDepth--
			if ctx.boldDepth == 0 {
				sb.WriteString("**")
			}
		}
		return
	}

	// DocumentNode / DoctypeNode / CommentNode — recurse into children so
	// html.Parse's implicit <html><body> wrapper doesn't swallow the page.
	walkChildren(n, sb, ctx)
}

// emitTag handles the tag-specific Markdown emission. Extracted from
// walkNode so the inline-style bold guard can wrap a single dispatch point
// without duplicating `**` logic across every `return` branch.
func emitTag(n *html.Node, sb *strings.Builder, ctx *walkCtx) {
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
		// Span is styling-only; bold has already been handled by the
		// inline-style guard in walkNode. Just recurse into children.
		walkChildren(n, sb, ctx)
		return
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
