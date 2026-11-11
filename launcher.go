package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	launcherWidth      = 640
	launcherHeight     = 540 // 结果区域的最大窗口高度
	launcherMaxResults = 8   // 540 高度下正好能完整显示 8 行，避免键盘选中被滚动截断

	// 原生 UI 没有内容自适应高度，这里按各区域的实际占位累加。
	launcherHeaderHeight = 67
	launcherFooterHeight = 37
	launcherRowHeight    = 52
	launcherListPadding  = 16
	launcherEmptyHeight  = 92
	launcherNoticeHeight = 48
)

// launcherSession 复用一个置顶浮窗，首次唤起时创建。所有可变字段只在
// 主线程读写；后台结果通过 win.Update 回来，并用 gen 丢弃过期数据。
type launcherSession struct {
	app *app
	win *mygo.Window

	query    string
	selected int
	results  []launcherItem
	notice   string
	input    ui.Handle
	inputSeq int

	apps        []launcherAppEntry
	appsLoaded  bool
	appsLoading bool
	appsErr     string

	files *launcherFileIndex
	// VSCode 项目模式的缓存。项目只扫描配置文件夹的下一级子目录。
	projects        []launcherProjectEntry
	projectsLoaded  bool
	projectsLoading bool
	projectErrs     []string
	projectRootsSig string
	projectGen      int

	icons   map[string]*launcherIcon
	iconSem chan struct{}

	// 这几个函数允许测试注入，默认走 mygo.Shell。
	loadAppIcon   func(path string, size int) ([]byte, error)
	launchApp     func(mygo.InstalledApp) error
	openPath      func(string) error
	launchProject func(string) error

	gen       int
	closed    bool
	shownAt   time.Time
	hiddenAt  time.Time
	hidByBlur bool
	// lastHeight 避免每次按键都对窗口做一次无变化的 SetSize。
	lastHeight int
}

func newLauncherSession(a *app) *launcherSession {
	s := &launcherSession{
		app:           a,
		loadAppIcon:   mygo.Shell.FileIcon,
		launchApp:     mygo.Shell.LaunchApp,
		openPath:      mygo.Shell.OpenPath,
		launchProject: a.openFolderInVSCode,
		icons:         make(map[string]*launcherIcon),
		iconSem:       make(chan struct{}, launcherIconConcurrency),
	}
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "快速启动",
		Width:             launcherWidth,
		Height:            launcherHeight,
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
	// 去掉 DWM 给圆角浮窗描的原生边框，避免和外层 UI 边框叠成两条线。
	hideNativeWindowBorder(s.win.NativeHandle())
	s.win.OnBlur(func() {
		if s.app.quitting || s.closed {
			return
		}
		// 刚 Show 时系统可能先给一次失焦，不能把它当成用户点击外部。
		if time.Since(s.shownAt) < 150*time.Millisecond {
			return
		}
		s.hide()
		s.hidByBlur = true
	})
	s.win.OnClose(func(e *mygo.CloseEvent) {
		if s.app.quitting || s.closed {
			return
		}
		e.PreventDefault()
		s.hide()
	})
	return s
}

func (s *launcherSession) show() {
	if s.win == nil || s.closed {
		return
	}
	s.query = ""
	s.selected = 0
	s.notice = ""
	s.inputSeq++
	s.hidByBlur = false
	// 每次唤起都重新扫描项目目录，用户刚新建的项目也能立刻看到。
	s.projectGen++
	s.projects = nil
	s.projectsLoaded = false
	s.projectsLoading = false
	s.projectErrs = nil
	s.projectRootsSig = ""
	s.rebuildResults()

	s.position()
	// 先记时间再 Show：个别平台上失焦事件可能在 Show 返回前同步到达，
	// 此时 shownAt 还是零值，会让“刚显示就忽略失焦”的判断失效。
	s.shownAt = time.Now()
	s.win.Show()
	// DWM 可能在显示后重新应用描边，显示时再设一次。
	hideNativeWindowBorder(s.win.NativeHandle())
	s.win.Focus()
	s.refresh()

	// 和翻译浮窗一样，等窗口与输入框建好后再补一次焦点，避免激活竞态。
	s.input.Focus()
	time.AfterFunc(60*time.Millisecond, func() {
		if s.closed || s.win == nil || !s.win.IsVisible() {
			return
		}
		s.input.Focus()
	})

	s.ensureApps()
	s.ensureFileIndex()
}

func (s *launcherSession) position() {
	pt := mygo.Screen.CursorScreenPoint()
	disp := mygo.Screen.DisplayNearestPoint(pt)
	area := disp.WorkArea
	w, h := launcherWidth, s.desiredHeight()
	x := area.X + (area.Width-w)/2
	y := area.Y + min(area.Height/6, 180)
	x = max(area.X, min(x, area.X+max(0, area.Width-w)))
	y = max(area.Y, min(y, area.Y+max(0, area.Height-h)))
	s.win.SetPosition(x, y)
}

func (s *launcherSession) hide() {
	if s.win == nil || s.closed {
		return
	}
	s.win.Hide()
	s.hiddenAt = time.Now()
}

func (s *launcherSession) toggle() {
	if s.win == nil || s.closed {
		return
	}
	if s.win.IsVisible() {
		s.hide()
		return
	}
	// 失焦隐藏和全局快捷键可能几乎同时发生：吞掉紧随其后的一次显示，
	// 否则用户按快捷键想关闭，窗口会被回调重新弹出来。
	if s.hidByBlur && time.Since(s.hiddenAt) < 250*time.Millisecond {
		s.hidByBlur = false
		return
	}
	s.show()
}

func (s *launcherSession) dispose() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	s.gen++
	s.files = nil
	s.projects = nil
	s.icons = nil
	if s.win != nil {
		s.win.Hide()
		s.win.Destroy()
		s.win = nil
	}
}

func (s *launcherSession) refresh() {
	if s.win == nil || s.closed {
		return
	}
	s.win.Update(func() {})
}

// rebuildResults 按当前查询和搜索源重新计算列表。它只做纯内存过滤，
// 可以在输入变化、后台加载完成、设置切换等事件路径里调用。
func (s *launcherSession) rebuildResults() {
	if s.app == nil {
		return
	}
	// 输入“命令 + 空格”时切到项目模式：只列配置文件夹的下一级项目。
	if query, ok := s.projectQuery(); ok {
		s.ensureProjects()
		s.app.ensureVSCodeProbe()
		s.results = filterLauncherProjects(query, s.projects, launcherMaxResults)
		s.selected = clampLauncherSelection(s.selected, len(s.results), 0)
		s.fitHeight()
		if s.win != nil && s.win.IsVisible() {
			s.refresh()
		}
		return
	}
	src := launcherSources{
		includeApps:  s.app.settings.LauncherSearchApps,
		includeFiles: s.app.settings.LauncherSearchFiles,
	}
	if src.includeApps {
		src.apps = s.apps
	}
	if src.includeFiles && s.files != nil {
		src.files = s.files.entries
	}
	s.results = launcherFilter(s.query, src, launcherMaxResults)
	s.selected = clampLauncherSelection(s.selected, len(s.results), 0)
	s.prefetchIcons(s.results)
	s.fitHeight()
	if s.win != nil && s.win.IsVisible() {
		s.refresh()
	}
}

// desiredHeight 让浮窗跟着结果数量伸缩：空查询只剩搜索框，一两条结果就
// 只占一两行，结果多时封顶在当前尺寸，超出的部分由 Scroll 承接。
func (s *launcherSession) desiredHeight() int {
	if strings.TrimSpace(s.query) == "" {
		return launcherHeaderHeight
	}
	h := launcherHeaderHeight + launcherFooterHeight
	if s.notice != "" {
		h += launcherNoticeHeight
	}
	if len(s.results) == 0 {
		h += launcherEmptyHeight
	} else {
		h += launcherListPadding + len(s.results)*launcherRowHeight
	}
	if h > launcherHeight {
		return launcherHeight
	}
	return h
}

func (s *launcherSession) fitHeight() {
	if s.win == nil || s.closed {
		return
	}
	h := s.desiredHeight()
	if h == s.lastHeight {
		return
	}
	s.lastHeight = h
	s.win.SetSize(launcherWidth, h)
}

// sourcesChanged 在主窗口切换搜索源后同步当前浮窗。
func (s *launcherSession) sourcesChanged() {
	if s == nil {
		return
	}
	s.rebuildResults()
	s.ensureApps()
	s.ensureFileIndex()
	s.refresh()
}

func (s *launcherSession) handleTextKey(key ui.Key, mods ui.Modifiers) bool {
	switch key {
	case ui.KeyUp:
		if mods != 0 {
			return false
		}
		s.selected = clampLauncherSelection(s.selected, len(s.results), -1)
		s.refresh()
		return true
	case ui.KeyDown:
		if mods != 0 {
			return false
		}
		s.selected = clampLauncherSelection(s.selected, len(s.results), 1)
		s.refresh()
		return true
	case ui.KeyEnter:
		if mods != 0 {
			return false
		}
		s.activate(s.selected)
		return true
	case ui.KeyEscape:
		s.hide()
		return true
	}
	return false
}

func (s *launcherSession) activate(i int) {
	if i < 0 || i >= len(s.results) {
		return
	}
	item := s.results[i]
	var err error
	switch item.kind {
	case launcherKindApp:
		if s.launchApp != nil {
			err = s.launchApp(item.app)
		}
	case launcherKindProject:
		if s.launchProject != nil {
			err = s.launchProject(item.path)
		}
	default:
		if s.openPath != nil {
			err = s.openPath(item.path)
		}
	}
	if err != nil {
		s.notice = "打开失败：" + err.Error()
		log.Printf("快速启动打开 %q 失败：%v", item.path, err)
		s.fitHeight()
		s.refresh()
		return
	}
	s.hide()
}

// ---------------- 浮窗界面 ----------------

func (s *launcherSession) view(c *ui.Context) {
	// 输入框通常用 OnTextKey 处理 Esc；焦点如果已经移到结果行，这里兜底。
	if c.Shortcut(0, ui.KeyEscape) && !s.input.Focused(c) {
		s.hide()
	}
	ui.Themed(c, sToolsTheme()).Fill().Background(ui.Transparent).Children(func() {
		ui.Column(c).Fill().Radius(16).Border(1, colLineStrong).
			Gradient(ui.Hex("#2b2c30"), ui.Hex("#202124"), 180).Children(func() {
			s.header(c)
			// 没有输入时只保留搜索框，不展示结果和底部状态。
			if strings.TrimSpace(s.query) != "" {
				s.body(c)
				s.footer(c)
			}
		})
	})
}

func (s *launcherSession) header(c *ui.Context) {
	header := ui.Row(c).Padding(14, 16).Gap(10).AlignItems(ui.Center)
	// 有结果区域时才画分隔线；空查询时浮窗只剩搜索框，不能多出一条底线。
	if strings.TrimSpace(s.query) != "" {
		header.BorderWidth(0, 0, 1, 0).BorderColor(colLine)
	}
	header.Children(func() {
		ui.Icon(c, icon("launcher")).FontSize(20).TextColor(colAccentStr)
		// 用无边框输入框，避免外层面板和搜索框各画一圈边框形成“双边框”。
		in := ui.TextInputBase(c.Key(fmt.Sprintf("launcher-input-%d", s.inputSeq)), &s.query).
			Grow(1).Height(38).FontSize(15).
			Placeholder("搜索应用和文件…").
			Bind(&s.input).
			AutoFocus().
			OnTextKey(func(k ui.TextKey) bool {
				// TextKey 只在回调期间有效，只取它携带的值，不能保存 k。
				return s.handleTextKey(k.Key, k.Mods)
			})
		if in.Changed() {
			s.selected = 0
			s.notice = ""
			s.rebuildResults()
		}
	})
}

func (s *launcherSession) body(c *ui.Context) {
	if s.notice != "" {
		ui.Row(c).Margin(10, 10, 0, 10).Padding(9, 10).Radius(9).
			Border(1, ui.RGBA(248, 113, 113, 0.16)).Background(ui.RGBA(248, 113, 113, 0.1)).
			AlignItems(ui.Center).Gap(7).Children(func() {
			ui.Icon(c, icon("alert")).FontSize(14).TextColor(colDanger)
			ui.Text(c, s.notice).Grow(1).MinWidth(0).FontSize(12).TextColor(colDanger)
		})
	}

	if len(s.results) == 0 {
		s.emptyState(c)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Padding(8).Gap(2).Children(func() {
			for i := range s.results {
				s.resultRow(c, i)
			}
		})
	})
}

func (s *launcherSession) emptyState(c *ui.Context) {
	title, desc := s.emptyHint()
	ui.Column(c).Grow(1).Center().Gap(4).Children(func() {
		ui.Icon(c, icon("launcher")).FontSize(26).TextColor(colTextMuted)
		ui.Text(c, title).FontSize(14).TextColor(colTextSoft)
		ui.Text(c, desc).FontSize(12).TextColor(colTextMuted).MaxLines(2)
	})
}

func (s *launcherSession) emptyHint() (string, string) {
	settings := s.app.settings
	if _, ok := s.projectQuery(); ok {
		roots := s.projectRoots()
		switch {
		case len(roots) == 0:
			return "还没有配置项目文件夹", "到主窗口的「启动」页添加项目文件夹。"
		case s.projectsLoading:
			return "正在扫描项目文件夹…", "只扫描每个文件夹的下一级目录。"
		case len(s.projectErrs) > 0 && len(s.projects) == 0:
			return "无法读取项目文件夹", strings.Join(s.projectErrs, "；")
		case settings.LauncherVSCodeEnabled && s.app.vscode.checking:
			return "正在检测 VSCode 命令…", "检测完成后即可打开项目。"
		case settings.LauncherVSCodeEnabled && s.app.vscode.err != "":
			return "VSCode 命令不可用", s.app.vscode.err
		default:
			return "没有找到匹配的项目", "换个关键字，或到「启动」页检查文件夹。"
		}
	}
	switch {
	case !settings.LauncherSearchApps && !settings.LauncherSearchFiles:
		return "未启用任何搜索源", "到主窗口的「启动」页勾选应用或文件。"
	case strings.TrimSpace(s.query) == "" && !settings.LauncherSearchApps:
		return "输入关键字搜索文件", "文件搜索覆盖桌面、下载、文档和主目录浅层。"
	case settings.LauncherSearchApps && s.appsLoading:
		return "正在加载应用…", "首次加载需要一点时间。"
	case settings.LauncherSearchFiles && (s.files == nil || s.files.building):
		return "正在建立文件索引…", "索引完成后会自动刷新结果。"
	case s.appsErr != "":
		return "加载应用失败", s.appsErr
	case settings.LauncherVSCodeEnabled && strings.EqualFold(strings.TrimSpace(s.query), s.app.vscodeCommand()):
		return "输入 " + s.app.vscodeCommand() + " + 空格 搜索项目", "空格会进入 VSCode 项目模式。"
	case strings.TrimSpace(s.query) == "" && settings.LauncherSearchFiles && s.appsLoaded && len(s.apps) == 0:
		return "输入关键字搜索文件", "没有找到已安装应用，文件搜索仍然可用。"
	default:
		return "没有找到匹配结果", "换个关键字试试。"
	}
}

func (s *launcherSession) resultRow(c *ui.Context, i int) {
	item := s.results[i]
	bg := ui.Transparent
	if i == s.selected {
		// 选中态只用背景高亮，不再画一圈边框：整个浮窗只保留外层面板边框。
		bg = colAccentSoft
	}
	row := ui.Row(c.Key("launcher-row-"+item.path)).Padding(8, 10).Radius(10).
		Gap(11).AlignItems(ui.Center).Background(bg)
	row.Children(func() {
		s.itemIcon(c, item)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, item.title).FontSize(13).Bold().MaxLines(1)
			if item.subtitle != "" {
				ui.Text(c, item.subtitle).FontSize(11).TextColor(colTextMuted).MaxLines(1)
			}
		})
		pill(c, item.kind.label(), colBgSoft, colTextSoft)
	})
	if row.Clicked() {
		s.selected = i
		s.activate(i)
	}
}

func (s *launcherSession) itemIcon(c *ui.Context, item launcherItem) {
	if item.kind == launcherKindProject {
		iconTile(c, "code", 28, 8, colAccentSoft, colAccentStr)
		return
	}
	if item.kind == launcherKindApp {
		if bmp := s.appIcon(item.path); bmp != nil {
			ui.Image(c, bmp).Size(28, 28)
			return
		}
		iconTile(c, "app", 28, 8, colAccentSoft, colAccentStr)
		return
	}
	name := "file"
	if item.kind == launcherKindDir {
		name = "folder"
	}
	iconTile(c, name, 28, 8, colBgSoft, colTextSoft)
}

func (s *launcherSession) footer(c *ui.Context) {
	ui.Row(c).MinHeight(36).Padding(8, 14).AlignItems(ui.Center).Gap(8).
		BorderWidth(1, 0, 0, 0).BorderColor(colLine).Children(func() {
		hint := "↑↓ 选择 · ↵ 打开 · Esc 关闭"
		if _, ok := s.projectQuery(); ok {
			hint = "↑↓ 选择 · ↵ 用 VSCode 打开 · Esc 关闭"
		}
		ui.Text(c, hint).FontSize(11).TextColor(colTextMuted).MaxLines(1)
		ui.Spacer(c)
		if status := s.statusLine(); status != "" {
			ui.Text(c, status).FontSize(11).TextColor(colTextMuted).MaxLines(1)
		}
	})
}

func (s *launcherSession) statusLine() string {
	if s.app == nil {
		return ""
	}
	if _, ok := s.projectQuery(); ok {
		return s.projectStatusLine()
	}
	var parts []string
	if s.app.settings.LauncherSearchApps {
		switch {
		case s.appsLoading:
			parts = append(parts, "应用加载中")
		case s.appsErr != "":
			parts = append(parts, "应用加载失败")
		case s.appsLoaded:
			parts = append(parts, fmt.Sprintf("%d 个应用", len(s.apps)))
		default:
			parts = append(parts, "应用未加载")
		}
	}
	if s.app.settings.LauncherSearchFiles {
		switch {
		case s.files == nil:
			parts = append(parts, "文件索引未建立")
		case s.files.building:
			parts = append(parts, "正在索引文件")
		case s.files.built:
			suffix := ""
			if s.files.stats.truncated {
				suffix = "（已达上限）"
			} else if s.files.stats.errors > 0 {
				suffix = "（部分目录无权限）"
			}
			parts = append(parts, fmt.Sprintf("%d 项文件%s", len(s.files.entries), suffix))
		}
	}
	return strings.Join(parts, " · ")
}

// ---------------- 主窗口启动页 ----------------

func (a *app) startLauncher() {
	if !a.settings.LauncherEnabled {
		a.section = "overview"
		a.showMain()
		a.toast("快速启动未开启，请先在概览中打开")
		return
	}
	if a.launcher == nil {
		a.launcher = newLauncherSession(a)
	}
	a.launcher.toggle()
}

func (a *app) disposeLauncher() {
	if a.launcher == nil {
		return
	}
	a.launcher.dispose()
	a.launcher = nil
}

func (a *app) launcherSection(c *ui.Context) {
	a.intro(c, "LAUNCHER", "快速启动",
		"按快捷键唤起搜索浮窗，打开已安装应用与常用目录中的文件。", func() {
			pill(c, a.launcherHotkeyStatus(), colAccentSoft, colAccentStr)
		})

	hotkeyBody := func() {
		a.hotkeyRecorder(c, "launcher")
	}
	sourceBody := func() {
		a.launcherSourceRow(c, "应用", "从已安装应用中选择，并显示真实图标。",
			&a.settings.LauncherSearchApps, "搜索应用")
		a.launcherSourceRow(c, "文件", "桌面、下载、文档和主目录浅层。",
			&a.settings.LauncherSearchFiles, "搜索文件")
	}
	if a.twoColumn(c) {
		ui.Row(c).Gap(14).AlignItems(ui.Stretch).Children(func() {
			a.settingsSection2(c, "keyboard", "唤起快捷键", "留空时不注册，录制后立即生效。", hotkeyBody).Grow(1)
			a.settingsSection2(c, "launcher", "搜索源", "只在这里切换，浮窗内不提供开关。", sourceBody).Grow(1)
		})
	} else {
		ui.Column(c).Gap(14).Children(func() {
			a.settingsSection2(c, "keyboard", "唤起快捷键", "留空时不注册，录制后立即生效。", hotkeyBody)
			a.settingsSection2(c, "launcher", "搜索源", "只在这里切换，浮窗内不提供开关。", sourceBody)
		})
	}

	a.launcherVSCodeCard(c)

	a.settingsSection2(c, "cursor", "使用方式", "唤起后直接用键盘操作。", func() {
		for _, line := range []string{
			"输入关键字筛选应用和文件，结果会随输入实时更新。",
			"↑ / ↓ 切换选中项，回车打开；Esc 或点击窗口外部关闭。",
			"首次唤起会在后台加载应用并建立文件索引。",
		} {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon("arrow")).FontSize(11).TextColor(colAccentStr)
				ui.Text(c, line).FontSize(12).TextColor(colTextSoft)
			})
		}
	})

	a.launcherIndexCard(c)
}

func (a *app) launcherSourceRow(c *ui.Context, title, desc string, on *bool, label string) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(13)
			ui.Text(c, desc).FontSize(11).TextColor(colTextMuted)
		})
		if ui.Switch(c, on).Label(label).Changed() {
			a.afterLauncherSourceChange()
		}
	})
}

// launcherVSCodeCard 是启动页里的自定义命令配置。默认关闭，开启后才
// 展示命令、检测状态和项目文件夹。
func (a *app) launcherVSCodeCard(c *ui.Context) {
	enabled := a.settings.LauncherVSCodeEnabled
	bg, fg := colBgSoft, colTextSoft
	if enabled {
		bg, fg = colAccentSoft, colAccentStr
	}
	ui.Column(c).Padding(20).Radius(17).Background(colCard).Border(1, colLine).
		Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		ui.Row(c).Gap(13).AlignItems(ui.Center).Children(func() {
			iconTile(c, "code", 38, 11, bg, fg)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "VSCode 快速打开").FontSize(16).Bold()
				ui.Text(c, "输入命令 + 空格，从配置的文件夹中打开项目。").FontSize(12).TextColor(colTextSoft)
			})
			if ui.Switch(c, &a.settings.LauncherVSCodeEnabled).Label("VSCode 快速打开").Changed() {
				a.afterVSCodeToggle()
			}
		})
		if !a.settings.LauncherVSCodeEnabled {
			ui.Text(c, "开启后可以配置命令和项目文件夹，首次开启会自动检测 code 命令。").
				FontSize(11).TextColor(colTextMuted).Margin(14, 0, 0, 0)
			return
		}
		ui.Column(c).Margin(18, 0, 0, 0).Gap(14).Children(func() {
			a.vscodeCommandRow(c)
			a.vscodeFolderSection(c)
		})
	})
}

func (a *app) vscodeCommandRow(c *ui.Context) {
	ui.Column(c).Gap(7).Children(func() {
		ui.Text(c, "命令").FontSize(11).Bold().TextColor(colTextSoft)
		in := ui.TextInput(c.Key("vscode-command"), &a.settings.LauncherVSCodeCommand).
			Placeholder("code").Label("VSCode 命令").Height(40).WidthPercent(100)
		if in.Changed() {
			if err := a.persist(); err != nil {
				a.toast("保存失败：" + err.Error())
			} else {
				a.detectVSCodeCommand()
				if a.launcher != nil {
					a.launcher.vscodeChanged()
				}
			}
		}
	})

	ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
		name, color := "check", colSuccess
		switch {
		case a.vscode.checking:
			name, color = "refresh", colTextMuted
		case a.vscode.err != "":
			name, color = "alert", colDanger
		case !a.vscodeIsReady():
			name, color = "alert", colTextMuted
		}
		ui.Icon(c, icon(name)).FontSize(14).TextColor(color)
		ui.Text(c, a.vscodeStatusText()).Grow(1).MinWidth(0).FontSize(12).
			TextColor(color).MaxLines(2)
		if secondaryBtn(c, "重新检测").Clicked() {
			a.detectVSCodeCommand()
		}
	})
	ui.Text(c, "示例：输入 "+a.vscodeCommand()+" + 空格，再输入项目名；回车用 VSCode 打开。").
		FontSize(11).TextColor(colTextMuted)
	ui.Text(c, "自动配置的快捷命令只供启动器使用，不会修改系统 PATH。").
		FontSize(11).TextColor(colTextMuted)
}

func (a *app) vscodeFolderSection(c *ui.Context) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, "项目文件夹").FontSize(11).Bold().TextColor(colTextSoft)
			ui.Text(c, "只扫描每个文件夹的下一级目录，可添加多个。").FontSize(11).TextColor(colTextMuted)
		})
		if secondaryBtn(c, "添加文件夹").Clicked() {
			a.pickVSCodeFolders()
		}
	})

	folders := a.settings.LauncherVSCodeFolders
	if len(folders) == 0 {
		ui.Row(c).Padding(12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.035)).
			Border(1, colLine).Children(func() {
			ui.Text(c, "还没有项目文件夹。点击“添加文件夹”选择一个或多个目录。").
				FontSize(12).TextColor(colTextMuted)
		})
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		for i, folder := range folders {
			i, folder := i, folder
			row := ui.Row(c.Key("vscode-folder-"+folder)).Gap(10).AlignItems(ui.Center).
				Padding(10, 12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.035)).
				Border(1, colLine)
			row.Children(func() {
				ui.Icon(c, icon("folder")).FontSize(15).TextColor(colTextSoft)
				ui.Text(c, folder).Grow(1).MinWidth(0).FontSize(12).
					TextColor(colTextSoft).MaxLines(1)
				if ghostBtn(c, "移除").Label("移除 " + folder).MinHeight(26).Clicked() {
					a.removeVSCodeFolder(i)
				}
			})
		}
	})
}

func (a *app) afterVSCodeToggle() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.settings.LauncherVSCodeEnabled {
		a.detectVSCodeCommand()
		a.toast("VSCode 快速打开已开启")
	} else {
		a.vscode = vscodeState{}
		a.refreshMain()
		a.toast("VSCode 快速打开已关闭")
	}
	if a.launcher != nil {
		a.launcher.vscodeSettingsChanged()
	}
}

func (a *app) pickVSCodeFolders() {
	if a.win == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            a.win,
			Title:             "选择项目文件夹",
			ButtonLabel:       "添加",
			Directory:         true,
			Multiple:          true,
			CreateDirectories: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.win.Update(func() {
			if a.quitting {
				return
			}
			a.addVSCodeFolders(paths)
		})
	}()
}

func (a *app) addVSCodeFolders(paths []string) {
	seen := make(map[string]struct{}, len(a.settings.LauncherVSCodeFolders))
	for _, f := range normalizeProjectFolders(a.settings.LauncherVSCodeFolders) {
		seen[strings.ToLower(f)] = struct{}{}
	}
	changed := false
	for _, p := range paths {
		normalized := normalizeProjectFolders([]string{p})
		if len(normalized) == 0 {
			continue
		}
		p = normalized[0]
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		a.settings.LauncherVSCodeFolders = append(a.settings.LauncherVSCodeFolders, p)
		changed = true
	}
	if !changed {
		return
	}
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.launcher != nil {
		a.launcher.vscodeSettingsChanged()
	}
	a.toast("项目文件夹已更新")
}

func (a *app) removeVSCodeFolder(i int) {
	folders := a.settings.LauncherVSCodeFolders
	if i < 0 || i >= len(folders) {
		return
	}
	next := make([]string, 0, len(folders)-1)
	next = append(next, folders[:i]...)
	next = append(next, folders[i+1:]...)
	a.settings.LauncherVSCodeFolders = next
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.launcher != nil {
		a.launcher.vscodeSettingsChanged()
	}
}

func (a *app) launcherIndexCard(c *ui.Context) {
	ui.Column(c).Padding(20).Radius(17).Background(colCard).Border(1, colLine).
		Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		ui.Row(c).Gap(13).AlignItems(ui.Center).Children(func() {
			iconTile(c, "provider", 38, 11, colBgSoft, colTextSoft)
			ui.Column(c).Grow(1).Gap(3).Children(func() {
				ui.Text(c, "索引状态").FontSize(16).Bold()
				ui.Text(c, a.launcherAppStatus()).FontSize(12).TextColor(colTextSoft)
				ui.Text(c, a.launcherFileStatus()).FontSize(12).TextColor(colTextMuted)
			})
			if secondaryBtn(c, "重建索引").Clicked() {
				if a.launcher == nil {
					a.toast("首次唤起时会自动建立索引")
					return
				}
				a.launcher.rebuildCaches()
				a.toast("正在重建索引…")
			}
		})
	})
}

func (a *app) launcherHotkeyStatus() string {
	if a.settings.Hotkeys.Launcher == "" {
		return "快捷键未设置"
	}
	return formatHotkey(a.settings.Hotkeys.Launcher)
}

func (a *app) launcherAppStatus() string {
	s := a.launcher
	switch {
	case s == nil:
		return "应用：将在首次唤起时加载"
	case s.appsLoading:
		return "应用：正在加载…"
	case s.appsErr != "":
		return "应用：加载失败 · " + s.appsErr
	case s.appsLoaded:
		return fmt.Sprintf("应用：已加载 %d 个", len(s.apps))
	default:
		return "应用：尚未加载"
	}
}

func (a *app) launcherFileStatus() string {
	if !a.settings.LauncherSearchFiles {
		return "文件：已关闭搜索"
	}
	s := a.launcher
	switch {
	case s == nil:
		return "文件：首次唤起时后台建立索引"
	case s.files == nil:
		return "文件：尚未建立索引"
	case s.files.building:
		return "文件：正在建立索引…"
	case s.files.built:
		suffix := ""
		if s.files.stats.truncated {
			suffix = "（已达上限）"
		} else if s.files.stats.errors > 0 {
			suffix = "（部分目录无权限，可在系统设置中允许访问桌面/文档/下载）"
		}
		return fmt.Sprintf("文件：已索引 %d 项%s", len(s.files.entries), suffix)
	default:
		return "文件：尚未建立索引"
	}
}

func (a *app) afterLauncherSourceChange() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.launcher != nil {
		a.launcher.sourcesChanged()
	}
	a.toast("搜索源已更新")
}
