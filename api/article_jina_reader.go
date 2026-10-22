package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// 非公众号网页直抓拿不到正文时（站点反爬，如和讯的腾讯云 EdgeOne 只回一段写 cookie
// 再刷新的脚本），改用 Jina Reader 取 Markdown 正文，图片留在原位。国内服务器直连
// r.jina.ai 被墙，生产经 JINA_OUTBOUND_PROXY（socks5，与 Feed 同一出口）出去。
// 公众号不走这里：Markdown 丢了字号、颜色这些分类信号（2026-07-11 用户裁定公众号只用直抓）。
var (
	jinaReaderBase       = "https://r.jina.ai/"
	jinaReaderHTTPClient = newJinaReaderHTTPClient(os.Getenv("JINA_OUTBOUND_PROXY"))
)

// 去掉图片后不到这个字数，按验证页、登录墙处理。
const jinaReaderMinBodyRunes = 120

var (
	errJinaReaderNoBody     = errors.New("jina reader: page has no article body")
	reJinaMarkdownImage     = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	jinaReaderContentMarker = "\nMarkdown Content:\n"
)

func newJinaReaderHTTPClient(outboundProxy string) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if p := strings.TrimSpace(outboundProxy); p != "" {
		if u, err := url.Parse(p); err == nil && u.Host != "" {
			transport.Proxy = http.ProxyURL(u)
		} else {
			log.Printf("JINA_OUTBOUND_PROXY 无法解析，Jina Reader 改为直连")
		}
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: transport}
}

func fetchArticleMarkdownViaJina(ctx context.Context, articleURL string) (title, markdown string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jinaReaderBase+articleURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("X-Remove-Selector", "header, footer, nav, aside")
	resp, err := jinaReaderHTTPClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("jina reader: HTTP %d", resp.StatusCode)
	}
	body, err := readBoundedBody(resp.Body, maxArticlePageBytes)
	if err != nil {
		return "", "", err
	}
	title, markdown = parseJinaReaderResponse(string(body))
	text := strings.Join(strings.Fields(reJinaMarkdownImage.ReplaceAllString(markdown, "")), "")
	if utf8.RuneCountInString(text) < jinaReaderMinBodyRunes {
		return "", "", errJinaReaderNoBody
	}
	return title, markdown, nil
}

// Jina 的纯文本响应先是几行头（Title: / URL Source: / Published Time: …），
// 再是一行「Markdown Content:」，后面才是正文；正文里的同名前缀不能当头删。
func parseJinaReaderResponse(raw string) (title, markdown string) {
	head, body, found := strings.Cut("\n"+raw, jinaReaderContentMarker)
	if !found {
		return "", strings.TrimSpace(raw)
	}
	for _, line := range strings.Split(head, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Title:"); ok {
			title = strings.TrimSpace(v)
			break
		}
	}
	return title, strings.TrimSpace(body)
}
