package main

// wechat_swipe.go — 公众号原文里左右滑着看的一组图（滑动图，红线 50）。
//
// 2026-11-18 媒体提效群 Dudu：「排版工具有可能排版滑动图吗？（目前排版工具是将滑动图一张张摊开）」。
// 公众号编辑器（135、秀米等）做的滑动图都是一个写了 overflow-x: auto 的外框，里面一条几倍宽的
// 长条（width: 300%，或 flex、nowrap 排成一行），每张图占一格。抓原文时把同一个外框里的图记上
// 同一个组号（块的 swipe），公众号照原样排成滑动图；FoodTalks、头条号、百家号不读它，照旧竖排。
//
// 只认外框里只有图：带字的（每格图下带说明的卡片式滑动）、只有一张图的，照旧一张张排。

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
)

// isImageSwipeBox 这个元素是不是一组滑动图的外框：横向可滑、里面的格子排成一行、只有两张以上的图。
func isImageSwipeBox(n *html.Node) bool {
	if n == nil || n.Type != html.ElementNode || !scrollsHorizontally(n) || !laysOutInOneRow(n, 3) {
		return false
	}
	images, onlyImages := 0, true
	var visit func(*html.Node)
	visit = func(c *html.Node) {
		if !onlyImages {
			return
		}
		switch c.Type {
		case html.TextNode:
			if strings.TrimFunc(c.Data, isBlankRune) != "" {
				onlyImages = false
			}
			return
		case html.ElementNode:
			switch strings.ToLower(c.Data) {
			case "script", "style":
				return
			case "img":
				if kind, _ := mediaSourceForNode(c); kind == "img" {
					images++
				}
				return
			case "video", "table", "iframe", "svg":
				onlyImages = false
				return
			}
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			visit(k)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		visit(c)
	}
	return onlyImages && images >= 2
}

// scrollsHorizontally 原文写明横向可滑（overflow-x: auto | scroll）。只写 overflow: auto 的多是
// 定高的上下滑框（长图「上下滑动查看」），不算。
func scrollsHorizontally(n *html.Node) bool {
	for _, decl := range strings.Split(getAttr(n, "style"), ";") {
		prop, value, ok := cssDecl(decl)
		if ok && prop == "overflow-x" && (value == "auto" || value == "scroll") {
			return true
		}
	}
	return false
}

// laysOutInOneRow 外框自己或往里几层有把格子排成一行的写法：长条比外框宽（width 超过 100%）、
// 不换行（white-space: nowrap）、或不换行的 flex。
func laysOutInOneRow(n *html.Node, depth int) bool {
	if n == nil || n.Type != html.ElementNode || depth < 0 {
		return false
	}
	flex, wraps := false, false
	for _, decl := range strings.Split(getAttr(n, "style"), ";") {
		prop, value, ok := cssDecl(decl)
		if !ok {
			continue
		}
		switch prop {
		case "width", "min-width":
			if pct, found := strings.CutSuffix(value, "%"); found {
				if v, err := strconv.ParseFloat(strings.TrimSpace(pct), 64); err == nil && v > 100 {
					return true
				}
			}
		case "white-space":
			if value == "nowrap" {
				return true
			}
		case "display":
			flex = strings.Contains(value, "flex")
		case "flex-wrap":
			wraps = value == "wrap" || value == "wrap-reverse"
		case "flex-flow":
			wraps = strings.Contains(value, "wrap") && !strings.Contains(value, "nowrap")
		}
	}
	if flex && !wraps {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if laysOutInOneRow(c, depth-1) {
			return true
		}
	}
	return false
}

// cssDecl 拆一条行内样式：属性名小写，值去掉 !important、小写。
func cssDecl(decl string) (prop, value string, ok bool) {
	kv := strings.SplitN(decl, ":", 2)
	if len(kv) != 2 {
		return "", "", false
	}
	prop = strings.ToLower(strings.TrimSpace(kv[0]))
	value = strings.ToLower(strings.TrimSpace(kv[1]))
	value = strings.TrimSpace(strings.TrimSuffix(value, "!important"))
	return prop, value, prop != "" && value != ""
}

// isBlankRune 空格、不间断空格、全角空格、零宽字符都不算字。
func isBlankRune(r rune) bool {
	return unicode.IsSpace(r) || r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff'
}
