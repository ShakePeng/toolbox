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
		// 通用 AI 图标配浅色底，用深灰描边保证对比度。
		return ui.Hex("#2b2c30")
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

// 弹框（设置类）走另一套浅色：主窗口固定深色，弹框再用深色就跟页面糊在
// 一起，白底深字才能把弹框从背景里拉出来。
var (
	lightBg         = ui.Hex("#ffffff")
	lightBgSoft     = ui.Hex("#f1f3f5")
	lightBgHover    = ui.Hex("#e7e9ed")
	lightLine       = ui.RGBA(0, 0, 0, 0.09)
	lightLineStrong = ui.RGBA(0, 0, 0, 0.16)
	lightText       = ui.Hex("#1f2328")
	lightTextSoft   = ui.Hex("#3f454e")
	lightTextMuted  = ui.Hex("#6b7280")
	lightCard       = ui.Hex("#f7f8fa")
	lightAccentStr  = ui.Hex("#b45309")
	lightPrimaryTxt = ui.Hex("#ffffff")
	lightDanger     = ui.Hex("#dc2626")
	lightSuccess    = ui.Hex("#15803d")
	lightShadow     = ui.RGBA(0, 0, 0, 0.10)
)

// sToolsLightTheme 是弹框用的浅色主题：底色、文字、分割线换浅色，
// 强调色仍是品牌琥珀（压暗一档，白底上才够对比）。
func sToolsLightTheme() *ui.Theme {
	t := ui.LightTheme()
	t.Dark = false
	t.Background = lightBg
	t.Surface = lightBg
	t.SurfaceHover = lightBgHover
	t.SurfacePressed = lightBgSoft
	t.Border = lightLine
	t.Text = lightText
	t.TextMuted = lightTextMuted
	t.Accent = colAccent
	t.InactiveAccent = colAccent
	t.AccentHover = ui.Hex("#d97706")
	t.AccentPressed = colAccent
	t.AccentText = lightPrimaryTxt
	t.Danger = lightDanger
	t.Warning = ui.Hex("#b45309")
	t.Success = lightSuccess
	t.Selection = ui.RGBA(245, 158, 11, 0.30)
	t.Focus = colAccent
	t.Radius = 10
	t.Spacing = 4
	return t
}

// withLightPalette 在构建弹框子树期间把全局色板换成浅色，让按钮、权限条、
// Provider 卡片这些和深色页面共用的组件也跟着变浅，返回的函数负责恢复。
// 渲染是单线程同步的，弹框子树建完就还原，不会影响主窗口和之后的帧。
func withLightPalette() (restore func()) {
	saved := []ui.Color{
		colBg, colBgElevated, colBgSoft, colBgHover,
		colLine, colLineStrong,
		colText, colTextSoft, colTextMuted,
		colAccentStr, colDanger, colSuccess, colPrimaryTxt, colCard, shadowCard,
	}
	colBg, colBgElevated, colBgSoft, colBgHover = lightBg, lightBg, lightBgSoft, lightBgHover
	colLine, colLineStrong = lightLine, lightLineStrong
	colText, colTextSoft, colTextMuted = lightText, lightTextSoft, lightTextMuted
	colAccentStr, colDanger, colSuccess = lightAccentStr, lightDanger, lightSuccess
	colPrimaryTxt, colCard, shadowCard = lightPrimaryTxt, lightCard, lightShadow
	return func() {
		colBg, colBgElevated, colBgSoft, colBgHover = saved[0], saved[1], saved[2], saved[3]
		colLine, colLineStrong = saved[4], saved[5]
		colText, colTextSoft, colTextMuted = saved[6], saved[7], saved[8]
		colAccentStr, colDanger, colSuccess, colPrimaryTxt, colCard, shadowCard =
			saved[9], saved[10], saved[11], saved[12], saved[13], saved[14]
	}
}
