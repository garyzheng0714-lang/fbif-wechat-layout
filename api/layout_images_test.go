package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func layoutTestPolicy(t *testing.T, account string) layoutImagePolicy {
	t.Helper()
	t.Setenv("LAYOUT_IMAGE_HOSTS", "")
	meta, ok := accountMetaByID(account)
	if !ok {
		t.Fatalf("没有账号 %s", account)
	}
	return layoutImagePolicyFor(meta)
}

func TestLayoutImageGateRejectsEveryUnsafeAddress(t *testing.T) {
	mp := layoutTestPolicy(t, "fbif")
	ft := layoutTestPolicy(t, "ft-default")
	oss := "https://" + layoutTestOSSHost + "/a.png"
	wechat := "https://mmbiz.qpic.cn/mmbiz_png/abc/640"
	cases := []struct {
		name   string
		html   string
		policy layoutImagePolicy
		want   string // 空就是放行
	}{
		{"OSS 图放行", `<img src="` + oss + `">`, ft, ""},
		{"素材库的桶放行", `<img src="https://fbif-feishu-base.oss-cn-shanghai.aliyuncs.com/a.png">`, mp, ""},
		{"FoodTalks 图床放行", `<img src="https://static.foodtalks.cn/image/post/a.png">`, ft, ""},
		{"公众号账号留微信图", `<img src="` + wechat + `">`, mp, ""},
		{"FoodTalks 留微信图", `<img src="` + wechat + `">`, ft, "FoodTalks"},
		{"blob", `<img src="blob:https://x/1">`, mp, "blob:"},
		{"没转存的 data 位图", `<img src="data:image/png;base64,AAAA">`, mp, "data:"},
		{"SVG data", `<img src="data:image/svg+xml;base64,AAAA">`, mp, "位图"},
		{"docx-image", `<img src="docx-image://rId5">`, mp, "Word"},
		{"doc-cache 绝对地址", `<img src="https://fbifmp-layout.foodtalks.cn/api/doc-cache/ab/1.png">`, mp, "缓存"},
		{"doc-cache 相对地址", `<img src="/api/doc-cache/ab/1.png">`, mp, "缓存"},
		{"相对地址", `<img src="images/a.png">`, mp, "绝对地址"},
		{"站内素材", `<img src="/assets/logo.png">`, mp, "绝对地址"},
		{"http", `<img src="http://` + layoutTestOSSHost + `/a.png">`, mp, "图床"},
		{"协议相对", `<img src="//` + layoutTestOSSHost + `/a.png">`, mp, "https:"},
		{"白名单外", `<img src="https://cdn.example.com/a.png">`, mp, "图床"},
		{"带端口", `<img src="https://` + layoutTestOSSHost + `:8443/a.png">`, mp, "图床"},
		{"没地址", `<img alt="x">`, mp, "没有地址"},
		{"空地址", `<img src="">`, mp, "没有地址"},
		{"srcset", `<img src="` + oss + `" srcset="https://cdn.example.com/a.png 2x">`, mp, "srcset"},
		{"style 背景", `<section style="background-image:url('https://cdn.example.com/bg.png')">x</section>`, mp, "style"},
		{"style 实体引号", `<section style="background:url(&quot;data:image/png;base64,AA&quot;)">x</section>`, mp, "data:"},
		{"<style> 里", `<style>.a{background:url(https://cdn.example.com/bg.png)}</style>`, mp, "<style>"},
		{"SVG image", `<svg><image href="https://cdn.example.com/a.png"/></svg>`, mp, "svg"},
		{"style 里的 OSS 放行", `<section style="background:url(` + oss + `)">x</section>`, ft, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, lerr := checkLayoutImages(c.html, c.policy)
			if c.want == "" {
				if lerr != nil {
					t.Fatalf("要放行：%v", lerr)
				}
				return
			}
			if lerr == nil || lerr.Code != layoutErrImageNotAllowed || !strings.Contains(lerr.message(), c.want) {
				t.Fatalf("要 image_not_allowed 且说清是哪种（%s）：%v", c.want, lerr)
			}
		})
	}
}

func TestLayoutImageGateListsImagesOnce(t *testing.T) {
	policy := layoutTestPolicy(t, "fbif")
	oss := "https://" + layoutTestOSSHost + "/a.png?x=1&amp;y=2"
	images, lerr := checkLayoutImages(`<p><img src="`+oss+`"><img src='`+oss+`'><img src="https://mmbiz.qlogo.cn/b"></p>`, policy)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(images) != 2 || images[0].Host != layoutImageHostOwn || images[0].Src != "https://"+layoutTestOSSHost+"/a.png?x=1&y=2" ||
		images[1].Host != layoutImageHostWechat {
		t.Fatalf("去重、按顺序、标图床：%+v", images)
	}
	t.Setenv("LAYOUT_IMAGE_HOSTS", " new-bucket.oss-cn-shanghai.aliyuncs.com , ")
	moved := layoutImagePolicyFor(accountMeta{Channel: "ft"})
	if !moved.OwnHosts["new-bucket.oss-cn-shanghai.aliyuncs.com"] || moved.OwnHosts[layoutTestOSSHost] || moved.AllowWechat {
		t.Fatalf("白名单按配置换：%+v", moved)
	}
}

func TestLayoutFoodTalksAlwaysMirrorsWechatImages(t *testing.T) {
	env := newLayoutTestEnv(t)
	wechat := "https://mmbiz.qpic.cn/mmbiz_jpg/abc/640?wx_fmt=jpeg&amp;from=appmsg"
	env.node.html = func(layoutNodeRequest) string {
		return `<p><img data-src="x" src="` + wechat + `" style="width:100%"></p><p><img src="` + wechat + `"></p>`
	}
	result := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "ft-default", "text": "x"}))
	if env.images.callCount() != 1 || len(env.images.calls[0]) != 1 || env.images.calls[0][0] != "https://mmbiz.qpic.cn/mmbiz_jpg/abc/640?wx_fmt=jpeg&from=appmsg" {
		t.Fatalf("微信图整批转存一次（同一张只传一次）：%v", env.images.calls)
	}
	if strings.Contains(result.HTML, "mmbiz") || len(result.Images) != 1 || result.Images[0].Host != layoutImageHostOwn || result.Stats.ImagesMirrored != 1 {
		t.Fatalf("FoodTalks 成品里不许留微信图：%s %+v", result.HTML, result.Images)
	}
	if !strings.Contains(result.HTML, `data-src="x"`) || !strings.Contains(result.HTML, `style="width:100%"`) {
		t.Fatalf("只改 src，别的属性原样：%s", result.HTML)
	}

	// 同一张图在公众号账号原样留。
	waitLayoutIdle(t, env.api, "c1")
	mp := decodeLayoutResultBody(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "x"}))
	if env.images.callCount() != 1 || !strings.Contains(mp.HTML, "mmbiz.qpic.cn") || mp.Images[0].Host != layoutImageHostWechat {
		t.Fatalf("公众号账号留微信图：%s", mp.HTML)
	}
}

func TestLayoutMirrorRewritesOnlyNonCompliantImages(t *testing.T) {
	policy := layoutTestPolicy(t, "fbif")
	oss := "https://" + layoutTestOSSHost + "/keep.png"
	mirror := &fakeLayoutImageMirror{}
	html := `<img src="` + oss + `"><img src='https://cdn.example.com/a.png'><img src="//mmbiz.qpic.cn/b"><img src="http://mmbiz.qpic.cn/c">` +
		`<img src="data:image/webp;base64,AA"><img src="data:image/webp;base64,AA">`
	out, mirrored, lerr := mirrorLayoutImages(context.Background(), html, policy, mirror)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if mirrored != 2 || len(mirror.calls) != 1 || strings.Join(mirror.calls[0], " ") != "https://cdn.example.com/a.png data:image/webp;base64,AA" {
		t.Fatalf("只转存白名单外的外链图和 data: 位图，同一张只传一次：%v", mirror.calls)
	}
	for _, want := range []string{`src="` + oss + `"`, `src='https://` + layoutTestOSSHost + `/layout/0.png'`, `src="https://mmbiz.qpic.cn/b"`, `src="https://mmbiz.qpic.cn/c"`,
		`<img src="https://` + layoutTestOSSHost + `/layout/1.png"><img src="https://` + layoutTestOSSHost + `/layout/1.png">`} {
		if !strings.Contains(out, want) {
			t.Fatalf("改写后要有 %s：%s", want, out)
		}
	}
	if _, lerr := checkLayoutImages(out, policy); lerr != nil {
		t.Fatalf("转存完过得了硬门：%v", lerr)
	}
	// SVG 的 data: 不转存，留给硬门拦。
	svg := `<img src="data:image/svg+xml;base64,AA">`
	kept, _, lerr := mirrorLayoutImages(context.Background(), svg, policy, mirror)
	if lerr != nil || kept != svg || len(mirror.calls) != 1 {
		t.Fatalf("SVG data: 不交给上传服务：%v %s %v", lerr, kept, mirror.calls)
	}
	if _, lerr := checkLayoutImages(kept, policy); lerr == nil || !strings.Contains(lerr.message(), "位图") {
		t.Fatalf("硬门拦下 SVG data:：%v", lerr)
	}
	big := `<img src="data:image/png;base64,` + strings.Repeat("A", layoutInlineImagesMaxBytes) + `">`
	if _, _, lerr := mirrorLayoutImages(context.Background(), big, policy, mirror); lerr == nil || lerr.Code != layoutErrRequestTooLarge {
		t.Fatalf("data: 图太大：%v", lerr)
	}
}

func TestLayoutMirrorRetriesMissingOnceThenFails(t *testing.T) {
	policy := layoutTestPolicy(t, "ft-default")
	html := `<img src="https://cdn.example.com/a.png"><img src="https://cdn.example.com/b.png">`
	attempts := 0
	flaky := &fakeLayoutImageMirror{results: func(urls []string) map[string]string {
		attempts++
		out := map[string]string{}
		for _, src := range urls {
			if src == "https://cdn.example.com/a.png" || attempts > 1 {
				out[src] = "https://" + layoutTestOSSHost + "/" + src[len(src)-5:]
			}
		}
		return out
	}}
	out, mirrored, lerr := mirrorLayoutImages(context.Background(), html, policy, flaky)
	if lerr != nil || mirrored != 2 || len(flaky.calls) != 2 || len(flaky.calls[1]) != 1 {
		t.Fatalf("缺的补一批：%v %d %v", lerr, mirrored, flaky.calls)
	}
	if strings.Contains(out, "cdn.example.com") {
		t.Fatal(out)
	}

	never := &fakeLayoutImageMirror{results: func([]string) map[string]string { return map[string]string{} }}
	if _, _, lerr := mirrorLayoutImages(context.Background(), html, policy, never); lerr == nil || lerr.Code != layoutErrImageMirrorFailed ||
		!strings.Contains(lerr.message(), "cdn.example.com") || len(never.calls) != 2 {
		t.Fatalf("补一次还缺就失败，并说是哪张：%v %v", lerr, never.calls)
	}
	broken := &fakeLayoutImageMirror{err: errors.New("上游 500")}
	if _, _, lerr := mirrorLayoutImages(context.Background(), html, policy, broken); lerr == nil || lerr.Code != layoutErrImageMirrorFailed {
		t.Fatalf("上传服务出错：%v", lerr)
	}
	if _, _, lerr := mirrorLayoutImages(context.Background(), html, policy, nil); lerr == nil || lerr.Code != layoutErrImageMirrorFailed {
		t.Fatalf("没配上传服务：%v", lerr)
	}
	if _, _, lerr := mirrorLayoutImages(context.Background(), `<img src="http://10.0.0.8/a.png">`, policy, &fakeLayoutImageMirror{}); lerr == nil || lerr.Code != layoutErrImageNotAllowed {
		t.Fatalf("内网地址不交给上传服务：%v", lerr)
	}
}

func TestLayoutUploadAnswerOutsideAllowlistFails(t *testing.T) {
	env := newLayoutTestEnv(t)
	env.images.results = func(urls []string) map[string]string {
		return map[string]string{urls[0]: "https://evil.example.com/a.png"}
	}
	env.node.html = func(layoutNodeRequest) string { return `<img src="https://cdn.example.com/a.png">` }
	expectLayoutError(t, env.postJSON(env.keys.active, map[string]any{"account": "fbif", "text": "x"}), http.StatusUnprocessableEntity, layoutErrImageNotAllowed)
}

func TestLayoutImageLabelTruncates(t *testing.T) {
	long := "data:image/png;base64," + strings.Repeat("图", 200)
	label := layoutImageLabel(long)
	if len(label) > 170 || !strings.HasSuffix(label, "…") || !strings.HasPrefix(label, "data:") {
		t.Fatalf("%q", label)
	}
}

func TestLayoutMirrorCacheSkipsRecentUploads(t *testing.T) {
	clock := newLayoutTestClock()
	var batches [][]string
	mirror := newCachedLayoutImageMirror(func(_ context.Context, urls []string) (map[string]string, error) {
		batches = append(batches, append([]string(nil), urls...))
		out := map[string]string{}
		for _, src := range urls {
			if src != "https://cdn.example.com/broken.png" {
				out[src] = "https://" + layoutTestOSSHost + "/x.png"
			}
		}
		return out, nil
	}, nil, clock.Now)
	urls := []string{"https://cdn.example.com/qr.png", "https://cdn.example.com/broken.png"}
	if got, err := mirror.ByURLs(context.Background(), urls); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := mirror.ByURLs(context.Background(), urls); err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if len(batches) != 2 || len(batches[1]) != 1 || batches[1][0] != "https://cdn.example.com/broken.png" {
		t.Fatalf("一小时内传过的不再传，没传成的照样重传：%v", batches)
	}
	clock.Add(layoutMirrorCacheTTL)
	_, _ = mirror.ByURLs(context.Background(), urls[:1])
	if len(batches) != 3 {
		t.Fatalf("过了一小时重传：%v", batches)
	}
	failing := newCachedLayoutImageMirror(func(context.Context, []string) (map[string]string, error) {
		return nil, errors.New("上游 500")
	}, nil, clock.Now)
	if _, err := failing.ByURLs(context.Background(), urls); err == nil {
		t.Fatal("上传服务出错要报出来")
	}
}

func TestLayoutMirrorUploadsDataImagesOneByOne(t *testing.T) {
	clock := newLayoutTestClock()
	var mu sync.Mutex
	var urlBatches [][]string
	var dataCalls []map[string]string
	mirror := newCachedLayoutImageMirror(func(_ context.Context, urls []string) (map[string]string, error) {
		mu.Lock()
		defer mu.Unlock()
		urlBatches = append(urlBatches, append([]string(nil), urls...))
		return map[string]string{urls[0]: "https://" + layoutTestOSSHost + "/url.png"}, nil
	}, func(_ context.Context, images map[string]string) (map[string]string, error) {
		mu.Lock()
		defer mu.Unlock()
		dataCalls = append(dataCalls, images)
		out := map[string]string{}
		for key, src := range images {
			if !strings.Contains(src, "broken") {
				out[key] = "https://" + layoutTestOSSHost + "/" + key + ".png"
			}
		}
		return out, nil
	}, clock.Now)
	qr, logo, broken := "data:image/png;base64,UVI=", "data:image/jpeg;base64,TE9HTw==", "data:image/gif;base64,broken"
	got, err := mirror.ByURLs(context.Background(), []string{"https://cdn.example.com/a.png", qr, logo, broken})
	if err != nil || len(got) != 3 || got[broken] != "" || !strings.HasPrefix(got[qr], "https://"+layoutTestOSSHost+"/layout_image_") {
		t.Fatalf("网址整批、data: 图一张一个请求：%v %v", got, err)
	}
	if len(urlBatches) != 1 || len(urlBatches[0]) != 1 || len(dataCalls) != 3 {
		t.Fatalf("网址交 urls、data: 交 base64_images：%v %d", urlBatches, len(dataCalls))
	}
	for _, call := range dataCalls {
		if len(call) != 1 {
			t.Fatalf("一张一个请求：%v", call)
		}
	}
	if again, _ := mirror.ByURLs(context.Background(), []string{qr, logo}); again[qr] != got[qr] || len(dataCalls) != 3 {
		t.Fatalf("一小时内同一张 data: 图不再传：%v %d", again, len(dataCalls))
	}
	for key := range mirror.entries {
		if strings.HasPrefix(key, "data:") {
			t.Fatalf("data: 图按 sha256 记，不拿整串当键：%s", key)
		}
	}
	failing := newCachedLayoutImageMirror(nil, func(context.Context, map[string]string) (map[string]string, error) {
		return nil, errors.New("上游 500")
	}, clock.Now)
	if _, err := failing.ByURLs(context.Background(), []string{qr}); err == nil {
		t.Fatal("data: 图上传出错要报出来")
	}
	unwired := newCachedLayoutImageMirror(nil, nil, clock.Now)
	if _, err := unwired.ByURLs(context.Background(), []string{qr}); err == nil {
		t.Fatal("没接 data: 上传要报出来")
	}
}
