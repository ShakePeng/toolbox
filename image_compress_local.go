package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"

	"github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"
)

type localImageCompressor struct{}

// pngCompressionLevel 把界面上的 0–6 级别映射到 Go 标准库的三档压缩：
// 标准库只有最快/默认/最优三档，级别数字是为了让用户能表达“更偏速度还是更偏体积”。
func pngCompressionLevel(level int) png.CompressionLevel {
	switch {
	case level <= 1:
		return png.BestSpeed
	case level >= 5:
		return png.BestCompression
	default:
		return png.DefaultCompression
	}
}

func (localImageCompressor) Compress(ctx context.Context, input []byte, format string, opts imageCompressOptions) (imageCompressResult, error) {
	if err := ctx.Err(); err != nil {
		return imageCompressResult{}, err
	}
	img, err := decodeCompressImage(input, format)
	if err != nil {
		return imageCompressResult{}, err
	}
	bounds := img.Bounds()
	width, height := scaledSize(bounds.Dx(), bounds.Dy(), opts.MaxEdge)
	resized := width != bounds.Dx() || height != bounds.Dy()
	if resized {
		output := image.NewNRGBA(image.Rect(0, 0, width, height))
		xdraw.CatmullRom.Scale(output, output.Bounds(), img, bounds, xdraw.Over, nil)
		img = output
	}
	if err := ctx.Err(); err != nil {
		return imageCompressResult{}, err
	}

	var out bytes.Buffer
	switch format {
	case imageFormatJPEG:
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: opts.JPEGQuality})
	case imageFormatPNG:
		err = (&png.Encoder{CompressionLevel: pngCompressionLevel(opts.PNGLevel)}).Encode(&out, img)
	case imageFormatWebP:
		err = webp.Encode(&out, img, webp.Options{Quality: opts.WebPQuality, Method: 4})
	default:
		err = fmt.Errorf("不支持的图片格式：%s", format)
	}
	if err != nil {
		return imageCompressResult{}, fmt.Errorf("编码图片失败：%w", err)
	}
	data := out.Bytes()
	// 没有缩放时，重新编码变大就没有意义，保留原图也避免覆盖操作反而增大文件。
	if !resized && len(data) >= len(input) {
		data = input
	}
	return imageCompressResult{Data: data, Width: width, Height: height, KeyIndex: -1}, nil
}

func decodeCompressImage(data []byte, format string) (image.Image, error) {
	var (
		img image.Image
		err error
	)
	switch format {
	case imageFormatJPEG:
		img, err = jpeg.Decode(bytes.NewReader(data))
	case imageFormatPNG:
		img, err = png.Decode(bytes.NewReader(data))
	case imageFormatWebP:
		img, err = webp.Decode(bytes.NewReader(data), webp.Options{AutoRotate: true})
	default:
		return nil, fmt.Errorf("不支持的图片格式：%s", format)
	}
	if err != nil {
		return nil, fmt.Errorf("读取图片失败：%w", err)
	}
	return img, nil
}
