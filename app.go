package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"
)

// app 是整个界面的状态。字段都在主线程读写：view 与各种回调。
type app struct {
	win  *mygo.Window
	tray *mygo.Tray

	configPath string
	icon       *ui.Bitmap

	loading bool
	loadErr string
	section string // overview | translation | screenshot | settings

	settings  Settings
	providers []Provider

	langSel string

	draft     *providerDraft
	recording string

	settingsSaving bool

	// updatesOn 表示更新插件已装配（main 里 mygo.Use 之后才为 true）。
	// 单元测试不跑 main，靠它跳过更新相关的调用。
	updatesOn bool
	// available 是更新插件发现的新版本号，空表示没有可用更新。
	available string

	toastMsg string

	// quitting 表示应用正在退出，窗口的关闭拦截要让路。
	quitting bool

	registered map[string]string

	capture *captureSession
}

type sectionItem struct {
	id    string
	label string
	icon  string
}

func newApp() *app {
	return &app{
		section:    "overview",
		registered: map[string]string{},
	}
}

func (a *app) load() {
	a.loading = true
	defer func() { a.loading = false }()

	cfg := defaultConfig()
	path, err := configPath()
	if err == nil {
		a.configPath = path
		loaded, cerr := loadConfig(path)
		cfg = loaded
		if cerr != nil {
			a.loadErr = cerr.Error()
		}
	} else {
		a.loadErr = err.Error()
	}
	a.settings = cfg.Settings
	a.providers = cfg.Providers
	a.langSel = langLabel(a.settings.TargetLang)
}

func (a *app) persist() error {
	if a.configPath == "" {
		p, err := configPath()
		if err != nil {
			return err
		}
		a.configPath = p
	}
	return saveConfig(a.configPath, Config{
		SchemaVersion: 1,
		Settings:      a.settings,
		Providers:     a.providers,
	})
}

func (a *app) toast(msg string) {
	a.toastMsg = msg
	if a.win == nil {
		return
	}
	time.AfterFunc(2600*time.Millisecond, func() {
		a.win.Update(func() {
			if a.toastMsg == msg {
				a.toastMsg = ""
			}
		})
	})
}

// ---------------- 视图根 ----------------

func (a *app) view(c *ui.Context) {
	th := sToolsTheme()

	if a.recording != "" && c.Shortcut(0, ui.KeyEscape) {
		a.recording = ""
	}
	c.OnShortcut(ui.Cmd, ui.KeyQ, mygo.App.Quit)
	if !a.sectionVisible(a.section) {
		a.section = "overview"
	}

	ui.Themed(c, th).Fill().Children(func() {
		shell := ui.Column(c).Fill()
		// 深色纵向渐变 + 顶部暖色光晕，对齐原版 body 背景。
		shell.Gradient(ui.Hex("#292a2e"), colBg, 180)
		shell.Children(func() {
			// 光晕要铺满整个窗口：之前限高 380，半径 520 的光晕在底部被硬切，
			// 渐变会突然断掉，露出一条横向接缝。
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).IgnoreInput(true).
				RadialGradient(ui.RadialGradient{
					Stops:  []ui.GradientStop{{Color: colAccentSoft, At: 0}, {Color: ui.Transparent, At: 1}},
					Center: ui.Point{X: 0.72, Y: 0},
					Radius: 520,
				})
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				a.sidebar(c)
				a.content(c)
			})
			if a.toastMsg != "" {
				ui.Row(c).Absolute().LeftPercent(50).Bottom(26).TranslatePercent(-50, 0).
					IgnoreInput(true).Padding(11, 17).Radius(999).Background(ui.RGBA(52, 53, 58, 0.96)).
					Border(1, colLineStrong).Shadow(0, 18, 50, 0, ui.RGBA(0, 0, 0, 0.35)).Children(func() {
					ui.Text(c, a.toastMsg).FontSize(12).TextColor(colText)
				})
			}
			a.providerModal(c)
		})
	})
}

// ---------------- 响应式布局 ----------------
// 参考原版 media query：窗口变窄（<980）时侧栏收窄、内边距变小；
// 各网格按可用内容宽度自动降列，窄时堆叠成单列。

func (a *app) winWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	return w
}

func (a *app) compact(c *ui.Context) bool { return a.winWidth(c) < 980 }

func (a *app) sidebarWidth(c *ui.Context) float32 {
	if a.compact(c) {
		return 196
	}
	return 232
}

func (a *app) contentPad(c *ui.Context) float32 {
	if a.compact(c) {
		return 24
	}
	return 32
}

// contentWidth 估算工作区可用宽度（= 窗口宽 - 侧栏 - 左右内边距，封顶 1120）。
func (a *app) contentWidth(c *ui.Context) float32 {
	pad := a.contentPad(c)
	w := a.winWidth(c) - a.sidebarWidth(c) - 2*pad
	if w < 0 {
		w = 0
	}
	if w > 1120 {
		w = 1120
	}
	return w
}

// twoColumn 表示两列布局够不够宽，否则堆叠。
func (a *app) twoColumn(c *ui.Context) bool { return a.contentWidth(c) >= 560 }

func (a *app) providerColumns(c *ui.Context) int {
	switch cw := a.contentWidth(c); {
	case cw >= 660:
		return 3
	case cw >= 430:
		return 2
	default:
		return 1
	}
}

func (a *app) sidebar(c *ui.Context) {
	ui.Column(c).Width(a.sidebarWidth(c)).Shrink(0).Padding(24, 18, 20, 18).Gap(30).
		Background(ui.RGBA(42, 43, 47, 0.82)).BorderColor(colLine).BorderEndWidth(1).Children(func() {
		// 品牌
		ui.Row(c).Gap(11).AlignItems(ui.Center).Padding(4, 8).Children(func() {
			if a.icon != nil {
				ui.Image(c, a.icon).Size(38, 38)
			}
			ui.Column(c).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, "Toolbox").FontSize(16).Bold()
					ui.Text(c, "v"+appVersion()).FontSize(11).TextColor(colTextMuted)
				})
				ui.Text(c, appSubtitle).FontSize(12).TextColor(colTextMuted)
			})
		})
		// 导航
		ui.Column(c).Gap(6).Children(func() {
			for _, it := range a.visibleSections() {
				a.navItem(c, it)
			}
		})
	})
}

func (a *app) navItem(c *ui.Context, it sectionItem) {
	active := a.section == it.id
	ic, lc := colTextSoft, colTextSoft
	bg := ui.Transparent
	if active {
		bg, ic, lc = colAccentSoft, colAccentStr, colAccentStr
	}
	b := ui.Button(c.Key("nav-"+it.id), "").Label(it.label).
		Height(42).Radius(11).Padding(0, 12).Justify(ui.Start).Background(bg)
	b.Children(func() {
		ui.Row(c).Gap(11).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon(it.icon)).FontSize(18).TextColor(ic)
			ui.Text(c, it.label).FontSize(13).TextColor(lc)
		})
	})
	if b.Clicked() {
		a.section = it.id
	}
}

func (a *app) content(c *ui.Context) {
	ui.Column(c).Grow(1).MinWidth(0).Children(func() {
		if a.loading {
			ui.Column(c).Fill().Center().Gap(12).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "正在读取本地配置…").FontSize(13).TextColor(colTextMuted)
			})
			return
		}
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Row(c).Justify(ui.Center).Children(func() {
				pad := a.contentPad(c)
				ui.Column(c).WidthPercent(100).MaxWidth(1120).Padding(32, pad, 42, pad).Gap(16).Children(func() {
					if a.loadErr != "" {
						ui.Row(c).Gap(9).AlignItems(ui.Center).Padding(13, 16).Radius(12).
							Background(ui.RGBA(255, 107, 104, 0.1)).Children(func() {
							ui.Icon(c, icon("alert")).FontSize(15).TextColor(colDanger)
							ui.Text(c, a.loadErr).FontSize(12).TextColor(colDanger)
						})
					}
					switch a.section {
					case "overview":
						a.overviewSection(c)
					case "translation":
						a.translationSection(c)
					case "screenshot":
						a.screenshotSection(c)
					case "settings":
						a.settingsSection(c)
					}
				})
			})
		})
	})
}

// ---------------- 概览 ----------------

func (a *app) overviewSection(c *ui.Context) {
	a.intro(c, "PLUGINS", "插件概览", "按需启用插件。关闭后隐藏对应 Tab，并注销插件快捷键。", nil)
	if a.twoColumn(c) {
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			a.featureCard(c, "translate", "划词翻译", "在任意应用中选中文本，通过快捷键打开翻译浮窗。",
				&a.settings.TranslationEnabled, "translation").Grow(1)
			a.featureCard(c, "screenshot", "截图标注", "捕获当前屏幕，完成标注、复制和保存。",
				&a.settings.ScreenshotEnabled, "screenshot").Grow(1)
		})
	} else {
		ui.Column(c).Gap(14).Children(func() {
			a.featureCard(c, "translate", "划词翻译", "在任意应用中选中文本，通过快捷键打开翻译浮窗。",
				&a.settings.TranslationEnabled, "translation")
			a.featureCard(c, "screenshot", "截图标注", "捕获当前屏幕，完成标注、复制和保存。",
				&a.settings.ScreenshotEnabled, "screenshot")
		})
	}
}

func (a *app) featureCard(c *ui.Context, iconName, title, desc string, on *bool, kind string) ui.Element {
	bc := colLine
	ibg, ifg := colBgSoft, colTextSoft
	if *on {
		bc = ui.RGBA(245, 158, 11, 0.34)
		ibg, ifg = colAccentSoft, colAccentStr
	}
	status := "默认关闭"
	if *on {
		status = "已开启"
	}
	return ui.Row(c).MinWidth(0).Gap(14).AlignItems(ui.Center).Padding(18).Radius(17).
		Background(colCard).Border(1, bc).Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		iconTile(c, iconName, 42, 12, ibg, ifg)
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, title).FontSize(15).Bold()
				ui.Text(c, status).FontSize(11).TextColor(colTextMuted)
			})
			ui.Text(c, desc).FontSize(12).TextColor(colTextMuted)
		})
		if ui.Switch(c, on).Label(title + "开关").Changed() {
			a.afterFeatureChange(kind)
		}
	})
}

// ---------------- 翻译 ----------------

func (a *app) translationSection(c *ui.Context) {
	enabled := 0
	for _, p := range a.providers {
		if p.Enabled && p.Configured {
			enabled++
		}
	}
	a.intro(c, "PROVIDERS", "划词翻译", "按需启用，可同时对照多个服务的结果。密钥仅保存在本机应用配置中。", func() {
		pill(c, fmt.Sprintf("%d 个服务已启用", enabled), colAccentSoft, colAccentStr)
	})

	a.permissionStrip(c, "辅助功能权限", "用于读取当前应用中被选中的文本。", "accessibility", permissions().Accessibility)

	hotkeyBody := func() { a.hotkeyRecorder(c, "translate") }
	langBody := func() { a.langSelect(c) }
	if a.twoColumn(c) {
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			a.settingsSection2(c, "keyboard", "翻译快捷键", "选中文本后按此组合键打开翻译浮窗。", hotkeyBody).Grow(1)
			a.settingsSection2(c, "language", "翻译偏好", "“自动”会按选中文本是否含中文决定目标语言。", langBody).Grow(1)
		})
	} else {
		ui.Column(c).Gap(14).Children(func() {
			a.settingsSection2(c, "keyboard", "翻译快捷键", "选中文本后按此组合键打开翻译浮窗。", hotkeyBody)
			a.settingsSection2(c, "language", "翻译偏好", "“自动”会按选中文本是否含中文决定目标语言。", langBody)
		})
	}

	a.saveBar(c, "保存翻译设置")

	a.providerGrid(c)
}

// providerGrid 按可用宽度把服务卡排成 3/2/1 列。
func (a *app) providerGrid(c *ui.Context) {
	cols := a.providerColumns(c)
	if cols <= 1 {
		ui.Column(c).Gap(14).Children(func() {
			for i := range a.providers {
				a.providerCard(c, i)
			}
		})
		return
	}
	for i := 0; i < len(a.providers); i += cols {
		end := min(i+cols, len(a.providers))
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			for j := i; j < end; j++ {
				a.providerCard(c, j).Grow(1)
			}
			for k := end - i; k < cols; k++ {
				ui.Box(c).Grow(1)
			}
		})
	}
}

func (a *app) providerCard(c *ui.Context, i int) ui.Element {
	p := &a.providers[i]
	bc := colLine
	if p.Enabled {
		bc = ui.RGBA(245, 158, 11, 0.35)
	}
	return ui.Column(c).MinWidth(0).MinHeight(210).Padding(18).Radius(17).
		Background(colCard).Border(1, bc).Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Size(38, 38).Center().Radius(11).Background(ui.Hex("#f4f4f6")).Children(func() {
				ui.Icon(c, providerIcon(p.Kind)).FontSize(21).TextColor(brandColor(p.Kind))
			})
			ui.Spacer(c)
			if ui.Switch(c, &p.Enabled).Label(p.Name + "启用").Changed() {
				a.afterProviderChange()
			}
		})
		ui.Column(c).Gap(6).Margin(18, 0, 0, 0).Children(func() {
			ui.Text(c, p.Name).FontSize(15).Bold()
			ui.Text(c, p.Description).FontSize(12).TextColor(colTextSoft)
		})
		ui.Spacer(c)
		ui.Row(c).AlignItems(ui.Center).Margin(18, 0, 0, 0).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if p.Configured {
					pill(c, "已配置", ui.RGBA(82, 211, 154, 0.12), colSuccess)
				} else {
					pill(c, "待配置", colBgSoft, colTextSoft)
				}
				proxy := "直连"
				if p.UseSystemProxy {
					proxy = "系统代理"
				}
				pill(c, proxy, colBgSoft, colTextSoft)
			})
			ui.Spacer(c)
			if ghostBtn(c, "配置").Label(p.Name + "配置").Clicked() {
				a.openProvider(i)
			}
		})
	})
}

// ---------------- 截图 ----------------

func (a *app) screenshotSection(c *ui.Context) {
	a.intro(c, "SCREENSHOT", "截图标注", "冻结当前屏幕，完成框选、标注、复制和保存。", func() {
		if primaryBtn(c, "保存截图设置").Clicked() {
			a.saveSettingsUI()
		}
	})

	a.permissionStrip(c, "屏幕录制权限", "用于捕获当前显示器画面并进入标注编辑。", "screenRecording", permissions().ScreenRecording)

	a.captureConfigPanel(c)
}

// captureConfigPanel 是截图页右侧的快捷键面板。
func (a *app) captureConfigPanel(c *ui.Context) ui.Element {
	return ui.Column(c).Padding(20).Radius(18).Gap(16).Background(colBgElevated).
		Border(1, colLine).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			iconTile(c, "keyboard", 38, 11, ui.RGBA(82, 211, 154, 0.11), colSuccess)
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, "截图快捷键").FontSize(13).Bold()
				ui.Text(c, "按下快捷键即可进入截图模式").FontSize(11).TextColor(colTextMuted)
			})
		})
		a.hotkeyRecorder(c, "screenshot")
	})
}

// ---------------- 设置 ----------------

func (a *app) settingsSection(c *ui.Context) {
	ui.Column(c).Padding(20).Radius(17).Background(colCard).Border(1, colLine).
		Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		ui.Row(c).Gap(13).AlignItems(ui.Start).Margin(0, 0, 18, 0).Children(func() {
			iconTile(c, "settings", 38, 11, colBgSoft, colTextSoft)
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, "应用行为").FontSize(16).Bold()
				ui.Text(c, "关闭主窗口后应用仍驻留托盘。").FontSize(13).TextColor(colTextSoft)
			})
		})
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "开机自动启动").FontSize(13)
				ui.Text(c, "登录系统后在托盘常驻").FontSize(11).TextColor(colTextMuted)
			})
			if ui.Switch(c, &a.settings.LaunchAtLogin).Label("开机自动启动").Changed() {
				if err := mygo.App.SetOpenAtLogin(a.settings.LaunchAtLogin); err != nil {
					a.toast("设置开机自启失败：" + err.Error())
				}
				_ = a.persist()
			}
		})
	})

	a.saveBar(c, "保存设置")

	// 更新 hero
	ui.Row(c).Gap(20).AlignItems(ui.Center).Padding(24).Radius(19).Background(colCard).
		Border(1, colLine).Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		if a.icon != nil {
			ui.Image(c, a.icon).Size(64, 64)
		}
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "TOOLBOX DESKTOP").FontSize(11).Bold().TextColor(colTextMuted).LetterSpacing(1.4)
			ui.Text(c, "版本 "+appVersion()).FontSize(25).Bold()
			ui.Text(c, a.updateHint()).FontSize(13).TextColor(colTextSoft)
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if primaryBtn(c, "检查更新").Clicked() {
				a.checkUpdate()
			}
			if secondaryBtn(c, "查看 Releases").Clicked() {
				_ = mygo.Shell.OpenExternal(releasePageURL)
			}
		})
	})

	// 诊断
	ui.Column(c).Padding(20).Radius(17).Background(colCard).Border(1, colLine).
		Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		ui.Row(c).Gap(13).AlignItems(ui.Center).Children(func() {
			iconTile(c, "provider", 38, 11, colBgSoft, colTextSoft)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "诊断").FontSize(16).Bold()
				ui.Text(c, "查看启动、截图和更新过程中的错误记录。").FontSize(13).TextColor(colTextSoft)
			})
			if secondaryBtn(c, "打开日志").Clicked() {
				a.openLog()
			}
		})
	})
}

// ---------------- 部件 ----------------

func (a *app) intro(c *ui.Context, eyebrow, title, desc string, right func()) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Text(c, eyebrow).FontSize(11).Bold().TextColor(colTextMuted).LetterSpacing(1.4)
			ui.Text(c, title).FontSize(25).Bold().LetterSpacing(-0.8)
			ui.Text(c, desc).FontSize(13).TextColor(colTextSoft)
		})
		if right != nil {
			right()
		}
	})
}

func (a *app) settingsSection2(c *ui.Context, iconName, title, desc string, body func()) ui.Element {
	return ui.Column(c).MinWidth(0).Padding(20).Radius(17).Background(colCard).
		Border(1, colLine).Shadow(0, 2, 12, 0, shadowCard).Gap(18).Children(func() {
		ui.Row(c).Gap(13).AlignItems(ui.Start).Children(func() {
			iconTile(c, iconName, 38, 11, colBgSoft, colTextSoft)
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, title).FontSize(16).Bold()
				ui.Text(c, desc).FontSize(13).TextColor(colTextSoft)
			})
		})
		body()
	})
}

func (a *app) permissionStrip(c *ui.Context, title, desc, kind string, ok bool) {
	ui.Row(c).Gap(20).AlignItems(ui.Center).Padding(18, 20).Radius(16).Background(colCard).
		Border(1, colLine).Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).MinWidth(240).Children(func() {
			iconTile(c, "shield", 38, 11, colBgSoft, colTextSoft)
			ui.Column(c).Gap(3).Children(func() {
				ui.Text(c, title).FontSize(15).Bold()
				ui.Text(c, desc).FontSize(12).TextColor(colTextMuted)
			})
		})
		ui.Row(c).Grow(1).Gap(10).AlignItems(ui.Center).Children(func() {
			if ok {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(12).Radius(12).Grow(1).
					Background(ui.RGBA(255, 255, 255, 0.035)).Border(1, colLine).Children(func() {
					ui.Row(c).Size(24, 24).Center().Radius(12).Background(ui.RGBA(82, 211, 154, 0.12)).Children(func() {
						ui.Icon(c, icon("check")).FontSize(13).TextColor(colSuccess)
					})
					ui.Column(c).Gap(2).Children(func() {
						ui.Text(c, "已授权").FontSize(12)
						ui.Text(c, "系统设置中已允许").FontSize(11).TextColor(colTextMuted)
					})
				})
			} else {
				item := ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(12).Radius(12).Grow(1).
					Background(ui.RGBA(255, 255, 255, 0.035)).Border(1, colLine)
				item.Children(func() {
					ui.Row(c).Size(24, 24).Center().Radius(12).Background(ui.RGBA(255, 107, 104, 0.12)).Children(func() {
						ui.Icon(c, icon("alert")).FontSize(13).TextColor(colDanger)
					})
					ui.Column(c).Grow(1).Gap(2).Children(func() {
						ui.Text(c, "需要授权").FontSize(12)
						ui.Text(c, "点击打开 macOS 系统设置").FontSize(11).TextColor(colTextMuted)
					})
					ui.Icon(c, icon("arrow")).FontSize(15).TextColor(colTextMuted)
				})
				if item.Clicked() {
					a.onPermission(kind)
				}
			}
		})
	})
}

func (a *app) hotkeyRecorder(c *ui.Context, slot string) {
	recording := a.recording == slot
	label := formatHotkey(a.currentHotkey(slot))
	tc := colText
	if recording {
		label = "按下新的组合键…"
		tc = colAccentStr
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "快捷键").FontSize(11).Bold().TextColor(colTextSoft)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Grow(1).Height(40).Padding(0, 12).Radius(10).Background(colBg).
				Border(1, colLine).Children(func() {
				ui.Text(c, label).FontSize(13).TextColor(tc)
			})
			if recording {
				if secondaryBtn(c, "取消").Clicked() {
					a.recording = ""
				}
				if acc, ok := captureHotkey(c); ok {
					a.setHotkey(slot, acc)
					a.recording = ""
				}
			} else if secondaryBtn(c, "录制").Clicked() {
				a.recording = slot
			}
		})
	})
}

func (a *app) langSelect(c *ui.Context) {
	labels := make([]string, len(targetLangOptions))
	for i, o := range targetLangOptions {
		labels[i] = o.Label
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "默认目标语言").FontSize(11).Bold().TextColor(colTextSoft)
		ui.Select(c, &a.langSel, labels).WidthPercent(100).Height(40).Label("默认目标语言")
	})
	if a.settings.TargetLang != langCode(a.langSel) {
		a.settings.TargetLang = langCode(a.langSel)
	}
}

func (a *app) saveBar(c *ui.Context, label string) {
	ui.Row(c).Justify(ui.End).Children(func() {
		if primaryBtn(c, label).Clicked() {
			a.saveSettingsUI()
		}
	})
}

// iconTile 画一个带圆角底色的图标块。
func iconTile(c *ui.Context, name string, size, radius float32, bg, fg ui.Color) {
	ui.Row(c).Size(size, size).Center().Radius(radius).Background(bg).Children(func() {
		ui.Icon(c, icon(name)).FontSize(size * 0.46).TextColor(fg)
	})
}

func pill(c *ui.Context, text string, bg, fg ui.Color) {
	ui.Row(c).Padding(4, 8).Radius(999).Background(bg).Children(func() {
		ui.Text(c, text).FontSize(11).TextColor(fg)
	})
}

func primaryBtn(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Background(colText).TextColor(colPrimaryTxt).Bold().
		MinHeight(38).Radius(10).Padding(0, 16)
}

func secondaryBtn(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Background(ui.RGBA(255, 255, 255, 0.055)).
		Border(1, colLine).MinHeight(38).Radius(10).Padding(0, 16)
}

func ghostBtn(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Background(ui.Transparent).TextColor(colTextSoft).
		MinHeight(28).Radius(8).Padding(0, 8)
}

// ---------------- 动作 ----------------

func (a *app) visibleSections() []sectionItem {
	items := []sectionItem{{"overview", "概览", "home"}}
	if a.settings.TranslationEnabled {
		items = append(items, sectionItem{"translation", "翻译", "translate"})
	}
	if a.settings.ScreenshotEnabled {
		items = append(items, sectionItem{"screenshot", "截图", "screenshot"})
	}
	items = append(items, sectionItem{"settings", "设置", "settings"})
	return items
}

func (a *app) sectionVisible(id string) bool {
	for _, it := range a.visibleSections() {
		if it.id == id {
			return true
		}
	}
	return false
}

func (a *app) currentHotkey(slot string) string {
	if slot == "translate" {
		return a.settings.Hotkeys.Translate
	}
	return a.settings.Hotkeys.Screenshot
}

func (a *app) setHotkey(slot, acc string) {
	if slot == "translate" {
		a.settings.Hotkeys.Translate = acc
	} else {
		a.settings.Hotkeys.Screenshot = acc
	}
	if a.settings.Hotkeys.Translate == a.settings.Hotkeys.Screenshot {
		a.toast("提示：两个插件使用了相同快捷键，可能相互冲突")
	}
}

func (a *app) afterFeatureChange(kind string) {
	if !a.sectionVisible(a.section) {
		a.section = "overview"
	}
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	a.registerHotkeys()
	label, on := "划词翻译", a.settings.TranslationEnabled
	if kind == "screenshot" {
		label, on = "截图标注", a.settings.ScreenshotEnabled
	}
	if on {
		a.toast(label + " 已开启")
	} else {
		a.toast(label + " 已关闭")
	}
}

func (a *app) afterProviderChange() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
	}
}

func (a *app) saveSettingsUI() {
	a.settingsSaving = true
	err := a.persist()
	a.settingsSaving = false
	if err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	a.registerHotkeys()
	a.toast("设置已保存")
}

// checkUpdate 打开更新窗口。真正的检查、下载、签名校验与替换都由
// mygo 的更新插件完成（updater/native），这里只负责发起。
func (a *app) checkUpdate() {
	if !a.updatesOn {
		a.toast("开发构建不检查更新，请使用打包后的 App")
		return
	}
	updater.CheckForUpdates()
}

// updateHint 是更新卡片的一句话说明，跟着检查结果显示。
func (a *app) updateHint() string {
	if !a.updatesOn {
		return "开发构建不检查更新；打包版会从 GitHub Release 校验签名后升级。"
	}
	if a.available != "" {
		return "发现新版本 v" + a.available + "，点击“检查更新”查看说明并安装。"
	}
	if t := updater.LastCheck(); !t.IsZero() {
		return "已是最新版本 · 上次检查 " + t.Local().Format("01-02 15:04")
	}
	return "更新来自 GitHub Release，安装前会用内置公钥校验签名。"
}

// refreshUpdateState 同步插件发现的新版本，并让界面重绘。
func (a *app) refreshUpdateState() {
	if !a.updatesOn {
		return
	}
	a.available = updater.AvailableVersion()
	if a.win != nil {
		a.win.Update(func() {})
	}
}

func (a *app) openLog() {
	dir, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		a.toast("无法定位日志目录")
		return
	}
	if err := mygo.Shell.OpenPath(dir); err != nil {
		a.toast("打开日志目录失败：" + err.Error())
	}
}

func (a *app) registerHotkeys() {
	a.ensureHotkey("translate", a.settings.TranslationEnabled, a.settings.Hotkeys.Translate, a.startTranslation)
	a.ensureHotkey("screenshot", a.settings.ScreenshotEnabled, a.settings.Hotkeys.Screenshot, a.startCapture)
}

func (a *app) ensureHotkey(slot string, enabled bool, acc string, fn func()) {
	if old, ok := a.registered[slot]; ok {
		mygo.GlobalShortcut.Unregister(old)
		delete(a.registered, slot)
	}
	if !enabled || acc == "" {
		return
	}
	if err := mygo.GlobalShortcut.Register(acc, fn); err != nil {
		log.Printf("注册快捷键 %s 失败：%v", acc, err)
		return
	}
	a.registered[slot] = acc
}

func (a *app) notifyCaptureError(msg string) {
	a.toast(strings.TrimSpace(msg))
}
