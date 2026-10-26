package main

// layout_images.go — 排版接口成品里的图片：先把不合格的图转存到 FBIF 的 OSS，再独立过一道硬门。
//
// 转存：排版程序回来的 html 里，凡是 <img src> 不是 FBIF 自己图床（两个 OSS 桶、FoodTalks 图床）的 https 地址
// （公众号账号另外放过微信图床），都转存，只改 src="…" 这几处原文：网址用 ossUploadByURLs（和发布流水线同一个
// 上传服务，整批一次，缺的再补一次）；data: 位图用 ossUploadByBase64Images（网页复制前也是这样把页脚、素材图
// 传上 OSS 的——排版程序照网页把站内 /assets 图读成了 data:）。一小时内转存过的同一张图（页脚二维码、名片）直接用上次的结果。
// 硬门：全部改写之后，用 HTML 解析器重新扫一遍最终 html 的 <img src/srcset>、SVG <image href>、style 属性和 <style> 里的 url(…)，
// 每个地址都必须是 https 且主机在 FBIF 自己图床，或者（只限公众号账号）在微信图床；FoodTalks 账号留了微信图床、
// 上传服务回了白名单外的地址、blob:、data:、docx-image://、/api/doc-cache/、相对地址，整篇失败 image_not_allowed。
// 转存那一步看漏的（比如属性写法怪），这一道一定拦得住。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// layoutDefaultOwnImageHosts FBIF 自己的图床，成品里可以直接留：上传服务（WECHAT_UPLOAD_ENDPOINT 的 /api/oss-upload）
// 真实返回的桶（见 docs/foodtalks-api-contract.md）、规则素材库里页脚图所在的桶、FoodTalks 图床（网站页脚二维码、logo，
// 网页复制也原样留着，见 rule_asset_library.go）。换桶时用 LAYOUT_IMAGE_HOSTS（逗号分隔，整张表替换）改，不改代码。
const layoutDefaultOwnImageHosts = "fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com,fbif-feishu-base.oss-cn-shanghai.aliyuncs.com,static.foodtalks.cn"

// layoutWechatImageHosts 微信图床。只有公众号账号能留：粘进公众号后台时微信自己认。
var layoutWechatImageHosts = []string{"mmbiz.qpic.cn", "mmbiz.qlogo.cn"}

const (
	layoutImageHostWechat = "wechat"
	layoutImageHostOwn    = "fbif"
)

type layoutImagePolicy struct {
	AllowWechat bool
	OwnHosts    map[string]bool
}

func layoutOwnImageHosts() map[string]bool {
	raw := strings.TrimSpace(os.Getenv("LAYOUT_IMAGE_HOSTS"))
	if raw == "" {
		raw = layoutDefaultOwnImageHosts
	}
	hosts := map[string]bool{}
	for _, host := range strings.Split(raw, ",") {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			hosts[host] = true
		}
	}
	return hosts
}

// layoutImagePolicyFor 按账号的渠道（account_catalog.go 的 Channel）定：公众号可以留微信图床，FoodTalks 一律转 OSS。
func layoutImagePolicyFor(meta accountMeta) layoutImagePolicy {
	channel, _ := layoutChannelFor(meta.Channel)
	return layoutImagePolicy{AllowWechat: channel.AllowWechat, OwnHosts: layoutOwnImageHosts()}
}

func isLayoutWechatImageHost(host string) bool {
	for _, wechat := range layoutWechatImageHosts {
		if host == wechat {
			return true
		}
	}
	return false
}

// layoutImageMirror 把一批图转存到 OSS：公开的 http(s) 地址，或 data: 位图。回 {原地址: OSS 地址}，转不成的不在里面。
type layoutImageMirror interface {
	ByURLs(ctx context.Context, srcs []string) (map[string]string, error)
}

// cachedLayoutImageMirror 记住一小时内转存过的图：页脚的二维码、名片每篇都有，不用每篇都重传一遍。
// data: 图按内容的 sha256 记，不拿整串当键。
type cachedLayoutImageMirror struct {
	upload     func(ctx context.Context, urls []string) (map[string]string, error)
	uploadData func(ctx context.Context, images map[string]string) (map[string]string, error)
	now        func() time.Time

	mu      sync.Mutex
	entries map[string]layoutMirrorEntry
}

type layoutMirrorEntry struct {
	oss string
	at  time.Time
}

const (
	layoutMirrorCacheTTL     = time.Hour
	layoutMirrorCacheEntries = 2048
)

func newCachedLayoutImageMirror(
	upload func(ctx context.Context, urls []string) (map[string]string, error),
	uploadData func(ctx context.Context, images map[string]string) (map[string]string, error),
	now func() time.Time,
) *cachedLayoutImageMirror {
	if now == nil {
		now = time.Now
	}
	return &cachedLayoutImageMirror{upload: upload, uploadData: uploadData, now: now, entries: map[string]layoutMirrorEntry{}}
}

func layoutMirrorCacheKey(src string) string {
	if isLayoutDataImage(src) {
		sum := sha256.Sum256([]byte(src))
		return "sha256:" + hex.EncodeToString(sum[:])
	}
	return src
}

func (m *cachedLayoutImageMirror) ByURLs(ctx context.Context, srcs []string) (map[string]string, error) {
	now := m.now()
	out := map[string]string{}
	var missingURLs, missingData []string
	m.mu.Lock()
	for _, src := range srcs {
		if entry, ok := m.entries[layoutMirrorCacheKey(src)]; ok && now.Sub(entry.at) < layoutMirrorCacheTTL {
			out[src] = entry.oss
			continue
		}
		if isLayoutDataImage(src) {
			missingData = append(missingData, src)
		} else {
			missingURLs = append(missingURLs, src)
		}
	}
	m.mu.Unlock()
	uploaded := map[string]string{}
	if len(missingURLs) > 0 {
		results, err := m.upload(ctx, missingURLs)
		if err != nil {
			return nil, err
		}
		for _, src := range missingURLs {
			uploaded[src] = results[src]
		}
	}
	if len(missingData) > 0 {
		results, err := m.uploadDataImages(ctx, missingData)
		if err != nil {
			return nil, err
		}
		for src, oss := range results {
			uploaded[src] = oss
		}
	}
	if len(uploaded) == 0 {
		return out, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries)+len(uploaded) > layoutMirrorCacheEntries {
		for key, entry := range m.entries {
			if now.Sub(entry.at) >= layoutMirrorCacheTTL || len(m.entries)+len(uploaded) > layoutMirrorCacheEntries {
				delete(m.entries, key)
			}
		}
	}
	for src, oss := range uploaded {
		if oss = strings.TrimSpace(oss); oss != "" {
			out[src] = oss
			m.entries[layoutMirrorCacheKey(src)] = layoutMirrorEntry{oss: oss, at: now}
		}
	}
	return out, nil
}

// uploadDataImages data: 图一张一个请求（和网页 uploader.js、Word 拆图一样），同时最多 layoutDataImageUploads 张；
// 一张出错整批算错，没回地址的不在结果里（由 mirrorLayoutImages 再补一次）。
func (m *cachedLayoutImageMirror) uploadDataImages(ctx context.Context, srcs []string) (map[string]string, error) {
	if m.uploadData == nil {
		return nil, errors.New("data: 图片转存没有接上")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		firstErr error
	)
	out := map[string]string{}
	slots := make(chan struct{}, layoutDataImageUploads)
	for i, src := range srcs {
		wg.Add(1)
		go func(key, src string) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			results, err := m.uploadData(ctx, map[string]string{key: src})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			if oss := strings.TrimSpace(results[key]); oss != "" {
				out[src] = oss
			}
		}(fmt.Sprintf("layout_image_%03d", i), src)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// newLayoutImageMirrorFromEnv 上传服务没配置时返回 nil：需要转存的稿子会 image_mirror_failed，不需要的照常排。
func newLayoutImageMirrorFromEnv() layoutImageMirror {
	if !docImageMirroringConfigured() {
		return nil
	}
	return newCachedLayoutImageMirror(ossUploadByURLs, ossUploadByBase64Images, time.Now)
}

// layoutDataImagePrefix 只转存这几种位图的 data:（和网页 uploader 一样）；SVG 之类的 data: 直接拦下。
var layoutDataImagePrefix = regexp.MustCompile(`(?i)^data:image/(png|jpe?g|gif|webp);base64,`)

func isLayoutDataImage(src string) bool { return layoutDataImagePrefix.MatchString(src) }

// layoutImageVerdict 一个图片地址过不过得去：host 是 wechat/oss 表示合格，reason 非空表示不合格且不能转存。
type layoutImageVerdict struct {
	normalized string // 转存或比对用的地址（// 开头补成 https:）
	host       string
	inline     bool // data: 位图：不合格，但可以转存
	reason     string
}

func judgeLayoutImageURL(raw string, policy layoutImagePolicy) layoutImageVerdict {
	src := strings.TrimSpace(raw)
	lower := strings.ToLower(src)
	switch {
	case src == "":
		return layoutImageVerdict{reason: "有张图没有地址"}
	case strings.HasPrefix(lower, "blob:"):
		return layoutImageVerdict{reason: "blob: 地址只在原来那个浏览器里有效"}
	case isLayoutDataImage(src):
		return layoutImageVerdict{normalized: src, inline: true} // 位图，可以转存
	case strings.HasPrefix(lower, "data:"):
		return layoutImageVerdict{reason: "data: 内嵌图只收 png、jpeg、gif、webp 位图"}
	case strings.HasPrefix(lower, "docx-image:"):
		return layoutImageVerdict{reason: "Word 里的图没转存成"}
	case strings.Contains(lower, "/api/doc-cache/"):
		return layoutImageVerdict{reason: "排版工具的临时图片缓存地址不能留在成品里"}
	}
	if strings.HasPrefix(src, "//") {
		src = "https:" + src
	}
	parsed, err := url.Parse(src)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return layoutImageVerdict{reason: "不是 http(s) 绝对地址（相对地址、本地路径都不收）"}
	}
	host := strings.ToLower(parsed.Hostname())
	verdict := layoutImageVerdict{normalized: src}
	if parsed.Scheme == "https" && parsed.Port() == "" && parsed.User == nil {
		if policy.OwnHosts[host] {
			verdict.host = layoutImageHostOwn
		} else if policy.AllowWechat && isLayoutWechatImageHost(host) {
			verdict.host = layoutImageHostWechat
		}
	}
	return verdict
}

// layoutImgSrcAttr 转存时找 <img … src="…"> 的原文位置；只用来改写，放行与否由下面的 HTML 解析器硬门说了算。
var layoutImgSrcAttr = regexp.MustCompile(`(?is)<img\b[^>]*?\ssrc\s*=\s*("[^"]*"|'[^']*')`)

// mirrorLayoutImages 把不合格的 <img src> 转存到 OSS 并改写，回改写后的 html 和转存张数。
// 本来就不能转存的地址（blob:、SVG data:、相对地址……）原样留着，交给硬门报 image_not_allowed。
func mirrorLayoutImages(ctx context.Context, rawHTML string, policy layoutImagePolicy, mirror layoutImageMirror) (string, int, *layoutError) {
	type location struct{ start, end int }
	var spots []location
	rewrite := map[string]string{} // 解码后的 src → 要换成的地址
	var pending []string
	pendingSeen := map[string]bool{}
	inlineBytes := 0
	for _, m := range layoutImgSrcAttr.FindAllStringSubmatchIndex(rawHTML, -1) {
		valueStart, valueEnd := m[2]+1, m[3]-1
		src := html.UnescapeString(rawHTML[valueStart:valueEnd])
		verdict := judgeLayoutImageURL(src, policy)
		if verdict.reason != "" {
			continue
		}
		spots = append(spots, location{valueStart, valueEnd})
		if verdict.inline {
			if !pendingSeen[src] {
				pendingSeen[src] = true
				pending = append(pending, src)
				if inlineBytes += len(src); inlineBytes > layoutInlineImagesMaxBytes {
					return "", 0, newLayoutError(layoutErrRequestTooLarge,
						fmt.Sprintf("内嵌的 data: 图片加起来超过 %d MB，请改成公开的 https 图片地址", layoutInlineImagesMaxBytes>>20))
				}
			}
			rewrite[src] = src // 先占位，转存后换成 OSS 地址
			continue
		}
		if verdict.host != "" {
			if verdict.normalized != src {
				rewrite[src] = verdict.normalized
			}
			continue
		}
		// 微信图床的 http 地址换成 https 就行，不用转存。
		if parsed, err := url.Parse(verdict.normalized); err == nil && policy.AllowWechat &&
			isLayoutWechatImageHost(strings.ToLower(parsed.Hostname())) && parsed.Scheme == "http" {
			parsed.Scheme = "https"
			rewrite[src] = parsed.String()
			continue
		}
		if _, err := parsePublicWebURL(verdict.normalized); err != nil {
			// 内网、带账号密码的地址不交给上传服务去取。
			return "", 0, newLayoutError(layoutErrImageNotAllowed,
				"图片地址不是公开网址："+layoutImageLabel(src)).withCause(err)
		}
		if !pendingSeen[verdict.normalized] {
			pendingSeen[verdict.normalized] = true
			pending = append(pending, verdict.normalized)
		}
		rewrite[src] = verdict.normalized // 先占位，转存后换成 OSS 地址
	}
	mirrored := 0
	if len(pending) > 0 {
		if mirror == nil {
			return "", 0, newLayoutError(layoutErrImageMirrorFailed, "图片转存服务没配置").
				withCause(errors.New("WECHAT_UPLOAD_ENDPOINT 未配置"))
		}
		uploaded := map[string]string{}
		// 和发布流水线一样：整批一次，回来缺的再补一批，还缺就整篇失败。
		for attempt := 0; len(pending) > 0 && attempt < 2; attempt++ {
			results, err := mirror.ByURLs(ctx, pending)
			if err != nil {
				if ctx.Err() != nil {
					return "", 0, layoutContextError(ctx, nil)
				}
				return "", 0, layoutErr(layoutErrImageMirrorFailed).withCause(err)
			}
			var missing []string
			for _, src := range pending {
				if oss := strings.TrimSpace(results[src]); oss != "" {
					uploaded[src] = oss
				} else {
					missing = append(missing, src)
				}
			}
			pending = missing
		}
		if len(pending) > 0 {
			return "", 0, newLayoutError(layoutErrImageMirrorFailed,
				fmt.Sprintf("%d 张图转存失败，第一张：%s", len(pending), layoutImageLabel(pending[0])))
		}
		for src, target := range rewrite {
			if oss, ok := uploaded[target]; ok {
				rewrite[src] = oss
			}
		}
		mirrored = len(uploaded)
	}
	if len(rewrite) == 0 {
		return rawHTML, mirrored, nil
	}
	var b strings.Builder
	b.Grow(len(rawHTML))
	last := 0
	for _, spot := range spots {
		src := html.UnescapeString(rawHTML[spot.start:spot.end])
		target, ok := rewrite[src]
		if !ok {
			continue
		}
		b.WriteString(rawHTML[last:spot.start])
		b.WriteString(html.EscapeString(target))
		last = spot.end
	}
	b.WriteString(rawHTML[last:])
	return b.String(), mirrored, nil
}

// layoutCSSURL style 里的 url(…)，引号可有可无。
var layoutCSSURL = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"\s]*))\s*\)`)

// checkLayoutImages 硬门：成品里每一个图片地址都要过。回 html 里的图（去重、按出现顺序）和各自的图床。
func checkLayoutImages(rawHTML string, policy layoutImagePolicy) ([]layoutImageJSON, *layoutError) {
	images := []layoutImageJSON{}
	seen := map[string]bool{}
	check := func(where, raw string) *layoutError {
		verdict := judgeLayoutImageURL(raw, policy)
		if verdict.reason != "" {
			return newLayoutError(layoutErrImageNotAllowed, where+"："+verdict.reason+"："+layoutImageLabel(raw))
		}
		if verdict.host == "" || verdict.normalized != strings.TrimSpace(raw) {
			parsed, _ := url.Parse(verdict.normalized)
			host := ""
			if parsed != nil {
				host = strings.ToLower(parsed.Hostname())
			}
			switch {
			case isLayoutWechatImageHost(host) && !policy.AllowWechat:
				return newLayoutError(layoutErrImageNotAllowed, where+"：FoodTalks 账号的成品里留了微信图床的图（FoodTalks 页面显示不出来）："+layoutImageLabel(raw))
			case verdict.host == "":
				return newLayoutError(layoutErrImageNotAllowed, where+"：图片不在 FBIF 自己的图床上，也没转存成："+layoutImageLabel(raw))
			default:
				return newLayoutError(layoutErrImageNotAllowed, where+"：图片地址要写成 https: 开头："+layoutImageLabel(raw))
			}
		}
		if !seen[verdict.normalized] {
			seen[verdict.normalized] = true
			images = append(images, layoutImageJSON{Src: verdict.normalized, Host: verdict.host})
		}
		return nil
	}
	checkCSS := func(where, css string) *layoutError {
		for _, m := range layoutCSSURL.FindAllStringSubmatch(css, -1) {
			if lerr := check(where, firstNonEmpty(m[1], m[2], m[3])); lerr != nil {
				return lerr
			}
		}
		if strings.Contains(strings.ToLower(css), "url(") && !layoutCSSURL.MatchString(css) {
			return newLayoutError(layoutErrImageNotAllowed, where+"：样式里有读不懂的 url(…)")
		}
		return nil
	}
	tokenizer := xhtml.NewTokenizer(strings.NewReader(rawHTML))
	inStyle := false
	for {
		switch tokenizer.Next() {
		case xhtml.ErrorToken:
			return images, nil
		case xhtml.TextToken:
			if inStyle {
				if lerr := checkCSS("<style> 里", string(tokenizer.Text())); lerr != nil {
					return nil, lerr
				}
			}
		case xhtml.EndTagToken:
			name, _ := tokenizer.TagName()
			if atom.Lookup(name) == atom.Style {
				inStyle = false
			}
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.DataAtom == atom.Style {
				inStyle = true
			}
			hasSrc := false
			for _, attr := range token.Attr {
				key := strings.ToLower(attr.Key)
				switch {
				case token.DataAtom == atom.Img && key == "src":
					hasSrc = true
					if lerr := check("<img src>", attr.Val); lerr != nil {
						return nil, lerr
					}
				case token.DataAtom == atom.Img && key == "srcset":
					for _, candidate := range strings.Split(attr.Val, ",") {
						fields := strings.Fields(candidate)
						if len(fields) == 0 {
							continue
						}
						if lerr := check("<img srcset>", fields[0]); lerr != nil {
							return nil, lerr
						}
					}
				case token.DataAtom == atom.Image && (key == "href" || key == "xlink:href"):
					// 内联 SVG 里的 <image>。
					if lerr := check("<svg image>", attr.Val); lerr != nil {
						return nil, lerr
					}
				case key == "style":
					if lerr := checkCSS("style 里的 url(…)", attr.Val); lerr != nil {
						return nil, lerr
					}
				}
			}
			if token.DataAtom == atom.Img && !hasSrc {
				return nil, newLayoutError(layoutErrImageNotAllowed, "<img src>：有张图没有地址")
			}
		}
	}
}

// layoutImageLabel 错误信息里引用图片地址：太长的（data: 之类）截短。
func layoutImageLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	const max = 160
	if len(raw) <= max {
		return raw
	}
	cut := max
	for cut > 0 && !isUTF8Start(raw[cut]) {
		cut--
	}
	return raw[:cut] + "…"
}

func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }
