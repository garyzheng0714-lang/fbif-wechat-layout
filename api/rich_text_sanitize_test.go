package main

import "testing"

// 富文本白名单：接受常见排版标签/属性/style（codex c1 列的集合）。
func TestValidateRichTextHTMLAccepts(t *testing.T) {
	ok := []string{
		"",
		"<p>纯文本</p>",
		`<p style="text-align: center;"><span style="color: #544545; font-size: 17px; font-weight: bold;">提示：</span></p>`,
		`<p><strong>粗</strong><em>斜</em><u>下划线</u></p>`,
		`<p><a href="https://example.com" target="_blank" rel="noopener">链接</a></p>`,
		"<ul><li>一</li><li>二</li></ul>",
		`<span style="background-color: #ffffff; text-decoration: underline; font-style: italic;">高亮</span>`,
		"第一行<br>第二行",
	}
	for _, s := range ok {
		if err := validateRichTextHTML(s); err != nil {
			t.Errorf("should accept %q: %v", s, err)
		}
	}
}

// 富文本白名单：拒绝 XSS 与白名单外的标签/属性/style。
func TestValidateRichTextHTMLRejects(t *testing.T) {
	bad := map[string]string{
		"script":           `<p><script>alert(1)</script></p>`,
		"img":              `<p><img src="x"></p>`,
		"onclick":          `<p onclick="evil()">x</p>`,
		"javascript href":  `<a href="javascript:alert(1)">x</a>`,
		"bad tag div":      `<div>x</div>`,
		"bad attr class":   `<p class="x">y</p>`,
		"bad style prop":   `<p style="position: fixed;">x</p>`,
		"style url":        `<p style="background-color: url(evil);">x</p>`,
		"style expr":       `<span style="color: expression(alert(1));">x</span>`,
		"css comment expr": `<span style="color: expr/**/ession(x);">y</span>`,
		"css escape":       `<span style="color: \6a avascript;">y</span>`,
		"font-size calc":   `<span style="font-size: calc(100vw);">y</span>`,
		"font-size vw":     `<span style="font-size: 99vw;">y</span>`,
		"style at-import":  `<span style="color: red; @import url(x);">y</span>`,
		"data href":        `<a href="data:text/html,x">y</a>`,
		"vbscript href":    `<a href="vbscript:msgbox(1)">y</a>`,
		"proto-relative":   `<a href="//evil.com">y</a>`,
		"html comment":     `<p><!-- evil --></p>`,
	}
	for name, s := range bad {
		if err := validateRichTextHTML(s); err == nil {
			t.Errorf("should reject (%s): %q", name, s)
		}
	}
}
