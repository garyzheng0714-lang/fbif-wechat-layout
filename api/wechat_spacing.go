package main

// wechat_spacing.go — 公众号原文里两段文字之间有没有留距（块的 tightBefore）。
//
// 公众号编辑器分段有两种办法：段与段之间插空段（<p><br></p>，块上记 blankBefore），或者
// 每段自带段后距（margin-bottom: 20px）。一列短句（「1. 品牌名：好听、好记」……）常写成
// margin: 0、中间不空段，原文上紧挨着；FoodTalks 每段后面都垫空段，这一列就被拉开了
// （2026-11-03 #338，用户：「原文行间距比较矮，FoodTalks同步时也保持一样的行间距」）。
//
// 只认原文明写的：两段之间经过的每个元素，上下外边距、内边距都写明是 0，没有固定高度、
// 没有并排排布，中间也没有空段、空格段、分隔线，才算紧贴。没写段距的 <p>、<ul> 这类
// 浏览器自带段距的标签一律当有距，照旧排开。

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// 浏览器给这些标签自带上下段距，原文没写 margin 时当有距。
var tagsWithDefaultVerticalMargin = map[string]bool{
	"p": true, "ul": true, "ol": true, "dl": true, "blockquote": true, "figure": true, "pre": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// boxLeavesSpace 这个元素在原文上边（top=true）或下边留没留出竖向间距。
// 说不准的（auto、calc、负值、并排排布）都当留了，宁可照旧排开。
func boxLeavesSpace(n *html.Node, tag string, top bool) bool {
	marginZero := !tagsWithDefaultVerticalMargin[tag]
	paddingZero := true
	sized := false
	for _, decl := range strings.Split(getAttr(n, "style"), ";") {
		kv := strings.SplitN(decl, ":", 2)
		if len(kv) != 2 {
			continue
		}
		prop := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(kv[1])), "!important"))
		if value == "" {
			continue
		}
		edge := "margin-bottom"
		blockEdge := "margin-block-end"
		if top {
			edge, blockEdge = "margin-top", "margin-block-start"
		}
		switch prop {
		case "margin", "margin-block":
			marginZero = shorthandEdgeIsZero(value, top, prop == "margin-block")
		case edge, blockEdge:
			marginZero = cssLengthIsZero(value)
		case "padding", "padding-block":
			paddingZero = shorthandEdgeIsZero(value, top, prop == "padding-block")
		case strings.Replace(edge, "margin", "padding", 1), strings.Replace(blockEdge, "margin", "padding", 1):
			paddingZero = cssLengthIsZero(value)
		case "height", "min-height":
			sized = value != "auto" && !cssLengthIsZero(value)
		case "display":
			if value != "block" && value != "list-item" && value != "inline" {
				return true
			}
		}
	}
	return !marginZero || !paddingZero || sized
}

// shorthandEdgeIsZero margin/padding 简写里上边或下边是不是 0。block 简写只有两个值：上、下。
func shorthandEdgeIsZero(value string, top, block bool) bool {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return false
	}
	if top || len(parts) == 1 {
		return cssLengthIsZero(parts[0])
	}
	if block {
		return cssLengthIsZero(parts[1])
	}
	if len(parts) == 2 {
		return cssLengthIsZero(parts[0])
	}
	return cssLengthIsZero(parts[2])
}

// isBoxElement 不在块标签里、但写了 display 自成一块的元素（<span style="display:block">）也算块，
// 它的段距同样把两段隔开。
func isBoxElement(n *html.Node) bool {
	for _, decl := range strings.Split(getAttr(n, "style"), ";") {
		kv := strings.SplitN(decl, ":", 2)
		if len(kv) == 2 && strings.ToLower(strings.TrimSpace(kv[0])) == "display" {
			value := strings.ToLower(strings.TrimSpace(kv[1]))
			return value != "" && value != "inline" && !strings.HasPrefix(value, "inline ")
		}
	}
	return false
}

// laysOutSideBySide 子元素左右并排的容器（flex、grid、表格排布），里面的几段不是上下挨着。
func laysOutSideBySide(n *html.Node) bool {
	for _, decl := range strings.Split(getAttr(n, "style"), ";") {
		kv := strings.SplitN(decl, ":", 2)
		if len(kv) == 2 && strings.ToLower(strings.TrimSpace(kv[0])) == "display" {
			value := strings.ToLower(strings.TrimSpace(kv[1]))
			return strings.Contains(value, "flex") || strings.Contains(value, "grid") || strings.HasPrefix(value, "table") ||
				strings.HasPrefix(value, "inline-table")
		}
	}
	return false
}

// hasBlankLineText 只有空格的一段里有不间断空格、全角空格：公众号上显示成一个空行。
func hasBlankLineText(s string) bool {
	return strings.ContainsRune(s, ' ') || strings.ContainsRune(s, '　')
}

func isTightTextKind(k string) bool {
	return k == "txt" || k == "li" || k == "bq"
}

// keepSourceTightness 定稿的块上只留真紧贴的：前后都是文字段，原文里也是挨着的两块（中间没被
// 认成图注、没合并标题、没丢块）。再看整篇：长段之间也大多紧贴的，是没认出这篇靠什么分段
// （外层留白、行高、我们不认的占位元素），这时一处也不认，照旧每段排开。口径同红线 #18 的
// paragraph-breaks.js：只看长段（≥30 字），过半紧贴就不信。
func keepSourceTightness(decisions []RoleDecision) {
	pairs, tight := 0, 0
	prevProse := false
	for i := range decisions {
		b := &decisions[i].Block
		if b.TightBefore {
			prev := i - 1
			b.TightBefore = prev >= 0 && decisions[prev].Index == decisions[i].Index-1 &&
				isTightTextKind(decisions[prev].Block.K) && isTightTextKind(b.K)
		}
		prose := b.K == "txt" && utf8.RuneCountInString(strings.Join(strings.Fields(textFromRuns(b.Runs)+b.Text), "")) >= 30
		if prose && prevProse {
			pairs++
			if b.TightBefore {
				tight++
			}
		}
		prevProse = prose
	}
	if tight*2 <= pairs {
		return
	}
	for i := range decisions {
		decisions[i].Block.TightBefore = false
	}
}
