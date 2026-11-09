package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

const (
	appName     = "Toolbox"
	appSubtitle = "轻巧的桌面工具箱"
	appVersion  = "1.0.0"
	// releasePageURL 是 GitHub 发布页，“查看 Releases”和更新说明都指向它。
	releasePageURL = "https://github.com/ShakePeng/toolbox/releases"
)

// PermissionStatus 是插件所需系统权限的快照。
// 阶段一先返回占位值；阶段二用 mygo 权限检测 / 自研 AX 检测替换。
type PermissionStatus struct {
	Accessibility   bool
	ScreenRecording bool
}

// permissions 返回当前系统权限状态（占位实现）。
func permissions() PermissionStatus {
	// TODO(phase2): macOS 用 AXIsProcessTrusted / CGPreflightScreenCaptureAccess 检测。
	return PermissionStatus{Accessibility: false, ScreenRecording: false}
}

// startTranslation 触发一次划词翻译。阶段一为桩：只记录日志并提示。
// 阶段二实现：读取选区文本 -> 并发调用 Provider -> 显示悬浮翻译窗。
func (a *app) startTranslation() {
	a.toast("划词翻译模块将在后续阶段接入")
	log.Printf("%s: 触发划词翻译（阶段一桩实现）", appName)
}

// onPermission 打开系统权限设置页（占位，阶段二接入）。
func (a *app) onPermission(kind string) {
	// TODO(phase2): 打开 macOS 系统设置对应面板。
	a.toast("请在系统设置中授权：" + kind)
}

// trayIcon 画一个工具箱样式的托盘中图标。
// macOS 用纯黑模板图（由系统按明暗着色），其他平台用蓝色以便两种任务栏都可见。
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{R: 79, G: 124, B: 255, A: 255}
	if runtime.GOOS == "darwin" {
		ink = color.NRGBA{A: 255}
	}
	// 箱体
	draw.Draw(img, image.Rect(6, 12, 26, 26), image.NewUniform(ink), image.Point{}, draw.Src)
	// 提手
	draw.Draw(img, image.Rect(12, 7, 20, 10), image.NewUniform(ink), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(10, 9, 12, 13), image.NewUniform(ink), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(20, 9, 22, 13), image.NewUniform(ink), image.Point{}, draw.Src)
	// 中间横条镂空
	draw.Draw(img, image.Rect(6, 18, 26, 20), image.NewUniform(color.NRGBA{}), image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// openAtLogin 读取开机自启状态（兜底 false）。
func openAtLogin() bool {
	return mygo.App.OpenAtLogin()
}
