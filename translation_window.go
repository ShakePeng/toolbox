package main

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// 翻译浮窗复刻 S-Tools 的划词翻译面板：品牌头、目标语言切换、
// 原文预览/编辑、各服务卡片（品牌图标、检测语言与耗时）、底部复制。
const (
	translationWidth     = 320
	translationHeight    = 300
	translationMinHeight = 240
	translationMaxHeight = 560
)

type translationSession struct {
	app *app
	win *mygo.Window

	// source 是当前翻译的原文；sourceEdit 是编辑框里的工作副本。
	source     string
	sourceEdit string
	editing    bool

	// pref 是用户在浮窗里选的目标语言偏好（auto 等），
	// target 是按原文自动判断后真正使用的语言代码。
	pref   string
	target string

	results []translationResult
	notice  string
	// noticeSuccess 让“译文已复制”用绿色提示，其余提示用红色。
	noticeSuccess bool

	targetMenu bool

	// reqID 用来丢弃过期请求：连续划词时只让最后一次的结果上屏。
	reqID  int
	closed bool

	editor ui.Handle
}

func newTranslationSession(a *app) *translationSession {
	s := &translationSession{app: a}
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "划词翻译",
		Width:             translationWidth,
		Height:            translationHeight,
		Hidden:            true,
		Frameless:         true,
		Transparent:       true,
		Panel:             true,
		AlwaysOnTop:       true,
		SkipTaskbar:       true,
		DisableResize:     true,
		DisableMinimize:   true,
		DisableMaximize:   true,
		DisableFullScreen: true,
		Content:           ui.View(s.view),
	})
	s.win.OnBlur(func() {
		// 应用退出过程中不再拦焦点，交给系统关闭窗口。
		if s.app.quitting {
			return
		}
		s.hide()
	})
	s.win.OnClose(func(e *mygo.CloseEvent) {
		if s.app.quitting {
			s.closed = true
			return
		}
		e.PreventDefault()
		s.hide()
	})
	return s
}

// translate 展示浮窗并开始一次新的翻译。
func (s *translationSession) translate(text, pref string) {
	s.source = text
	s.sourceEdit = text
	s.pref = pref
	s.target = resolveTargetLang(pref, text)
	s.editing = false
	s.targetMenu = false
	s.notice = ""
	s.noticeSuccess = false

	s.startRequests()
	s.present()
}

// present 在窗口已显示时只重绘，避免重新定位或抢走编辑框焦点。
func (s *translationSession) present() {
	if s.win.IsVisible() {
		s.refresh()
		return
	}
	s.show()
}

// showNotice 在浮窗里展示一条失败原因。allowEdit 为 true 时直接进入
// 原文编辑态，用户可以手动输入要翻译的内容。
func (s *translationSession) showNotice(msg string, allowEdit bool) {
	s.reqID++
	s.source = ""
	s.sourceEdit = ""
	s.pref = s.app.settings.TargetLang
	s.target = ""
	s.results = nil
	s.notice = msg
	s.noticeSuccess = false
	s.editing = allowEdit
	s.targetMenu = false

	s.fitHeight()
	s.refresh()
	s.present()
	if allowEdit {
		// 等浮窗显示、编辑框建好之后再抢焦点。
		time.AfterFunc(120*time.Millisecond, func() { s.editor.Focus() })
	}
}

// startRequests 为当前原文并发请求所有已启用的服务。
// 每张卡片独立更新，先完成的服务先显示。
func (s *translationSession) startRequests() {
	s.reqID++
	id := s.reqID
	usable := translationProviders(s.app.providers)
	s.results = make([]translationResult, len(usable))
	for i, p := range usable {
		s.results[i] = translationResult{
			ProviderID: p.ID,
			Name:       p.Name,
			Kind:       p.Kind,
			Model:      p.Model,
			Loading:    true,
		}
	}

	text, target := s.source, s.target
	s.fitHeight()
	s.refresh()

	for i, p := range usable {
		i, p := i, p
		go func() {
			r := translateOne(p, text, target)
			s.win.Update(func() {
				if s.closed || s.reqID != id {
					return
				}
				if i < len(s.results) {
					s.results[i] = r
				}
				s.fitHeight()
			})
		}()
	}
}

// retry 只重试一张失败的卡片。
func (s *translationSession) retry(idx int) {
	if idx < 0 || idx >= len(s.results) {
		return
	}
	p, ok := s.providerByID(s.results[idx].ProviderID)
	if !ok {
		return
	}
	id := s.reqID
	text, target := s.source, s.target
	s.results[idx].Loading = true
	s.results[idx].Err = ""
	s.results[idx].Text = ""
	s.fitHeight()
	s.refresh()

	go func() {
		r := translateOne(p, text, target)
		s.win.Update(func() {
			if s.closed || s.reqID != id {
				return
			}
			for i := range s.results {
				if s.results[i].ProviderID == r.ProviderID {
					s.results[i] = r
				}
			}
			s.fitHeight()
		})
	}()
}

func (s *translationSession) providerByID(id string) (Provider, bool) {
	for _, p := range s.app.providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

func (s *translationSession) copyResult(idx int) {
	if idx < 0 || idx >= len(s.results) || s.results[idx].Text == "" {
		return
	}
	mygo.Clipboard.WriteText(s.results[idx].Text)
	s.notice = "译文已复制"
	s.noticeSuccess = true
	s.fitHeight()
	s.refresh()
	time.AfterFunc(1800*time.Millisecond, func() {
		s.win.Update(func() {
			if s.closed || s.notice != "译文已复制" {
				return
			}
			s.notice = ""
			s.noticeSuccess = false
			s.fitHeight()
		})
	})
}

func (s *translationSession) firstSuccess() (int, bool) {
	for i, r := range s.results {
		if r.Err == "" && r.Text != "" {
			return i, true
		}
	}
	return 0, false
}

func (s *translationSession) selectTarget(code string) {
	s.targetMenu = false
	if code == s.pref {
		s.refresh()
		return
	}
	s.pref = code
	if s.source == "" {
		s.refresh()
		return
	}
	s.target = resolveTargetLang(code, s.source)
	s.startRequests()
}

// show 把浮窗放在光标附近，并在贴边时自动翻到另一侧，始终留在工作区内。
func (s *translationSession) show() {
	pt := mygo.Screen.CursorScreenPoint()
	disp := mygo.Screen.DisplayNearestPoint(pt)
	area := disp.WorkArea
	b := s.win.Bounds()
	w, h := b.Width, b.Height
	if w <= 0 {
		w = translationWidth
	}
	if h <= 0 {
		h = translationHeight
	}

	const gap = 16
	x := pt.X + gap
	y := pt.Y + gap
	if x+w > area.X+area.Width {
		x = pt.X - w - gap
	}
	if y+h > area.Y+area.Height {
		y = pt.Y - h - gap
	}
	x = max(area.X, min(x, area.X+max(0, area.Width-w)))
	y = max(area.Y, min(y, area.Y+max(0, area.Height-h)))

	s.win.SetPosition(x, y)
	s.win.Show()
	s.win.Focus()
}

func (s *translationSession) hide() {
	if s.win == nil || s.closed {
		return
	}
	s.win.Hide()
}

func (s *translationSession) refresh() {
	if s.win == nil || s.closed {
		return
	}
	s.win.Update(func() {})
}

func (s *translationSession) openSettings() {
	s.hide()
	s.app.section = "translation"
	s.app.showMain()
}

// ---------------- 高度估算 ----------------
// 原生 UI 没有网页那样的自动内容高度，这里按行数估算并调整窗口高度；
// 内容超出时由中间区域的 Scroll 兜底。

func (s *translationSession) fitHeight() {
	if s.win == nil {
		return
	}
	// 逐项累加实际占位：窗口上下边框 2、头部约 53（两行文字 + 底部分隔线）、底部 40。
	// 多留 1px 给不同平台的字体行高，避免 body 溢出后 Scroll 裁掉 results 底部内边距。
	h := 2 + 54 + 40
	if s.editing {
		h += 10 + 136 // 原文编辑卡：上外边距 + 卡片
	} else if s.source != "" {
		h += 10 + 50 // 原文预览卡
	}
	if s.notice != "" {
		h += 9 + 38 // 提示条
	}
	if len(s.results) > 0 {
		h += 10 // results 上内边距
		for i, r := range s.results {
			if i > 0 {
				h += 8 // 卡片间距
			}
			h += resultCardHeight(r)
		}
		h += 10 // results 下内边距
	}
	h = max(translationMinHeight, min(h, translationMaxHeight))
	s.win.SetSize(translationWidth, h)
}

func resultCardHeight(r translationResult) int {
	if r.Loading || r.Err != "" || r.Text == "" {
		return 88
	}
	// 66 = 上下 padding + 服务标题行 + 译文上边距；实测单行卡片约 90。
	return 66 + estimateLines(r.Text)*24
}

// estimateLines 粗略按“中文 2 个宽度单位、其他 1 个”估算换行后的行数。
func estimateLines(text string) int {
	const unitsPerLine = 36
	lines := 0
	for _, line := range strings.Split(text, "\n") {
		units := 0
		for _, r := range line {
			if unicode.Is(unicode.Han, r) {
				units += 2
			} else {
				units++
			}
		}
		n := (units + unitsPerLine - 1) / unitsPerLine
		if n < 1 {
			n = 1
		}
		lines += n
	}
	if lines < 1 {
		return 1
	}
	return lines
}

// ---------------- 界面 ----------------

func (s *translationSession) view(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEscape) {
		if s.targetMenu {
			s.targetMenu = false
			return
		}
		s.hide()
	}
	ui.Themed(c, sToolsTheme()).Fill().Background(ui.Transparent).Children(func() {
		ui.Column(c).Fill().Radius(14).Border(1, colLineStrong).
			Gradient(ui.Hex("#2b2c30"), ui.Hex("#202124"), 180).Children(func() {
			s.header(c)
			s.body(c)
			s.footer(c)
		})
		s.targetDropdown(c)
	})
}

func (s *translationSession) header(c *ui.Context) {
	ui.Row(c).MinHeight(52).Padding(9, 10).AlignItems(ui.Center).Gap(8).
		BorderWidth(0, 0, 1, 0).BorderColor(colLine).Children(func() {
		if s.app.icon != nil {
			ui.Image(c, s.app.icon).Size(24, 24)
		} else {
			iconTile(c, "translate", 24, 7, colAccentSoft, colAccentStr)
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, "划词翻译").FontSize(16).Bold().MaxLines(1)
			ui.Text(c, formatHotkey(s.app.settings.Hotkeys.Translate)+" · 自动翻译").
				FontSize(12).TextColor(colTextMuted).MaxLines(1)
		})

		trigger := ui.Button(c.Key("translation-target"), "").Label("目标语言").
			Height(28).Radius(9).Padding(0, 8).
			Background(ui.RGBA(255, 255, 255, 0.045)).Border(1, colLine)
		trigger.Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon("language")).FontSize(13).TextColor(colTextSoft)
				ui.Text(c, targetShortLabel(s.pref)).FontSize(12).TextColor(colTextSoft)
				ui.Icon(c, icon("chevron-down")).FontSize(11).TextColor(colTextMuted)
			})
		})
		if trigger.Clicked() {
			s.targetMenu = !s.targetMenu
		}
		s.iconButton(c, "settings", "翻译设置", s.openSettings)
		s.iconButton(c, "close", "关闭", s.hide)
	})
}

func (s *translationSession) iconButton(c *ui.Context, name, label string, fn func()) {
	b := ui.Button(c.Key("translation-icon-"+label), "").Label(label).Size(28, 28).
		Center().Radius(8).Background(ui.Transparent).Padding(0)
	b.Children(func() {
		ui.Icon(c, icon(name)).FontSize(16).TextColor(colTextSoft)
	})
	if b.Clicked() {
		fn()
	}
}

// targetDropdown 是目标语言的下拉菜单，绝对定位在头部按钮下方。
func (s *translationSession) targetDropdown(c *ui.Context) {
	if !s.targetMenu {
		return
	}
	ui.Column(c).Absolute().Top(48).Right(70).Width(112).Padding(4).Radius(10).
		Background(ui.RGBA(45, 46, 50, 0.98)).Border(1, colLineStrong).
		Shadow(0, 14, 34, 0, ui.RGBA(0, 0, 0, 0.34)).Gap(2).Children(func() {
		for _, o := range targetLangOptions {
			active := o.Code == s.pref
			textCol, bg := colTextSoft, ui.Transparent
			if active {
				textCol, bg = colAccentStr, colAccentSoft
			}
			b := ui.Button(c.Key("translation-target-"+o.Code), "").Label("目标语言"+o.Label).
				Height(30).Radius(7).Padding(0, 8).Justify(ui.Start).Background(bg)
			b.Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, targetShortLabel(o.Code)).FontSize(12).TextColor(textCol)
					if active {
						ui.Spacer(c)
						ui.Icon(c, icon("check")).FontSize(12).TextColor(colAccentStr)
					}
				})
			})
			if b.Clicked() {
				s.selectTarget(o.Code)
			}
		}
	})
}

func (s *translationSession) body(c *ui.Context) {
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(0).Children(func() {
			if s.editing {
				s.editorSection(c)
			} else if s.source != "" {
				s.sourcePreview(c)
			}
			s.noticeSection(c)
			s.resultsSection(c)
		})
	})
}

func (s *translationSession) sourcePreview(c *ui.Context) {
	ui.Row(c).Margin(10, 10, 0, 10).Padding(10).Radius(11).Border(1, colLine).
		Background(ui.RGBA(255, 255, 255, 0.035)).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Text(c, s.source).Grow(1).MinWidth(0).FontSize(13).TextColor(colTextSoft).
			LineHeight(1.55).MaxLines(2)
		if ghostBtn(c, "编辑原文").Label("编辑原文").MinHeight(28).Clicked() {
			s.editing = true
			s.sourceEdit = s.source
			s.fitHeight()
			s.refresh()
			time.AfterFunc(80*time.Millisecond, func() { s.editor.Focus() })
		}
	})
}

func (s *translationSession) editorSection(c *ui.Context) {
	ui.Column(c).Margin(10, 10, 0, 10).Padding(10).Radius(11).Border(1, colLine).
		Background(ui.RGBA(255, 255, 255, 0.035)).Gap(8).Children(func() {
		ui.TextArea(c, &s.sourceEdit).Label("原文").Placeholder("未读取到选中文本，可在这里手动输入…").
			Height(76).Bind(&s.editor)
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Text(c, fmt.Sprintf("%d 字符", len([]rune(s.sourceEdit)))).
				FontSize(12).TextColor(colTextMuted)
			ui.Spacer(c)
			if s.sourceEdit != s.source && ghostBtn(c, "收起").Label("收起").MinHeight(28).Clicked() {
				s.editing = false
				s.sourceEdit = s.source
				s.fitHeight()
				s.refresh()
			}
			if translateButton(c, "翻译").Clicked() {
				text := strings.TrimSpace(s.sourceEdit)
				if text == "" {
					s.notice = "请选择或输入需要翻译的文本"
					s.noticeSuccess = false
					s.fitHeight()
					s.refresh()
					return
				}
				s.translate(text, s.pref)
			}
		})
	})
}

func translateButton(c *ui.Context, label string) ui.Element {
	b := ui.Button(c.Key("translation-"+label), "").Label(label).
		Height(30).Radius(8).Padding(0, 11).Background(colText).Bold()
	b.TextColor(colPrimaryTxt)
	b.Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon("translate")).FontSize(14).TextColor(colPrimaryTxt)
			ui.Text(c, label).FontSize(12).Bold().TextColor(colPrimaryTxt)
		})
	})
	return b
}

func (s *translationSession) noticeSection(c *ui.Context) {
	if s.notice == "" {
		return
	}
	border, bg, fg := ui.RGBA(248, 113, 113, 0.16), ui.RGBA(248, 113, 113, 0.1), colDanger
	iconName := "alert"
	if s.noticeSuccess {
		border, bg, fg = ui.RGBA(74, 222, 128, 0.18), ui.RGBA(74, 222, 128, 0.09), colSuccess
		iconName = "check"
	}
	ui.Row(c).Margin(9, 10, 0, 10).Padding(9, 10).Radius(9).Border(1, border).
		Background(bg).AlignItems(ui.Center).Gap(7).Children(func() {
		ui.Icon(c, icon(iconName)).FontSize(14).TextColor(fg)
		ui.Text(c, s.notice).Grow(1).MinWidth(0).FontSize(12).TextColor(fg).LineHeight(1.45)
	})
}

func (s *translationSession) resultsSection(c *ui.Context) {
	usable := translationProviders(s.app.providers)
	if len(usable) == 0 && len(s.results) == 0 {
		ui.Row(c).Margin(10).Padding(13).Radius(11).Border(1, colLineStrong).
			AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Icon(c, icon("provider")).FontSize(20).TextColor(colTextMuted)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, "还没有可用的翻译服务").FontSize(14).TextColor(colText)
				ui.Text(c, "先在翻译页中完成配置并启用服务。").FontSize(12).TextColor(colTextMuted)
			})
			if ghostBtn(c, "去配置").Label("去配置").MinHeight(28).Clicked() {
				s.openSettings()
			}
		})
		return
	}

	if len(s.results) == 0 {
		return
	}
	ui.Column(c).Padding(10).Gap(8).Children(func() {
		for i := range s.results {
			s.resultCard(c, i)
		}
	})
}

func (s *translationSession) resultCard(c *ui.Context, idx int) {
	r := s.results[idx]
	ui.Column(c).Padding(11).Radius(11).Border(1, colLine).
		Background(ui.RGBA(255, 255, 255, 0.04)).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			if svg := providerIcon(r.Kind); svg != nil {
				ui.Row(c).Size(26, 26).Center().Children(func() {
					ui.Icon(c, svg).FontSize(22).TextColor(brandColor(r.Kind))
				})
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, r.Name).FontSize(14).Bold().MaxLines(1)
				ui.Text(c, s.resultMeta(r)).FontSize(12).TextColor(colTextMuted).MaxLines(1)
			})
			switch {
			case r.Loading:
			case r.Err != "":
				s.iconButton(c, "refresh", "重试"+r.Name, func() { s.retry(idx) })
			case r.Text != "":
				s.iconButton(c, "copy", "复制"+r.Name, func() { s.copyResult(idx) })
			}
		})

		switch {
		case r.Loading:
			ui.Row(c).Margin(9, 0, 0, 0).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "翻译中…").FontSize(12).TextColor(colTextMuted)
			})
		case r.Err != "":
			ui.Row(c).Margin(9, 0, 0, 0).AlignItems(ui.Start).Gap(6).Children(func() {
				ui.Icon(c, icon("alert")).FontSize(15).TextColor(colDanger)
				ui.Text(c, r.Err).Grow(1).MinWidth(0).FontSize(12).TextColor(colDanger).LineHeight(1.45)
			})
		case r.Text != "":
			ui.Text(c, r.Text).Margin(9, 0, 0, 0).FontSize(14).TextColor(colText).LineHeight(1.7)
		default:
			ui.Text(c, "等待翻译").Margin(9, 0, 0, 0).FontSize(12).TextColor(colTextMuted)
		}
	})
}

func (s *translationSession) resultMeta(r translationResult) string {
	if r.Loading {
		if r.Model != "" {
			return r.Model
		}
		return "正在请求"
	}
	if r.Err != "" {
		return "请求失败"
	}
	detected := r.Detected
	if detected == "" {
		detected = "自动"
	}
	target := s.target
	if target == "" {
		target = "自动"
	}
	return fmt.Sprintf("%s → %s · %dms", detected, target, r.ElapsedMS)
}

func (s *translationSession) footer(c *ui.Context) {
	ui.Row(c).MinHeight(40).Padding(6, 10).AlignItems(ui.Center).Gap(8).
		BorderWidth(1, 0, 0, 0).BorderColor(colLine).Children(func() {
		ui.Text(c, "Esc 关闭 · 点击外部隐藏").FontSize(12).TextColor(colTextMuted).MaxLines(1)
		ui.Spacer(c)
		if idx, ok := s.firstSuccess(); ok {
			if ghostBtn(c, "复制译文").Label("复制译文").MinHeight(28).Clicked() {
				s.copyResult(idx)
			}
		}
	})
}

// targetShortLabel 是浮窗里使用的紧凑语言名。
func targetShortLabel(code string) string {
	switch code {
	case "auto":
		return "自动"
	case "zh-CN":
		return "简中"
	case "zh-TW":
		return "繁中"
	case "en":
		return "英语"
	case "ja":
		return "日语"
	case "ko":
		return "韩语"
	}
	return "自动"
}
