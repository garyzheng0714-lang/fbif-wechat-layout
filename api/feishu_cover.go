package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	_ "image/gif"
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const feishuCoverSquareSize = 512

var feishuCoverBackground = color.RGBA{R: 0xF7, G: 0xF8, B: 0xFA, A: 0xFF}

const (
	feishuThumbWidth  = 960
	feishuThumbHeight = 540
)

// thumbFeishuCoverJPEG 审核聚合卡的封面图：居中裁成 16:9、缩到 960×540，不补边。电脑上
// 显示约 200×113，手机上铺满整行（约 310pt，三倍屏需 ~930px）。公众号头条封面是 2.35:1，
// 左右各裁去约 12%。
func thumbFeishuCoverJPEG(src []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("识别封面图失败: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 80_000_000 {
		return nil, fmt.Errorf("封面图尺寸异常: %dx%d", config.Width, config.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("解码封面图失败: %w", err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	crop := bounds
	if int64(width)*feishuThumbHeight > int64(height)*feishuThumbWidth {
		cropWidth := max(1, int(int64(height)*feishuThumbWidth/feishuThumbHeight))
		left := bounds.Min.X + (width-cropWidth)/2
		crop = image.Rect(left, bounds.Min.Y, left+cropWidth, bounds.Max.Y)
	} else {
		cropHeight := max(1, int(int64(width)*feishuThumbHeight/feishuThumbWidth))
		top := bounds.Min.Y + (height-cropHeight)/2
		crop = image.Rect(bounds.Min.X, top, bounds.Max.X, top+cropHeight)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, feishuThumbWidth, feishuThumbHeight))
	stddraw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	xdraw.CatmullRom.Scale(canvas, canvas.Bounds(), img, crop, xdraw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, canvas, &jpeg.Options{Quality: 88}); err != nil {
		return nil, fmt.Errorf("生成封面小图失败: %w", err)
	}
	return out.Bytes(), nil
}

// squareFeishuCoverPNG 把任意横竖比封面放入方形画布，保持原图完整可见。
// 飞书卡片只展示这个方形素材，不再通过 crop_center 猜测用户想保留的区域。
func squareFeishuCoverPNG(src []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("识别封面图失败: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 80_000_000 {
		return nil, fmt.Errorf("封面图尺寸异常: %dx%d", config.Width, config.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("解码封面图失败: %w", err)
	}

	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	targetWidth, targetHeight := feishuCoverSquareSize, feishuCoverSquareSize
	if width >= height {
		targetHeight = max(1, int((int64(height)*feishuCoverSquareSize+int64(width)/2)/int64(width)))
	} else {
		targetWidth = max(1, int((int64(width)*feishuCoverSquareSize+int64(height)/2)/int64(height)))
	}
	target := image.Rect(
		(feishuCoverSquareSize-targetWidth)/2,
		(feishuCoverSquareSize-targetHeight)/2,
		(feishuCoverSquareSize+targetWidth)/2,
		(feishuCoverSquareSize+targetHeight)/2,
	)
	canvas := image.NewRGBA(image.Rect(0, 0, feishuCoverSquareSize, feishuCoverSquareSize))
	stddraw.Draw(canvas, canvas.Bounds(), image.NewUniform(feishuCoverBackground), image.Point{}, stddraw.Src)
	xdraw.CatmullRom.Scale(canvas, target, img, img.Bounds(), xdraw.Over, nil)

	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&out, canvas); err != nil {
		return nil, fmt.Errorf("生成方形封面失败: %w", err)
	}
	return out.Bytes(), nil
}
