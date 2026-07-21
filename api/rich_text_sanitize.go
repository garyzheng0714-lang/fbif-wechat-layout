package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// font-size 只允许数字 + 安全单位（防 vw/vh/calc(...) 等注入；codex P2 review）。
var richTextFontSizeRe = regexp.MustCompile(`^\d+(\.\d+)?(px|em|rem|%|pt)$`)

// rich_text_sanitize.go — 富文本（text 块 html 字段）的白名单安全校验（codex review c1）。
//
// P2 富文本：text 块从「散字段(content/font_size/...)」升级为单一中性 HTML 字段 html。
// wangEditor 产出 / 迁移写入的中性 HTML 必须只含白名单标签/属性/style，禁 XSS
// （<script>/<img>/on*=/javascript:/url()/expression）。各渠道渲染器再把中性 HTML
// 转成自己的 inline-style（mp: rgb()+mp-quote；site: hex）——存储中性、渲染期分渠道（红线#19）。

var richTextAllowedTags = map[string]bool{
	"p": true, "span": true, "strong": true, "em": true, "u": true,
	"a": true, "ul": true, "ol": true, "li": true, "br": true,
}

var richTextAllowedStyleProps = map[string]bool{
	"color": true, "background-color": true, "font-size": true,
	"font-style": true, "font-weight": true, "text-decoration": true, "text-align": true,
}

// 页脚 HTML 比正文富文本多图片和表格，但仍只允许静态排版元素。表单、可执行/可嵌入
// 内容（script/style/svg/iframe/object/embed）不进入白名单。
var footerHTMLAllowedTags = map[string]bool{
	"p": true, "div": true, "section": true, "span": true,
	"strong": true, "b": true, "em": true, "i": true, "u": true,
	"a": true, "br": true, "hr": true, "img": true,
	"ul": true, "ol": true, "li": true, "blockquote": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true,
	"tr": true, "td": true, "th": true, "colgroup": true, "col": true,
}

var footerHTMLAllowedStyleProps = map[string]bool{
	"color": true, "background-color": true, "font-size": true,
	"font-style": true, "font-weight": true, "text-decoration": true,
	"text-align": true, "display": true,
	"margin": true, "margin-top": true, "margin-right": true,
	"margin-bottom": true, "margin-left": true,
	"padding": true, "padding-top": true, "padding-right": true,
	"padding-bottom": true, "padding-left": true,
	"width": true, "max-width": true, "height": true,
	"border": true, "border-top": true, "border-right": true,
	"border-bottom": true, "border-left": true, "border-collapse": true,
	"line-height": true, "letter-spacing": true, "vertical-align": true,
	"white-space": true,
}

var (
	footerAssetRefRe = regexp.MustCompile(`^\{\{asset(:[a-z0-9_-]+|#[a-z0-9][a-z0-9_-]{0,63})\}\}$`)
	cssLengthRe      = regexp.MustCompile(`^(0|(\d+(\.\d+)?)(px|em|rem|%|pt))$`)
	cssColorRe       = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|[a-zA-Z]{1,30}|(rgb|rgba|hsl|hsla)\([0-9.,%\s+-]+\))$`)
)

// validateRichTextHTML 校验富文本 HTML 只含白名单标签/属性/style。空串合法。
func validateRichTextHTML(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if len([]rune(s)) > 20000 {
		return fmt.Errorf("rich text too long")
	}
	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), context)
	if err != nil {
		return fmt.Errorf("rich text parse failed: %w", err)
	}
	for _, n := range nodes {
		if err := walkRichTextNode(n); err != nil {
			return err
		}
	}
	return nil
}

func walkRichTextNode(n *html.Node) error {
	switch n.Type {
	case html.CommentNode:
		return fmt.Errorf("rich text comment not allowed")
	case html.DoctypeNode:
		return fmt.Errorf("rich text doctype not allowed")
	}
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.Data)
		if !richTextAllowedTags[tag] {
			return fmt.Errorf("rich text tag %q not allowed", tag)
		}
		for _, attr := range n.Attr {
			key := strings.ToLower(attr.Key)
			if strings.HasPrefix(key, "on") {
				return fmt.Errorf("rich text event attr %q not allowed", key)
			}
			switch {
			case tag == "a" && (key == "href" || key == "target" || key == "rel"):
				if key == "href" && !isSafeRuleBehaviorURL(attr.Val) {
					return fmt.Errorf("rich text href not safe: %s", attr.Val)
				}
			case key == "style":
				if err := validateRichTextStyle(attr.Val); err != nil {
					return err
				}
			default:
				return fmt.Errorf("rich text attr %q on <%s> not allowed", key, tag)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := walkRichTextNode(c); err != nil {
			return err
		}
	}
	return nil
}

// validateFooterHTML 校验高级页脚片段。它只做验证、不重写字符串，确保现有 golden HTML
// 字节不漂移；保存和渲染前都可以复用同一安全边界。
func validateFooterHTML(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if utf8.RuneCountInString(s) > 20000 {
		return fmt.Errorf("footer HTML too long")
	}
	context := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), context)
	if err != nil {
		return fmt.Errorf("footer HTML parse failed: %w", err)
	}
	for _, n := range nodes {
		if err := walkFooterHTMLNode(n); err != nil {
			return err
		}
	}
	return nil
}

func walkFooterHTMLNode(n *html.Node) error {
	switch n.Type {
	case html.CommentNode:
		return fmt.Errorf("footer HTML comment not allowed")
	case html.DoctypeNode:
		return fmt.Errorf("footer HTML doctype not allowed")
	}
	if n.Type == html.ElementNode {
		tag := strings.ToLower(n.Data)
		if !footerHTMLAllowedTags[tag] {
			return fmt.Errorf("footer HTML tag %q not allowed", tag)
		}
		attrs := make(map[string]string, len(n.Attr))
		for _, attr := range n.Attr {
			if attr.Namespace != "" {
				return fmt.Errorf("footer HTML namespaced attr %q not allowed", attr.Key)
			}
			key := strings.ToLower(attr.Key)
			if _, duplicate := attrs[key]; duplicate {
				return fmt.Errorf("footer HTML duplicate attr %q on <%s>", key, tag)
			}
			attrs[key] = attr.Val
			if err := validateFooterHTMLAttr(tag, key, attr.Val); err != nil {
				return err
			}
		}
		if attrs["target"] == "_blank" && !containsSpaceToken(attrs["rel"], "noopener") {
			return fmt.Errorf("footer HTML target=_blank requires rel=noopener")
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := walkFooterHTMLNode(c); err != nil {
			return err
		}
	}
	return nil
}

func validateFooterHTMLAttr(tag, key, value string) error {
	if strings.HasPrefix(key, "on") {
		return fmt.Errorf("footer HTML event attr %q not allowed", key)
	}
	if utf8.RuneCountInString(value) > 2000 || strings.IndexFunc(value, func(r rune) bool {
		return r < 0x20 && r != '\t' && r != '\n' && r != '\r'
	}) >= 0 {
		return fmt.Errorf("footer HTML attr %q on <%s> is invalid", key, tag)
	}
	switch {
	case key == "style":
		if err := validateFooterHTMLStyle(value); err != nil {
			return err
		}
		return nil
	case key == "title":
		return nil
	case tag == "a" && key == "href":
		if !isSafeRuleBehaviorURL(value) {
			return fmt.Errorf("footer HTML href not safe: %s", value)
		}
		return nil
	case tag == "a" && key == "target":
		if value != "_blank" && value != "_self" {
			return fmt.Errorf("footer HTML target %q not allowed", value)
		}
		return nil
	case tag == "a" && key == "rel":
		for _, token := range strings.Fields(strings.ToLower(value)) {
			switch token {
			case "noopener", "noreferrer", "nofollow":
			default:
				return fmt.Errorf("footer HTML rel token %q not allowed", token)
			}
		}
		return nil
	case tag == "img" && key == "src":
		if !footerAssetRefRe.MatchString(strings.TrimSpace(value)) && !isSafeRuleBehaviorURL(value) {
			return fmt.Errorf("footer HTML img src not safe: %s", value)
		}
		return nil
	case tag == "img" && key == "alt":
		if utf8.RuneCountInString(value) > 500 {
			return fmt.Errorf("footer HTML img alt too long")
		}
		return nil
	case tag == "img" && key == "class":
		for _, token := range strings.Fields(value) {
			switch token {
			case "mc-img", "rich_pages", "wxw-img":
			default:
				return fmt.Errorf("footer HTML img class %q not allowed", token)
			}
		}
		return nil
	case tag == "img" && (key == "width" || key == "height"):
		return validateHTMLDimension("footer HTML img "+key, value, true)
	case tag == "img" && key == "border":
		if value != "0" {
			return fmt.Errorf("footer HTML img border must be 0")
		}
		return nil
	case (tag == "td" || tag == "th") && (key == "colspan" || key == "rowspan"):
		return validateBoundedHTMLInteger("footer HTML "+key, value, 1, 100)
	case (tag == "td" || tag == "th" || tag == "tr" || tag == "p" || tag == "div") && key == "align":
		if !oneOf(strings.ToLower(value), "left", "center", "right", "justify") {
			return fmt.Errorf("footer HTML align %q not allowed", value)
		}
		return nil
	case (tag == "td" || tag == "th") && key == "valign":
		if !oneOf(strings.ToLower(value), "top", "middle", "bottom", "baseline") {
			return fmt.Errorf("footer HTML valign %q not allowed", value)
		}
		return nil
	case tag == "table" && (key == "border" || key == "cellpadding" || key == "cellspacing"):
		return validateBoundedHTMLInteger("footer HTML table "+key, value, 0, 100)
	case (tag == "table" || tag == "td" || tag == "th" || tag == "col") && key == "width":
		return validateHTMLDimension("footer HTML "+tag+" width", value, true)
	case tag == "ol" && key == "start":
		return validateBoundedHTMLInteger("footer HTML ol start", value, -10000, 10000)
	case tag == "li" && key == "value":
		return validateBoundedHTMLInteger("footer HTML li value", value, -10000, 10000)
	default:
		return fmt.Errorf("footer HTML attr %q on <%s> not allowed", key, tag)
	}
}

func validateFooterHTMLStyle(style string) error {
	return validateAllowedInlineStyle("footer HTML", style, footerHTMLAllowedStyleProps, validateFooterStyleValue)
}

// validateRichTextStyle 校验 style 的「值」而非仅属性名（codex 三审）：禁 javascript:/url()/expression。
func validateRichTextStyle(style string) error {
	return validateAllowedInlineStyle("rich text", style, richTextAllowedStyleProps, func(prop, val string) error {
		if prop == "font-size" && !richTextFontSizeRe.MatchString(val) {
			return fmt.Errorf("rich text font-size invalid: %q", val)
		}
		return nil
	})
}

func validateAllowedInlineStyle(context, style string, allowed map[string]bool, validateValue func(string, string) error) error {
	// 先拒绝 CSS 注释与转义——防 expr/**/ession、\6a... 拆分/编码绕过子串匹配。
	if utf8.RuneCountInString(style) > 2000 || richTextStyleHasCommentOrEscape(style) {
		return fmt.Errorf("%s style contains comment/escape or is too long", context)
	}
	declCount := 0
	for _, decl := range strings.Split(style, ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		declCount++
		if declCount > 40 {
			return fmt.Errorf("%s style has too many declarations", context)
		}
		idx := strings.Index(decl, ":")
		if idx < 0 {
			return fmt.Errorf("%s style decl invalid: %q", context, decl)
		}
		prop := strings.TrimSpace(strings.ToLower(decl[:idx]))
		val := strings.TrimSpace(decl[idx+1:])
		valLower := strings.ToLower(val)
		if !allowed[prop] {
			return fmt.Errorf("%s style prop %q not allowed", context, prop)
		}
		if val == "" || strings.Contains(valLower, "javascript:") || strings.Contains(valLower, "url(") ||
			strings.Contains(valLower, "expression") || strings.Contains(valLower, "@") ||
			strings.Contains(valLower, "var(") || strings.Contains(valLower, "env(") ||
			strings.Contains(valLower, "calc(") || strings.ContainsAny(val, "{}<>\"") {
			return fmt.Errorf("%s style value unsafe: %q", context, val)
		}
		if err := validateValue(prop, val); err != nil {
			return err
		}
	}
	return nil
}

func validateFooterStyleValue(prop, value string) error {
	lower := strings.ToLower(strings.TrimSpace(value))
	switch prop {
	case "color", "background-color":
		if !cssColorRe.MatchString(value) {
			return fmt.Errorf("footer HTML %s invalid: %q", prop, value)
		}
	case "font-size":
		if !validateCSSFontSize(lower) {
			return fmt.Errorf("footer HTML font-size invalid: %q", value)
		}
	case "font-style":
		if !oneOf(lower, "normal", "italic", "oblique") {
			return fmt.Errorf("footer HTML font-style invalid: %q", value)
		}
	case "font-weight":
		if !oneOf(lower, "normal", "bold", "bolder", "lighter", "100", "200", "300", "400", "500", "600", "700", "800", "900") {
			return fmt.Errorf("footer HTML font-weight invalid: %q", value)
		}
	case "text-decoration":
		for _, token := range strings.Fields(lower) {
			if !oneOf(token, "none", "underline", "overline", "line-through", "solid", "double", "dotted", "dashed", "wavy") {
				return fmt.Errorf("footer HTML text-decoration invalid: %q", value)
			}
		}
	case "text-align":
		if !oneOf(lower, "left", "right", "center", "justify", "start", "end") {
			return fmt.Errorf("footer HTML text-align invalid: %q", value)
		}
	case "display":
		if !oneOf(lower, "block", "inline", "inline-block", "table", "table-row", "table-cell", "none") {
			return fmt.Errorf("footer HTML display invalid: %q", value)
		}
	case "margin", "margin-top", "margin-right", "margin-bottom", "margin-left":
		if !validateCSSLengthList(lower, true, 4) {
			return fmt.Errorf("footer HTML %s invalid: %q", prop, value)
		}
	case "padding", "padding-top", "padding-right", "padding-bottom", "padding-left":
		if !validateCSSLengthList(lower, false, 4) {
			return fmt.Errorf("footer HTML %s invalid: %q", prop, value)
		}
	case "width", "max-width", "height":
		if lower != "auto" && lower != "none" && !validateCSSLengthList(lower, false, 1) {
			return fmt.Errorf("footer HTML %s invalid: %q", prop, value)
		}
	case "border", "border-top", "border-right", "border-bottom", "border-left":
		if !validateCSSBorder(lower) {
			return fmt.Errorf("footer HTML %s invalid: %q", prop, value)
		}
	case "border-collapse":
		if !oneOf(lower, "collapse", "separate") {
			return fmt.Errorf("footer HTML border-collapse invalid: %q", value)
		}
	case "line-height":
		if lower != "normal" && !validateCSSLengthList(lower, false, 1) {
			return fmt.Errorf("footer HTML line-height invalid: %q", value)
		}
	case "letter-spacing":
		if lower != "normal" && !validateCSSLengthList(lower, false, 1) {
			return fmt.Errorf("footer HTML letter-spacing invalid: %q", value)
		}
	case "vertical-align":
		if !oneOf(lower, "baseline", "middle", "top", "bottom", "text-top", "text-bottom", "sub", "super") &&
			!validateCSSLengthList(lower, false, 1) {
			return fmt.Errorf("footer HTML vertical-align invalid: %q", value)
		}
	case "white-space":
		if !oneOf(lower, "normal", "nowrap", "pre", "pre-wrap", "pre-line") {
			return fmt.Errorf("footer HTML white-space invalid: %q", value)
		}
	}
	return nil
}

func validateCSSLengthList(value string, allowAuto bool, maxParts int) bool {
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > maxParts {
		return false
	}
	for _, part := range parts {
		if allowAuto && part == "auto" {
			continue
		}
		if !validateCSSLengthToken(part) {
			return false
		}
	}
	return true
}

func validateCSSBorder(value string) bool {
	if value == "0" || value == "none" {
		return true
	}
	parts := strings.Fields(value)
	if len(parts) < 1 || len(parts) > 3 {
		return false
	}
	seenLength, seenStyle, seenColor := false, false, false
	for _, part := range parts {
		switch {
		case !seenLength && validateCSSLengthToken(part):
			seenLength = true
		case !seenStyle && oneOf(part, "none", "solid", "dashed", "dotted", "double"):
			seenStyle = true
		case !seenColor && cssColorRe.MatchString(part):
			seenColor = true
		default:
			return false
		}
	}
	return true
}

func validateCSSLengthToken(value string) bool {
	if value == "0" {
		return true
	}
	match := cssLengthRe.FindStringSubmatch(value)
	if match == nil {
		return false
	}
	n, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return false
	}
	switch match[4] {
	case "px", "pt":
		return n <= 5000
	case "em", "rem":
		return n <= 100
	case "%":
		return n <= 1000
	default:
		return false
	}
}

func validateCSSFontSize(value string) bool {
	match := cssLengthRe.FindStringSubmatch(value)
	if match == nil || match[2] == "" {
		return false
	}
	n, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return false
	}
	switch match[4] {
	case "px", "pt":
		return n >= 4 && n <= 200
	case "em", "rem":
		return n >= 0.25 && n <= 20
	case "%":
		return n >= 10 && n <= 500
	default:
		return false
	}
}

func validateHTMLDimension(label, value string, allowPercent bool) error {
	v := strings.TrimSpace(value)
	if allowPercent && strings.HasSuffix(v, "%") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "%"))
		if err == nil && n >= 1 && n <= 100 {
			return nil
		}
		return fmt.Errorf("%s is invalid", label)
	}
	return validateBoundedHTMLInteger(label, v, 1, 5000)
}

func validateBoundedHTMLInteger(label, value string, min, max int) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < min || n > max {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func containsSpaceToken(value, want string) bool {
	for _, token := range strings.Fields(strings.ToLower(value)) {
		if token == want {
			return true
		}
	}
	return false
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}

// 顶部 CSS 注释/转义防御（在 validateRichTextStyle 入口调用）。
func richTextStyleHasCommentOrEscape(style string) bool {
	return strings.Contains(style, "/*") || strings.Contains(style, "*/") || strings.Contains(style, "\\")
}
