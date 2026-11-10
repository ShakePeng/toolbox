package main

import "github.com/egoist/mygo/ui"

// 复刻 S-Tools 的视觉：深色暖琥珀。色值直接取自原项目 src/style.css。
var (
	colBg         = ui.Hex("#202124")
	colBgElevated = ui.Hex("#2a2b2f")
	colBgSoft     = ui.Hex("#34353a")
	colBgHover    = ui.Hex("#3d3e44")
	colLine       = ui.RGBA(255, 255, 255, 0.095)
	colLineStrong = ui.RGBA(255, 255, 255, 0.16)
	colText       = ui.Hex("#f4f4f5")
	colTextSoft   = ui.Hex("#c8c8ce")
	colTextMuted  = ui.Hex("#9b9ba3")
	colAccent     = ui.Hex("#f59e0b")
	colAccentSoft = ui.RGBA(245, 158, 11, 0.13)
	colAccentStr  = ui.Hex("#ffc15c")
	colDanger     = ui.Hex("#f87171")
	colSuccess    = ui.Hex("#4ade80")
	colCard       = ui.RGBA(48, 49, 54, 0.78) // 半透明卡片底
	colPrimaryTxt = ui.Hex("#18181a")
	colKnob       = ui.Hex("#1d1408")
	shadowCard    = ui.RGBA(0, 0, 0, 0.16)
)

// brandColor 给每个翻译服务一个品牌色。
func brandColor(kind string) ui.Color {
	switch kind {
	case "baidu":
		return ui.Hex("#2932e1")
	case "google":
		return ui.Hex("#4285f4")
	case "openai-compatible":
		return ui.Hex("#111111")
	}
	return colTextMuted
}

// sToolsTheme 是固定的深色主题（原版 color-scheme: dark，不随系统明暗变化）。
func sToolsTheme() *ui.Theme {
	t := ui.DarkTheme()
	t.Dark = true
	t.Background = colBg
	t.Surface = colBgElevated
	t.SurfaceHover = colBgHover
	t.SurfacePressed = colBgSoft
	t.Border = colLine
	t.Text = colText
	t.TextMuted = colTextMuted
	t.Accent = colAccent
	// 窗口失焦时 mygo 默认把强调色调灰（InactiveAccent）；
	// 这里保持琥珀色，开关、焦点环在非激活窗口里也还是品牌色。
	t.InactiveAccent = colAccent
	t.AccentHover = colAccentStr
	t.AccentPressed = colAccent
	t.AccentText = colKnob
	t.Danger = colDanger
	t.Warning = ui.Hex("#fbbf24")
	t.Success = colSuccess
	t.Selection = ui.RGBA(245, 158, 11, 0.30)
	t.Focus = colAccent
	t.Radius = 10
	t.Spacing = 4
	return t
}
