package main

// publish_external_images.go — 正文动图超了平台上限，推之前压小（2026-11-12 #367）。
//
// 腾讯号正文图由腾讯按地址转存，单张 20MB、动图 5MB 以内（分发服务的平台文件，经 GET /v1/platforms 的 images 给过来）：
// #367 第 1 张 5.6MB 的 GIF 腾讯一直转存不成，正文重写三遍等满 4 分钟没存成。所以推之前（queueExternalDistribution）
// 把这个平台超了上限的动图用 ffmpeg 重新编码：每帧只记变了的那块、用规则抖动（更好压），尺寸、帧数、时长都不变；
// 还超就按 0.9、0.8…倍缩小。压到上限以内转存 OSS，记进这个平台的改稿记录（publishExternalDraft.Images，原地址 → 压小的地址），
// 同一份内容再推还是这个地址、同一个幂等键；只换推给这个平台的，公众号和网站上的不动。
// 不是动图的超了（20MB 以上的照片）不压；压不成（取不到图、ffmpeg 出错、缩到一半还超）就照原图推。这两种分发服务都会拦下
// （422 image_too_large，卡片上写着第几张多大），请人换图。ffmpeg 用服务器上装的（主力服务器 apt 装的 6.1）。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image/gif"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// externalImageTimeout 一个平台所有超限动图的取图、压小、转存一共等多久；过了还没压好的照原图推。
	externalImageTimeout = 2 * time.Minute
	// externalImageFetchMax 取原图最多读多少。
	externalImageFetchMax = 64 << 20
)

// externalGIFScales 压动图试的缩放倍数，按顺序试到不超为止；缩到一半还超就不压了（再小看不清）。
var externalGIFScales = []float64{1, 0.9, 0.8, 0.7, 0.6, 0.5}

// 测试可替换：问一张图多大、什么类型；把动图压到 limit 字节以内。
var (
	externalImageProbe = probeExternalImage
	externalGIFShrink  = shrinkGIF
)

// externalImagesFor 这个平台要换的正文动图：现在正文里超了上限的每一张，上次压好的照用，其余现压。
// 平台不限动图大小、或正文里没有超的，就是 nil。
func (p *publishPipeline) externalImagesFor(job publishJob, profile distributionPlatform, draft publishExternalDraft) map[string]string {
	limit := profile.gifMaxBytes()
	if limit <= 0 {
		return nil
	}
	original := draft
	original.Images = nil
	content, err := buildDistributionContent(job, profile, original)
	if err != nil {
		return draft.Images // 正文解不开，交给构造 payload 那一步报错
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalImageTimeout)
	defer cancel()
	var out map[string]string
	for _, block := range content.Blocks {
		if block.K != "img" || block.Src == "" {
			continue
		}
		small, ok := draft.Images[block.Src]
		if !ok {
			size, kind, err := externalImageProbe(ctx, block.Src)
			if err != nil || kind != "image/gif" || size <= limit {
				continue
			}
			if small, err = p.shrinkExternalGIF(ctx, job.ID, block.Src, limit); err != nil {
				log.Printf("publish: #%d %s正文动图 %s 有 %s，超了 %s 的上限，没压成，照原图推：%v", job.ID, profile.Name, block.Src, byteSize(size), byteSize(limit), err)
				continue
			}
			log.Printf("publish: #%d %s正文动图 %s 有 %s，超了 %s 的上限，压小成 %s", job.ID, profile.Name, block.Src, byteSize(size), byteSize(limit), small)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[block.Src] = small
	}
	return out
}

// shrinkExternalGIF 取原图、压到 limit 以内、转存，返回压小的地址。
func (p *publishPipeline) shrinkExternalGIF(ctx context.Context, jobID int64, src string, limit int64) (string, error) {
	data, err := fetchExternalImage(ctx, src)
	if err != nil {
		return "", err
	}
	small, err := externalGIFShrink(ctx, data, limit)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(small)
	return p.storeExternalMedia(ctx, fmt.Sprintf("distribution-image-%d-%x.gif", jobID, digest[:8]), "image/gif", small)
}

// probeExternalImage 问一张图多大、什么类型（HEAD，不下载）。
func probeExternalImage(ctx context.Context, src string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, src, nil)
	if err != nil {
		return 0, "", err
	}
	resp, err := doOutboundRequest(articleFetchHTTPClient, req, publicWebURLPolicy)
	if err != nil {
		return 0, "", err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("问图片大小失败（HTTP %d）", resp.StatusCode)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	kind, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	return size, strings.ToLower(strings.TrimSpace(kind)), nil
}

func fetchExternalImage(ctx context.Context, src string) ([]byte, error) {
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
		return nil, fmt.Errorf("取原图失败（HTTP %d）", resp.StatusCode)
	}
	return readBoundedBody(resp.Body, externalImageFetchMax)
}

// shrinkGIF 用 ffmpeg 把动图压到 limit 字节以内：先原尺寸，还超就按 externalGIFScales 缩小。帧按原来的时间走（passthrough），
// 压完核对帧数和总时长都没变，变了就不用。
func shrinkGIF(ctx context.Context, data []byte, limit int64) ([]byte, error) {
	before, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("读不了这张动图：%w", err)
	}
	dir, err := os.MkdirTemp("", "fbif-gif-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.gif")
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return nil, err
	}
	smallest := int64(len(data))
	for i, scale := range externalGIFScales {
		resize := ""
		if scale < 1 {
			resize = fmt.Sprintf("scale=trunc(iw*%.2f/2)*2:-2:flags=lanczos,", scale)
		}
		out := filepath.Join(dir, fmt.Sprintf("out-%d.gif", i))
		cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-y", "-i", in,
			"-filter_complex", "[0:v]"+resize+"split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle",
			"-fps_mode", "passthrough", out)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg 压动图出错：%v %s", err, strings.TrimSpace(string(msg)))
		}
		small, err := os.ReadFile(out)
		if err != nil {
			return nil, err
		}
		if int64(len(small)) > limit {
			smallest = min(smallest, int64(len(small)))
			continue
		}
		after, err := gif.DecodeAll(bytes.NewReader(small))
		if err != nil {
			return nil, fmt.Errorf("压出来的动图读不了：%w", err)
		}
		if len(after.Image) != len(before.Image) || gifDuration(after) != gifDuration(before) {
			return nil, fmt.Errorf("压出来的动图帧数或时长变了（%d 帧 %d 毫秒 → %d 帧 %d 毫秒）",
				len(before.Image), gifDuration(before), len(after.Image), gifDuration(after))
		}
		return small, nil
	}
	return nil, errors.New("缩到一半还有 " + byteSize(smallest) + "，压不到 " + byteSize(limit) + " 以内")
}

// gifDuration 动图放一遍多少毫秒。
func gifDuration(g *gif.GIF) int {
	total := 0
	for _, d := range g.Delay {
		total += d * 10
	}
	return total
}

// byteSize 给人看的大小：5.3MB、980KB。
func byteSize(n int64) string {
	if n >= 1<<20 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/(1<<20)), ".0") + "MB"
	}
	return fmt.Sprintf("%dKB", (n+1023)/1024)
}
