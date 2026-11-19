package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/gen2brain/webp"
)

func testImageBytes(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 11), B: uint8(x * y), A: 255})
		}
	}
	var out bytes.Buffer
	var err error
	switch format {
	case imageFormatJPEG:
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 95})
	case imageFormatPNG:
		err = png.Encode(&out, img)
	case imageFormatWebP:
		err = webp.Encode(&out, img, webp.Options{Quality: 90})
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestLocalImageCompressorFormats(t *testing.T) {
	opts := imageCompressOptions{JPEGQuality: 80, WebPQuality: 80, PNGLevel: 6}
	for _, format := range []string{imageFormatJPEG, imageFormatPNG, imageFormatWebP} {
		t.Run(format, func(t *testing.T) {
			input := testImageBytes(t, format, 96, 64)
			result, err := (localImageCompressor{}).Compress(context.Background(), input, format, opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Data) == 0 || result.Width != 96 || result.Height != 64 || result.KeyIndex != -1 {
				t.Fatalf("压缩结果异常：%+v", result)
			}
			if _, err := decodeCompressImage(result.Data, format); err != nil {
				t.Fatalf("压缩结果无法解码：%v", err)
			}
		})
	}
}

func TestLocalImageCompressorResizeOnlyShrinks(t *testing.T) {
	opts := imageCompressOptions{JPEGQuality: 80, WebPQuality: 80, PNGLevel: 6, MaxEdge: 40}
	input := testImageBytes(t, imageFormatPNG, 96, 64)
	result, err := (localImageCompressor{}).Compress(context.Background(), input, imageFormatPNG, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Width != 40 || result.Height != 27 {
		t.Fatalf("等比例缩放应为 40×27，实际 %d×%d", result.Width, result.Height)
	}
	img, err := decodeCompressImage(result.Data, imageFormatPNG)
	if err != nil || img.Bounds().Dx() != 40 || img.Bounds().Dy() != 27 {
		t.Fatalf("输出尺寸不正确：%v, bounds=%v", err, img.Bounds())
	}

	if w, h := scaledSize(20, 10, 40); w != 20 || h != 10 {
		t.Fatalf("小图不应放大，实际 %d×%d", w, h)
	}
	if w, h := scaledSize(1, 1000, 10); w != 1 || h != 10 {
		t.Fatalf("极窄图短边至少为 1，实际 %d×%d", w, h)
	}
}

func TestLocalImageCompressorRejectsInvalidData(t *testing.T) {
	_, err := (localImageCompressor{}).Compress(context.Background(), []byte("not an image"), imageFormatPNG, compressOptionsFromSettings(defaultImageCompressSettings()))
	if err == nil {
		t.Fatal("坏图应返回错误")
	}
}

func TestImageFormatForPath(t *testing.T) {
	for path, want := range map[string]string{"a.JPG": imageFormatJPEG, "a.jpeg": imageFormatJPEG, "a.PNG": imageFormatPNG, "a.webp": imageFormatWebP} {
		if got, err := imageFormatForPath(path); err != nil || got != want {
			t.Errorf("%s: got %q, %v", path, got, err)
		}
	}
	if _, err := imageFormatForPath("a.gif"); err == nil {
		t.Fatal("GIF 不应进入当前压缩队列")
	}
}

// 队列缩略图必须真的缩小：直接把原图交给 ui.DecodeBitmap 会让每张图都留一份整图纹理。
func TestThumbnailFromBytesShrinks(t *testing.T) {
	for _, format := range []string{imageFormatPNG, imageFormatJPEG, imageFormatWebP} {
		t.Run(format, func(t *testing.T) {
			thumb, err := thumbnailFromBytes(testImageBytes(t, format, 400, 200), format)
			if err != nil {
				t.Fatal(err)
			}
			if thumb == nil {
				t.Fatal("缩略图不该为空")
			}
			if w, h := thumb.Size(); w != thumbnailMaxEdge || h != thumbnailMaxEdge/2 {
				t.Fatalf("缩略图尺寸应为 96×48，实际 %d×%d", w, h)
			}
		})
	}
}

// 小于缩略图上限的图片不放大。
func TestThumbnailFromBytesKeepsSmallImages(t *testing.T) {
	thumb, err := thumbnailFromBytes(testImageBytes(t, imageFormatPNG, 40, 30), imageFormatPNG)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := thumb.Size(); w != 40 || h != 30 {
		t.Fatalf("小图不该放大，实际 %d×%d", w, h)
	}
}
