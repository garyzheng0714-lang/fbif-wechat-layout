package main

// publish_external_cover.go — 外发平台封面：转存与自动放大。
//
// 2026-10-12 用户裁定：封面不合要求时「选一张封面或上传」，上传在侧边栏改稿页（点选、拖入、
// 粘贴），传上去就换上、能推就推；同日问「有没有可能，你直接把我们原先的头条号封面改成对应的
// 那个」：原封面只差一点（放大不超过 externalCoverUpscaleMax 倍就够平台下限，如 #183 的 780×332）
// 时自动放大一份作这个平台的封面，不用人管；差得多的放大会糊，仍请人换。图都转存 OSS（与正文图
// 同库，长期有效），不依赖一次性链接。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

// externalCoverUpscaleMax 自动放大的上限：超过就会明显发糊，交给人换图。
const externalCoverUpscaleMax = 1.5

// externalAutoCoverActor 自动放大的封面记在这个名下（不算人改过）。
const externalAutoCoverActor = "system:auto_cover"

// externalAutoCoverTimeout 取图、放大、转存的总时限；超时就照旧记成待修改。
const externalAutoCoverTimeout = 8 * time.Second

// externalCoverFetch 取原图（测试可替换）。
var externalCoverFetch = fetchExternalCoverBytes

func fetchExternalCoverBytes(ctx context.Context, src string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	resp, err := doOutboundRequest(articleFetchHTTPClient, req, publicWebURLPolicy)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("取封面原图失败（HTTP %d）", resp.StatusCode)
	}
	return readBoundedBody(resp.Body, maxExternalCoverUploadBytes)
}

// externalCoverUpscaleFactor 放大多少倍刚好够下限；已经够、读不到尺寸或要放大太多时返回 0。
func externalCoverUpscaleFactor(width, height, minWidth, minHeight int) float64 {
	if width <= 0 || height <= 0 || (width >= minWidth && height >= minHeight) {
		return 0
	}
	scale := math.Max(float64(minWidth)/float64(width), float64(minHeight)/float64(height))
	if scale > externalCoverUpscaleMax {
		return 0
	}
	return scale
}

// upscaleCoverJPEG 等比放大到宽高都不小于 minWidth×minHeight（透明处垫白），输出 JPEG。
func upscaleCoverJPEG(src []byte, minWidth, minHeight int) ([]byte, int, int, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("识别封面图失败: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 80_000_000 {
		return nil, 0, 0, fmt.Errorf("封面图尺寸异常: %dx%d", config.Width, config.Height)
	}
	scale := externalCoverUpscaleFactor(config.Width, config.Height, minWidth, minHeight)
	if scale == 0 {
		return nil, 0, 0, fmt.Errorf("封面 %d×%d 不能自动放大到 %d×%d", config.Width, config.Height, minWidth, minHeight)
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("解码封面图失败: %w", err)
	}
	width := max(minWidth, int(math.Ceil(float64(config.Width)*scale)))
	height := max(minHeight, int(math.Ceil(float64(config.Height)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	stddraw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 92}); err != nil {
		return nil, 0, 0, fmt.Errorf("编码封面图失败: %w", err)
	}
	return out.Bytes(), width, height, nil
}

// autoUpscaleExternalCover 封面只差一点：把检查出的那张（原封面）放大转存，返回可直接用的封面。
// 不合适（没有封面、人已经换过、差太多）或做不成时返回 false，照旧等人换。
func (p *publishPipeline) autoUpscaleExternalCover(jobID int64, draft publishExternalDraft, issue publishExternalIssue) (publishExternalCoverCandidate, bool) {
	if issue.Missing || strings.TrimSpace(draft.CoverURL) != "" || strings.TrimSpace(issue.Current) == "" ||
		externalCoverUpscaleFactor(issue.Width, issue.Height, issue.MinWidth, issue.MinHeight) == 0 {
		return publishExternalCoverCandidate{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalAutoCoverTimeout)
	defer cancel()
	src, err := externalCoverFetch(ctx, issue.Current)
	if err == nil {
		var data []byte
		var width, height int
		if data, width, height, err = upscaleCoverJPEG(src, issue.MinWidth, issue.MinHeight); err == nil {
			var candidate publishExternalCoverCandidate
			if candidate, err = p.storeExternalCoverBytes(ctx, jobID, data, "jpeg", width, height, externalAutoCoverActor); err == nil {
				return candidate, true
			}
		}
	}
	log.Printf("publish: 任务 #%d 封面自动放大失败，等人换封面: %v", jobID, err)
	return publishExternalCoverCandidate{}, false
}

// storeExternalCoverBytes 封面图转存 OSS，返回带尺寸的候选。
func (p *publishPipeline) storeExternalCoverBytes(ctx context.Context, jobID int64, data []byte, format string, width, height int, actor string) (publishExternalCoverCandidate, error) {
	mime := map[string]string{"jpeg": "image/jpeg", "png": "image/png"}[format]
	ext := map[string]string{"jpeg": "jpg", "png": "png"}[format]
	if mime == "" {
		return publishExternalCoverCandidate{}, errors.New("只支持 JPG、PNG 图片")
	}
	digest := sha256.Sum256(data)
	name := fmt.Sprintf("distribution-cover-%d-%x.%s", jobID, digest[:8], ext)
	var stored string
	var err error
	if p.externalCoverStore != nil {
		stored, err = p.externalCoverStore(ctx, name, data)
	} else {
		var results map[string]string
		results, err = ossUploadByBase64Images(ctx, map[string]string{name: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)})
		stored = results[name]
	}
	if err != nil {
		return publishExternalCoverCandidate{}, fmt.Errorf("图片转存失败：%w", err)
	}
	stored = publicMediaURL(strings.TrimSpace(stored))
	if stored == "" {
		return publishExternalCoverCandidate{}, errors.New("图片转存失败，请重试")
	}
	return publishExternalCoverCandidate{URL: stored, Width: width, Height: height, UploadedBy: actor}, nil
}
