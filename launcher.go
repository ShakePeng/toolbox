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
	launcherWidth        = 640
	launcherHeight       = 540 // 结果区域的最大窗口高度
	launcherCornerRadius = 16
	launcherMaxResults   = 8 // 540 高度下正好能完整显示 8 行，避免键盘选中被滚动截断

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
	// projectSource 是当前匹配到的项目源（vscode / fork），供打开与状态行使用。
	projectSource string

	icons   map[string]*launcherIcon
	iconSem chan struct{}

	// 这几个函数允许测试注入，默认走 mygo.Shell。
	loadAppIcon       func(path string, size int) ([]byte, error)
	launchApp         func(mygo.InstalledApp) error
	openPath          func(string) error
	launchProject     func(string) error
	launchForkProject func(string) error

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
		app:               a,
		loadAppIcon:       mygo.Shell.FileIcon,
		launchApp:         mygo.Shell.LaunchApp,
		openPath:          mygo.Shell.OpenPath,
		launchProject:     a.openFolderInVSCode,
		launchForkProject: a.openFolderInFork,
		icons:             make(map[string]*launcherIcon),
		iconSem:           make(chan struct{}, launcherIconConcurrency),
	}
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "快速启动",
		Width:             launcherWidth,
		Height:            launcherHeight,
		Hidden:            true,
		Frameless:         true,
		Transparent:       true,
		CornerRadius:      launcherCornerRadius,
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
	if src, query, ok := s.projectQuery(); ok {
		s.ensureProjects(src)
		if src.id == "fork" {
			s.app.ensureForkProbe()
		} else {
			s.app.ensureVSCodeProbe()
		}
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
		launch := s.launchProject
		if s.projectSource == "fork" {
			launch = s.launchForkProject
		}
		if launch != nil {
			err = launch(item.path)
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
		// 原生窗口负责圆角和阴影，这里只保留内容，不再重复画一圈圆角描边。
		ui.Column(c).Fill().Radius(launcherCornerRadius).
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
	if src, _, ok := s.projectQuery(); ok {
		roots := normalizeProjectFolders(src.folders)
		switch {
		case len(roots) == 0:
			return "还没有配置项目文件夹", "到主窗口的「启动」页添加 " + src.label + " 项目文件夹。"
		case s.projectsLoading:
			if src.gitOnly {
				return "正在扫描 Git 仓库…", "只列出项目文件夹下一级含 .git 的目录。"
			}
			return "正在扫描项目文件夹…", "只扫描每个文件夹的下一级目录。"
		case len(s.projectErrs) > 0 && len(s.projects) == 0:
			return "无法读取项目文件夹", strings.Join(s.projectErrs, "；")
		case src.id == "fork" && s.app.fork.checking:
			return "正在检测 Fork 命令…", "检测完成后即可打开项目。"
		case src.id == "fork" && s.app.fork.err != "":
			return "Fork 不可用", s.app.fork.err
		case src.id != "fork" && settings.LauncherVSCodeEnabled && s.app.vscode.checking:
			return "正在检测 VS Code 命令…", "检测完成后即可打开项目。"
		case src.id != "fork" && settings.LauncherVSCodeEnabled && s.app.vscode.err != "":
			return "VS Code 命令不可用", s.app.vscode.err
		case src.gitOnly:
			return "没有找到 Git 仓库", "换个关键字，或到「启动」页检查文件夹。"
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
		return "输入 " + s.app.vscodeCommand() + " + 空格 搜索项目", "空格会进入 VS Code 项目模式。"
	case settings.LauncherForkEnabled && strings.EqualFold(strings.TrimSpace(s.query), s.app.forkCommand()):
		return "输入 " + s.app.forkCommand() + " + 空格 搜索仓库", "空格会进入 Fork 项目模式，只列出 Git 仓库。"
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
		if toolIcon := launcherToolIcons[s.projectSource]; toolIcon != nil {
			ui.Image(c, toolIcon).Size(28, 28)
			return
		}
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
		if src, _, ok := s.projectQuery(); ok {
			hint = "↑↓ 选择 · ↵ 用 " + src.label + " 打开 · Esc 关闭"
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
	if _, _, ok := s.projectQuery(); ok {
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
	a.settingsSection2(c, "keyboard", "唤起快捷键", "留空时不注册，录制后立即生效。", hotkeyBody)

	a.settingsSection2(c, "launcher", "启动项", "点击卡片展开或收起配置，开关可独立切换。", func() {
		ui.Column(c).Gap(10).Children(func() {
			a.launcherSourceItem(c, "apps", "应用搜索", "搜索已安装应用", &a.settings.LauncherSearchApps, func() {
				ui.Column(c).Gap(7).Children(func() {
					ui.Text(c, "从系统已安装应用中检索，结果显示真实图标。").
						FontSize(12).TextColor(colTextSoft)
					ui.Text(c, a.launcherAppStatus()).FontSize(11).TextColor(colTextMuted).MaxLines(2)
				})
			})
			a.launcherSourceItem(c, "files", "文件搜索", "搜索桌面、下载、文档和主目录浅层", &a.settings.LauncherSearchFiles, func() {
				ui.Column(c).Gap(7).Children(func() {
					ui.Text(c, "文件索引在首次唤起时建立，覆盖常用目录，不做全盘搜索。").
						FontSize(12).TextColor(colTextSoft)
					ui.Text(c, a.launcherFileStatus()).FontSize(11).TextColor(colTextMuted).MaxLines(2)
					if secondaryBtn(c, "重建文件索引").Clicked() {
						if a.launcher == nil {
							a.toast("首次唤起时会自动建立索引")
							return
						}
						a.launcher.rebuildCaches()
						a.toast("正在重建索引…")
					}
				})
			})
			a.launcherSourceItem(c, "vscode", "VS Code 快速打开", "输入触发词 + 空格，快速打开项目", &a.settings.LauncherVSCodeEnabled, func() {
				if a.settings.LauncherVSCodeEnabled {
					ui.Column(c).Gap(16).Children(func() {
						a.vscodeCommandRow(c)
						a.vscodeFolderSection(c)
					})
					return
				}
				ui.Text(c, "开启后配置命令和项目文件夹，首次开启会自动检测 VS Code。").
					FontSize(12).TextColor(colTextMuted)
			})
			a.launcherSourceItem(c, "fork", "Fork 快速打开", "输入触发词 + 空格，用 Fork 打开 Git 仓库", &a.settings.LauncherForkEnabled, func() {
				if a.settings.LauncherForkEnabled {
					ui.Column(c).Gap(16).Children(func() {
						a.forkCommandRow(c)
						a.forkFolderSection(c)
					})
					return
				}
				ui.Text(c, "开启后配置命令和项目文件夹；Fork 只能打开 Git 仓库，列表只列含 .git 的目录。").
					FontSize(12).TextColor(colTextMuted)
			})
		})
	})
}

// 启动项卡片使用的品牌图标，没有时退回通用线性图标。
var launcherToolIcons = map[string]ui.ImageSource{
	"vscode": vscodeIcon,
	"fork":   forkIcon,
}

// launcherSourceItem 每个启动项一张卡片：标题行点击展开/收起，配置抽屉收在卡片内。
func (a *app) launcherSourceItem(c *ui.Context, id, title, desc string, on *bool, body func()) {
	// 没有配置内容的启动项不参与展开，避免出现一块空抽屉和多余的分隔线。
	expandable := body != nil
	expanded := expandable && a.launcherSourceExpanded == id
	bg := colBg
	border := colLine
	if expanded {
		bg = ui.Hex("#26272b")
		border = colLineStrong
	}
	ui.Column(c.Key("launcher-source-"+id)).Radius(12).Background(bg).
		Border(1, border).Children(func() {
		header := ui.Row(c).Padding(14, 16).Gap(12).AlignItems(ui.Center)
		if expandable {
			header.Label("展开或收起 " + title)
		}
		header.Children(func() {
			if toolIcon := launcherToolIcons[id]; toolIcon != nil {
				ui.Image(c, toolIcon).Size(30, 30)
			} else {
				iconName := "app"
				if id == "files" {
					iconName = "file"
				}
				iconTile(c, iconName, 30, 9, colBgSoft, colTextSoft)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, title).FontSize(13).Bold()
				ui.Text(c, desc).FontSize(11).TextColor(colTextMuted).MaxLines(2)
			})
			if ui.Switch(c, on).Label(title + "开关").Changed() {
				switch id {
				case "vscode":
					a.afterVSCodeToggle()
				case "fork":
					a.afterForkToggle()
				default:
					a.afterLauncherSourceChange()
				}
			}
			if expandable {
				ui.Icon(c, icon("chevron-down")).FontSize(16).TextColor(colTextMuted).
					Rotate(map[bool]float32{true: 180}[expanded])
			}
		})
		if expandable && header.Clicked() {
			if expanded {
				a.launcherSourceExpanded = ""
			} else {
				a.launcherSourceExpanded = id
			}
		}
		// 开关事件不在卡片点击里处理，避免操作开关时同时触发展开/收起。
		if !expanded {
			return
		}
		ui.Column(c).Margin(0, 16, 0, 0).Padding(14, 16).BorderWidth(1, 0, 0, 0).
			BorderColor(colLine).Children(body)
	})
}

// projectTool 汇总一个项目工具（VS Code / Fork）在设置页需要的状态与回调。
// 两者的抽屉内容只有命令、检测状态和文件夹列表，抽成一份避免重复。
type projectTool struct {
	id          string
	label       string
	placeholder string
	command     *string
	folders     *[]string
	status      string
	checking    bool
	failed      bool
	ready       bool
	onCommand   func()
	onRetry     func()
	onPick      func()
	onRemove    func(int)
	emptyHint   string
}

func (a *app) vscodeTool() projectTool {
	return projectTool{
		id: "vscode", label: "VS Code", placeholder: "code",
		command:   &a.settings.LauncherVSCodeCommand,
		folders:   &a.settings.LauncherVSCodeFolders,
		status:    a.vscodeStatusText(),
		checking:  a.vscode.checking,
		failed:    a.vscode.err != "",
		ready:     a.vscodeIsReady(),
		onCommand: a.afterToolCommandChange,
		onRetry:   a.detectVSCodeCommand,
		onPick:    a.pickVSCodeFolders,
		onRemove:  func(i int) { a.removeProjectFolder(&a.settings.LauncherVSCodeFolders, i, a.afterVSCodeFoldersChange) },
		emptyHint: "还没有项目文件夹。点击“添加文件夹”选择一个或多个目录。",
	}
}

func (a *app) forkTool() projectTool {
	return projectTool{
		id: "fork", label: "Fork", placeholder: "fork",
		command:   &a.settings.LauncherForkCommand,
		folders:   &a.settings.LauncherForkFolders,
		status:    a.forkStatusText(),
		checking:  a.fork.checking,
		failed:    a.fork.err != "",
		ready:     a.forkIsReady(),
		onCommand: a.afterToolCommandChange,
		onRetry:   a.detectForkCommand,
		onPick:    a.pickForkFolders,
		onRemove:  func(i int) { a.removeProjectFolder(&a.settings.LauncherForkFolders, i, a.afterForkFoldersChange) },
		emptyHint: "还没有项目文件夹。点击“添加文件夹”选择存放 Git 仓库的目录。",
	}
}

func (a *app) vscodeCommandRow(c *ui.Context) { a.projectCommandRow(c, a.vscodeTool()) }

func (a *app) vscodeFolderSection(c *ui.Context) { a.projectFolderSection(c, a.vscodeTool()) }

func (a *app) forkCommandRow(c *ui.Context) { a.projectCommandRow(c, a.forkTool()) }

func (a *app) forkFolderSection(c *ui.Context) { a.projectFolderSection(c, a.forkTool()) }

func (a *app) projectCommandRow(c *ui.Context, t projectTool) {
	ui.Column(c).Gap(7).Children(func() {
		ui.Text(c, "触发词").FontSize(11).Bold().TextColor(colTextSoft)
		in := ui.TextInput(c.Key(t.id+"-command"), t.command).
			Placeholder(t.placeholder).Label(t.label + " 触发词").Height(40).WidthPercent(100)
		if in.Changed() {
			t.onCommand()
		}
	})

	ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
		name, color := "check", colSuccess
		switch {
		case t.checking:
			name, color = "refresh", colTextMuted
		case t.failed:
			name, color = "alert", colDanger
		case !t.ready:
			name, color = "alert", colTextMuted
		}
		ui.Icon(c, icon(name)).FontSize(14).TextColor(color)
		ui.Text(c, t.status).Grow(1).MinWidth(0).FontSize(12).
			TextColor(color).MaxLines(2)
		if secondaryBtn(c, "重新检测").Label(t.label + " 重新检测").Clicked() {
			t.onRetry()
		}
	})
	ui.Text(c, "示例：输入 "+toolCommand(t)+" + 空格，再输入项目名；回车用 "+t.label+" 打开。").
		FontSize(11).TextColor(colTextMuted)
}

// toolCommand 读取配置里的命令，留空时用工具自己的默认值。
func toolCommand(t projectTool) string {
	if t.command == nil {
		return t.placeholder
	}
	if cmd := strings.TrimSpace(*t.command); cmd != "" {
		return cmd
	}
	return t.placeholder
}

func (a *app) projectFolderSection(c *ui.Context, t projectTool) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, "项目文件夹").FontSize(11).Bold().TextColor(colTextSoft)
			ui.Text(c, "只扫描每个文件夹的下一级目录，可添加多个。").
				FontSize(11).TextColor(colTextMuted)
		})
		if secondaryBtn(c, "添加文件夹").Label(t.label + " 添加文件夹").Clicked() {
			t.onPick()
		}
	})

	folders := *t.folders
	if len(folders) == 0 {
		ui.Row(c).Padding(12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.035)).
			Border(1, colLine).Children(func() {
			ui.Text(c, t.emptyHint).FontSize(12).TextColor(colTextMuted)
		})
		return
	}
	ui.Column(c).Gap(6).Children(func() {
		for i, folder := range folders {
			i, folder := i, folder
			row := ui.Row(c.Key(t.id+"-folder-"+folder)).Gap(10).AlignItems(ui.Center).
				Padding(10, 12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.035)).
				Border(1, colLine)
			row.Children(func() {
				ui.Icon(c, icon("folder")).FontSize(15).TextColor(colTextSoft)
				ui.Text(c, folder).Grow(1).MinWidth(0).FontSize(12).
					TextColor(colTextSoft).MaxLines(1)
				if ghostBtn(c, "移除").Label("移除 " + folder).MinHeight(26).Clicked() {
					t.onRemove(i)
				}
			})
		}
	})
}

// afterToolCommandChange 保存触发词改动。触发词只影响「命令 + 空格」的
// 输入匹配，不重新探测，已检测到的命令保持不变。
func (a *app) afterToolCommandChange() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.launcher != nil {
		a.launcher.vscodeChanged()
	}
}

func (a *app) pickVSCodeFolders() {
	a.pickProjectFolders(&a.settings.LauncherVSCodeFolders, a.afterVSCodeFoldersChange)
}

func (a *app) pickForkFolders() {
	a.pickProjectFolders(&a.settings.LauncherForkFolders, a.afterForkFoldersChange)
}

// pickProjectFolders 弹出原生目录多选，选完交给 addProjectFolders。
func (a *app) pickProjectFolders(target *[]string, onChanged func()) {
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
			a.addProjectFolders(target, paths, onChanged)
		})
	}()
}

func (a *app) addProjectFolders(target *[]string, paths []string, onChanged func()) {
	seen := make(map[string]struct{}, len(*target))
	for _, f := range normalizeProjectFolders(*target) {
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
		*target = append(*target, p)
		changed = true
	}
	if !changed {
		return
	}
	onChanged()
}

func (a *app) removeProjectFolder(target *[]string, i int, onChanged func()) {
	folders := *target
	if i < 0 || i >= len(folders) {
		return
	}
	next := make([]string, 0, len(folders)-1)
	next = append(next, folders[:i]...)
	next = append(next, folders[i+1:]...)
	*target = next
	onChanged()
}

// afterProjectFoldersChange 保存文件夹改动并让浮窗重扫项目。
func (a *app) afterProjectFoldersChange() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.launcher != nil {
		a.launcher.projectSettingsChanged()
	}
	a.toast("项目文件夹已更新")
}

func (a *app) afterVSCodeFoldersChange() { a.afterProjectFoldersChange() }

func (a *app) afterForkFoldersChange() { a.afterProjectFoldersChange() }

func (a *app) afterVSCodeToggle() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.settings.LauncherVSCodeEnabled {
		a.detectVSCodeCommand()
		a.toast("VS Code 快速打开已开启")
	} else {
		a.vscode = vscodeState{}
		a.refreshMain()
		a.toast("VS Code 快速打开已关闭")
	}
	if a.launcher != nil {
		a.launcher.projectSettingsChanged()
	}
}

func (a *app) afterForkToggle() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.settings.LauncherForkEnabled {
		a.detectForkCommand()
		a.toast("Fork 快速打开已开启")
	} else {
		a.fork = forkState{}
		a.refreshMain()
		a.toast("Fork 快速打开已关闭")
	}
	if a.launcher != nil {
		a.launcher.projectSettingsChanged()
	}
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
