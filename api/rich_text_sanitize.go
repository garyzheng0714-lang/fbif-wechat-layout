package main

import (
	"fmt"
	"regexp"
	"strings"

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

// validateRichTextStyle 校验 style 的「值」而非仅属性名（codex 三审）：禁 javascript:/url()/expression。
func validateRichTextStyle(style string) error {
	// 先拒绝 CSS 注释与转义——防 expr/**/ession、\6a... 拆分/编码绕过子串匹配（codex P2 review）。
	if richTextStyleHasCommentOrEscape(style) {
		return fmt.Errorf("rich text style contains comment/escape")
	}
	for _, decl := range strings.Split(style, ";") {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}
		idx := strings.Index(decl, ":")
		if idx < 0 {
			return fmt.Errorf("rich text style decl invalid: %q", decl)
		}
		prop := strings.TrimSpace(strings.ToLower(decl[:idx]))
		val := strings.TrimSpace(decl[idx+1:])
		valLower := strings.ToLower(val)
		if !richTextAllowedStyleProps[prop] {
			return fmt.Errorf("rich text style prop %q not allowed", prop)
		}
		if strings.Contains(valLower, "javascript:") || strings.Contains(valLower, "url(") ||
			strings.Contains(valLower, "expression") || strings.Contains(valLower, "@") {
			return fmt.Errorf("rich text style value unsafe: %q", val)
		}
		// font-size 限数字+安全单位（防 calc/vw 注入；codex P2 review）。
		if prop == "font-size" && !richTextFontSizeRe.MatchString(val) {
			return fmt.Errorf("rich text font-size invalid: %q", val)
		}
	}
	return nil
}

// 顶部 CSS 注释/转义防御（在 validateRichTextStyle 入口调用）。
func richTextStyleHasCommentOrEscape(style string) bool {
	return strings.Contains(style, "/*") || strings.Contains(style, "*/") || strings.Contains(style, "\\")
}
