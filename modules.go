package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/permissionflow"
)

const (
	appName     = "Toolbox"
	appSubtitle = "轻巧的桌面工具箱"
	// releasePageURL 是 GitHub 发布页，“查看 Releases”和更新说明都指向它。
	releasePageURL = "https://github.com/ShakePeng/toolbox/releases"
)

//go:embed mygo.json
var mygoConfigJSON []byte

// appVersion 用打包后的版本号（Info.plist / exe 资源）；开发构建没有
// 包信息时从编译期嵌入的 mygo.json 读取，避免再手工同步一个版本常量。
func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	var cfg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(mygoConfigJSON, &cfg); err == nil && cfg.Version != "" {
		return cfg.Version
	}
	return "dev"
}

// PermissionStatus 是插件所需系统权限的快照。
type PermissionStatus struct {
	Accessibility   bool
	ScreenRecording bool
}

// permissions 用 mygo 的权限检测读取真实状态，不再写死 false。
func permissions() PermissionStatus {
	return PermissionStatus{
		Accessibility:   permissionflow.Allowed(permissionflow.Accessibility),
		ScreenRecording: permissionflow.Allowed(permissionflow.ScreenRecording),
	}
}

// translationUnavailable 返回翻译不可用的原因，空字符串表示可用。
// translateClipboard 翻译剪贴板内容，供功能中心按钮使用。
// 必须在点击/回车这类事件路径上调用：读剪贴板要求应用已 ready。
func (a *app) translateClipboard() {
	if reason := a.translationUnavailable(); reason != "" {
		a.translationNotice(reason, false)
		return
	}
	a.translateText(mygo.Clipboard.ReadText())
}

func (a *app) translationUnavailable() string {
	if a == nil || !a.settings.TranslationEnabled {
		return "划词翻译未开启，请先在功能中心打开"
	}
	if len(translationProviders(a.resolvedProviders())) == 0 {
		return "请先在功能中心配置并启用至少一个翻译服务"
	}
	return ""
}

// startTranslation 是全局快捷键与托盘入口：读取当前应用的选中文本再翻译。
func (a *app) startTranslation() {
	if reason := a.translationUnavailable(); reason != "" {
		a.translationNotice(reason, false)
		return
	}
	// macOS 的选区读取依赖辅助功能；没有权限时直接提示，避免 AX 返回难懂的错误码。
	// Windows 走模拟 Ctrl+C，不需要授权。
	if runtime.GOOS == "darwin" && !permissionflow.Allowed(permissionflow.Accessibility) {
		a.translationNotice("需要辅助功能权限才能读取选中文本，请到翻译设置中授权", true)
		return
	}
	text, err := readSelectedText()
	if err != nil {
		// 这些失败都发生在浮窗打开之前，用浮窗提示，避免主窗口隐藏时毫无反馈。
		log.Printf("%s: 读取选中文本失败：%v", appName, err)
		a.translationNotice("读取选中文本失败："+err.Error(), true)
		return
	}
	log.Printf("%s: 划词翻译，%d 个字符", appName, len(text))
	a.translateText(text)
}

// translateText 是所有入口的统一出口：全局快捷键传入选区文本，功能中心的
// 「翻译剪贴板」按钮传入剪贴板内容。
func (a *app) translateText(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		// 没拿到文本不算失败：打开浮窗的编辑态，让用户直接输入。
		a.translationNotice("没有可翻译的文字，直接在这里输入要翻译的内容", true)
		return
	}
	target := resolveTargetLang(a.settings.TargetLang, text)
	if a.trans == nil {
		a.trans = newTranslationSession(a)
	}
	a.trans.translate(text, target)
}

// translationNotice 复用翻译浮窗展示一条提示（主窗口可能处于隐藏状态）。
// allowEdit 表示是否允许用户直接在浮窗里手动输入原文。
func (a *app) translationNotice(msg string, allowEdit bool) {
	if a.trans == nil {
		a.trans = newTranslationSession(a)
	}
	a.trans.showNotice(msg, allowEdit)
}

// onPermission 打开系统设置里对应的授权页。
func (a *app) onPermission(kind string) {
	p := permissionflow.Accessibility
	if kind == "screenRecording" {
		p = permissionflow.ScreenRecording
	}
	if err := permissionflow.OpenSettings(p); err != nil {
		a.toast("请手动打开 系统设置 → 隐私与安全性 完成授权")
	}
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
