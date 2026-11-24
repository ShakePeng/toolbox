package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	imageFormatJPEG = "jpeg"
	imageFormatPNG  = "png"
	imageFormatWebP = "webp"
)

// imageCompressOptions 是本地与云端共用的压缩参数；云端只消费最长边，
// 质量参数属于本地编码器，避免界面展示了实际不生效的选项。
type imageCompressOptions struct {
	JPEGQuality int
	WebPQuality int
	PNGLevel    int // 0–6 的 PNG 优化级别，见 pngCompressionLevel
	MaxEdge     int
}

type imageCompressResult struct {
	Data     []byte
	Width    int
	Height   int
	KeyIndex int // TinyPNG 实际使用的 Key，本地固定为 -1。
	Usage    int // TinyPNG 当月用量；响应未提供时为 0。
}

type imageCompressEngine interface {
	Compress(ctx context.Context, input []byte, format string, opts imageCompressOptions) (imageCompressResult, error)
}

func compressOptionsFromSettings(s ImageCompressSettings) imageCompressOptions {
	return imageCompressOptions{
		JPEGQuality: compressClampInt(s.JPEGQuality, 1, 100, 80),
		WebPQuality: compressClampInt(s.WebPQuality, 1, 100, 80),
		PNGLevel:    compressClampInt(s.PNGLevel, 0, 6, 3),
		MaxEdge:     compressClampInt(s.MaxEdge, 0, 16384, 0),
	}
}

func compressClampInt(v, min, max, fallback int) int {
	if v < min || v > max {
		return fallback
	}
	return v
}

// imageFormatForPath 只按扩展名做入口过滤；真正解码时仍会校验文件内容。
func imageFormatForPath(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return imageFormatJPEG, nil
	case ".png":
		return imageFormatPNG, nil
	case ".webp":
		return imageFormatWebP, nil
	default:
		return "", fmt.Errorf("仅支持 PNG、JPEG、WebP 图片")
	}
}

func imageExtension(format string) string {
	if format == imageFormatJPEG {
		return ".jpg"
	}
	return "." + format
}

// scaledSize 计算最长边缩放后的尺寸：只缩小，不放大，且任一边至少为 1。
func scaledSize(width, height, maxEdge int) (int, int) {
	if width < 1 || height < 1 || maxEdge <= 0 || max(width, height) <= maxEdge {
		return width, height
	}
	if width >= height {
		return maxEdge, max(1, int(float64(height)*float64(maxEdge)/float64(width)+0.5))
	}
	return max(1, int(float64(width)*float64(maxEdge)/float64(height)+0.5)), maxEdge
}

// imageCompressCommand 读取图片压缩的启动器触发词，留空或与内置命令
// 冲突时退回默认值 tiny。触发词此前是内置命令 tiny，v14 起改为用户配置。
func (a *app) imageCompressCommand() string {
	if a == nil {
		return "tiny"
	}
	cmd := strings.TrimSpace(a.settings.ImageCompress.Command)
	if cmd == "" || isBuiltinCommand(cmd) {
		return "tiny"
	}
	return cmd
}

// imageCompressCommandConflict 校验图片压缩触发词是否与内置命令、快速
// 打开触发词或搜索引擎触发词重名；返回空串表示可用。
func (a *app) imageCompressCommandConflict(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if isBuiltinCommand(command) {
		return "“" + command + "”是内置命令，不能作为图片压缩触发词"
	}
	for _, reserved := range []string{a.vscodeCommand(), a.traeCommand(), a.forkCommand()} {
		if strings.EqualFold(command, reserved) {
			return "“" + command + "”已被快速打开触发词占用"
		}
	}
	for _, eng := range a.settings.LauncherWebSearchEngines {
		if strings.EqualFold(strings.TrimSpace(eng.Command), command) {
			return "“" + command + "”已被网页搜索触发词占用"
		}
	}
	return ""
}
