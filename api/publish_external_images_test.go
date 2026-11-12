package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"io"
	"math/rand"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

func init() {
	// 测试默认问不到图片大小（不出网）；要大小的用例用 stubExternalImages 显式给。
	externalImageProbe = func(context.Context, string) (int64, string, error) { return 0, "", errors.New("测试不出网") }
}

const testTencentAccountID = "recTestTencent01"

const (
	testBigGIF   = "https://cdn.example.com/big.gif"
	testSmallGIF = "https://cdn.example.com/small.gif"
	testBigJPEG  = "https://cdn.example.com/big.jpg"
)

// testImagesHTML 网站上三张图：超了腾讯号 5MB 的动图、没超的动图、超了的照片（照片不压）。
func testImagesHTML() string {
	return `<p>这是一篇用于验证外部分发流程的原创文章正文。</p><p><img src="` + testBigGIF + `" alt=""></p><p>中间一段。</p>` +
		`<p><img src="` + testSmallGIF + `" alt=""></p><p><img src="` + testBigJPEG + `" alt=""></p>`
}

// TestExternalImagesShrunkBeforePush #367：腾讯号正文动图超了 5MB 腾讯转存不了。推之前把超了的动图压小转存、
// 推给腾讯号的换成压小的地址，记在腾讯号的改稿记录里，再推同一份内容不再压；没超的动图、照片原样；百家号不限，原样推。
func TestExternalImagesShrunkBeforePush(t *testing.T) {
	setTestExternalAccounts(t, testArticleAccount(testTencentAccountID, "tencent", false), testArticleAccount(testBaijiaAccountID, "baijiahao", false))
	stubDistributionImageSize(t, map[string][2]int{})
	sizes := map[string]struct {
		n    int64
		kind string
	}{testBigGIF: {5602523, "image/gif"}, testSmallGIF: {997396, "image/gif"}, testBigJPEG: {21 << 20, "image/jpeg"}}
	probes, shrinks := 0, 0
	stubExternalImages(t, func(_ context.Context, src string) (int64, string, error) {
		probes++
		return sizes[src].n, sizes[src].kind, nil
	}, func(_ context.Context, data []byte, limit int64) ([]byte, error) {
		shrinks++
		if limit != 5<<20 || string(data) != "big gif" {
			t.Fatalf("按腾讯号动图上限压取来的原图：%d %q", limit, data)
		}
		return []byte("small gif"), nil
	}, map[string]string{testBigGIF: "big gif"})
	env := newPublishTestEnv(t)
	robot := useFakeRobot(env)
	const stored = "https://fbif-gary-img-hz.oss-cn-hangzhou.aliyuncs.com/wechat-layout/distribution-image.gif"
	env.pipeline.externalCoverStore = func(_ context.Context, name string, body []byte) (string, error) {
		if !strings.HasPrefix(name, "distribution-image-") || !strings.HasSuffix(name, ".gif") || string(body) != "small gif" {
			t.Fatalf("转存的是压小的动图：%s %q", name, body)
		}
		return stored, nil
	}
	job := createPublishedDistributionJobWithKey(t, env, "site:big-gif", "FBIF食品饮料创新")
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL, Content: testImagesHTML(),
		Summary: job.Summary, SourceName: "FBIF食品饮料创新", PublishStatus: foodTalksPublishStatusPublished,
	})
	if _, err := env.pipeline.requestExternalDistribution(job.ID, "ou_reviewer_1", publishExternalDistributionCardVersion(job)); err != nil {
		t.Fatal(err)
	}
	srcs := map[string][]string{}
	for i, req := range robot.saves {
		for _, block := range req.Blocks {
			if block.K == "img" {
				srcs[robot.platforms[i]] = append(srcs[robot.platforms[i]], block.Src)
			}
		}
	}
	if got := strings.Join(srcs["tencent"], " "); got != stored+" "+testSmallGIF+" "+testBigJPEG {
		t.Fatalf("腾讯号只换超了的动图：%s", got)
	}
	if got := strings.Join(srcs["baijiahao"], " "); got != testBigGIF+" "+testSmallGIF+" "+testBigJPEG {
		t.Fatalf("百家号不限，原样推：%s", got)
	}
	if shrinks != 1 || probes != 3 {
		t.Fatalf("只问腾讯号的三张、只压超了的那张：probes=%d shrinks=%d", probes, shrinks)
	}
	got := mustJob(t, env, job.ID)
	draft, _ := externalDraftFor(got, testTencentAccountID)
	if draft.Images[testBigGIF] != stored || len(draft.Images) != 1 || externalJobNeedsFix(got) {
		t.Fatalf("压小的记在腾讯号的改稿记录里，不算待修改：%+v", draft)
	}
	snapshot, err := env.pipeline.publishedNewsSnapshotForDistribution(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if again := env.pipeline.externalImagesFor(snapshot, testPlatform(t, "tencent"), draft); again[testBigGIF] != stored || shrinks != 1 {
		t.Fatalf("同一份内容再推照用记下的、不再压：%v shrinks=%d", again, shrinks)
	}
}

// TestExternalImagesKeepOriginalWhenShrinkFails 压不成就照原图推（分发服务会拦下、卡片上写着第几张多大），不卡住别的。
func TestExternalImagesKeepOriginalWhenShrinkFails(t *testing.T) {
	stubExternalImages(t, func(context.Context, string) (int64, string, error) { return 6 << 20, "image/gif", nil },
		func(context.Context, []byte, int64) ([]byte, error) { return nil, errTestShrink }, map[string]string{testBigGIF: "big gif"})
	setTestExternalAccounts(t, testArticleAccount(testTencentAccountID, "tencent", false))
	stubDistributionImageSize(t, map[string][2]int{})
	env := newPublishTestEnv(t)
	job := createPublishedDistributionJobWithKey(t, env, "site:big-gif-fail", "FBIF食品饮料创新")
	seedLivePublishedNews(t, env, foodTalksNewsDetail{
		ID: testDistributionNewsID, Title: job.Title, CoverImg: job.CoverURL, Content: testImagesHTML(),
		Summary: job.Summary, SourceName: "FBIF食品饮料创新", PublishStatus: foodTalksPublishStatusPublished,
	})
	snapshot, err := env.pipeline.publishedNewsSnapshotForDistribution(context.Background(), job)
	if err != nil {
		t.Fatal(err)
	}
	if got := env.pipeline.externalImagesFor(snapshot, testPlatform(t, "tencent"), publishExternalDraft{}); got != nil {
		t.Fatalf("压不成不换：%v", got)
	}
}

var errTestShrink = errors.New("ffmpeg 压动图出错")

// stubExternalImages 换掉问大小、压动图、取原图（取原图走 articleFetchHTTPClient，按地址回 bodies 里的字）。
func stubExternalImages(t *testing.T, probe func(context.Context, string) (int64, string, error), shrink func(context.Context, []byte, int64) ([]byte, error), bodies map[string]string) {
	t.Helper()
	prevProbe, prevShrink := externalImageProbe, externalGIFShrink
	externalImageProbe, externalGIFShrink = probe, shrink
	t.Cleanup(func() { externalImageProbe, externalGIFShrink = prevProbe, prevShrink })
	prevClient := articleFetchHTTPClient
	articleFetchHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, ok := bodies[req.URL.String()]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/gif"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	t.Cleanup(func() { articleFetchHTTPClient = prevClient })
}

// TestShrinkGIF 真用 ffmpeg 压：压到上限以内，帧数、时长不变；原尺寸压不下去就缩小；缩到一半还超就报错。
func TestShrinkGIF(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("没装 ffmpeg")
	}
	noisy := testNoiseGIF(t, 160, 120, 12)
	shrunk, err := shrinkGIF(context.Background(), noisy, int64(len(noisy))/2)
	if err != nil {
		t.Fatal(err)
	}
	if len(shrunk) > len(noisy)/2 {
		t.Fatalf("压到上限以内：%d > %d", len(shrunk), len(noisy)/2)
	}
	before, _ := gif.DecodeAll(bytes.NewReader(noisy))
	after, err := gif.DecodeAll(bytes.NewReader(shrunk))
	if err != nil || len(after.Image) != len(before.Image) || gifDuration(after) != gifDuration(before) {
		t.Fatalf("帧数、时长不变：%v %d/%d 帧 %d/%d 毫秒", err, len(after.Image), len(before.Image), gifDuration(after), gifDuration(before))
	}
	if after.Config.Width >= before.Config.Width {
		t.Fatalf("纯噪点原尺寸压不到一半，应缩小：%d", after.Config.Width)
	}
	if _, err := shrinkGIF(context.Background(), noisy, 1000); err == nil || !strings.Contains(err.Error(), "缩到一半还有") {
		t.Fatalf("缩到一半还超就报错：%v", err)
	}
}

// testNoiseGIF 每帧都是随机色块的动图（最难压），每帧 0.1 秒。
func testNoiseGIF(t *testing.T, w, h, frames int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	palette := make(color.Palette, 256)
	for i := range palette {
		palette[i] = color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255}
	}
	g := &gif.GIF{}
	for f := 0; f < frames; f++ {
		img := image.NewPaletted(image.Rect(0, 0, w, h), palette)
		for i := range img.Pix {
			img.Pix[i] = uint8(rng.Intn(256))
		}
		g.Image = append(g.Image, img)
		g.Delay = append(g.Delay, 10)
	}
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, g); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
