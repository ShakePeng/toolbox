package main

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

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
	launcherRowHeight    = 48
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
	launchTraeProject func(string) error
	openSearch        func(string) error

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
		launchTraeProject: a.openFolderInTrae,
		openSearch:        openWebSearchURL,
		icons:             make(map[string]*launcherIcon),
		iconSem:           make(chan struct{}, launcherIconConcurrency),
	}
	s.win = mygo.NewWindow(mygo.WindowOptions{
		Title:             "启动器",
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
	// 输入“触发词 + 空格”时进入网页搜索模式。
	if eng, query, ok := s.webSearchQuery(); ok {
		s.showWebSearch(eng, query)
		return
	}
	// 输入“命令 + 空格”时切到项目模式：命令部分允许只输入触发词的前缀。
	if src, query, ok := s.projectQuery(); ok {
		s.showProjects(src, query)
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
	// 模糊命中的内置命令和项目触发词排在搜索结果最前面。
	if actions := s.launcherActions(); len(actions) > 0 {
		s.results = append(actions, s.results...)
		if len(s.results) > launcherMaxResults {
			s.results = s.results[:launcherMaxResults]
		}
	}
	s.selected = clampLauncherSelection(s.selected, len(s.results), 0)
	s.prefetchIcons(s.results)
	s.fitHeight()
	if s.win != nil && s.win.IsVisible() {
		s.refresh()
	}
}

// showWebSearch 渲染网页搜索入口。只输入触发词时提示补关键词，输入关键词
// 后行程就是「用某引擎搜索 xxx」，回车打开浏览器。
func (s *launcherSession) showWebSearch(eng LauncherSearchEngine, query string) {
	name := webSearchName(eng)
	item := launcherItem{
		kind:     launcherKindSearch,
		project:  eng.ID,
		title:    "用" + name + "搜索网页",
		subtitle: "输入关键词后回车，在浏览器中打开",
	}
	if query != "" {
		item.title = "用" + name + "搜索 “" + query + "”"
		item.path = buildWebSearchURL(eng.URL, query)
		// 副标题显示未编码的地址，编码后的中文没法读。
		item.subtitle = strings.ReplaceAll(eng.URL, "%s", query)
	}
	s.results = []launcherItem{item}
	s.selected = 0
	s.fitHeight()
	if s.win != nil && s.win.IsVisible() {
		s.refresh()
	}
}

// completeWebSearch 把输入补全成「触发词 + 空格」，并重建输入框让光标
// 落在末尾，用户直接接着输入关键词即可。
func (s *launcherSession) completeWebSearch(engineID string) {
	prefix := s.app.webSearchPrefix(engineID)
	if prefix == "" {
		return
	}
	s.query = prefix + " "
	s.inputSeq++
	s.selected = 0
	s.notice = ""
	s.rebuildResults()
}

// showProjects 渲染某个项目源的项目列表。
func (s *launcherSession) showProjects(src launcherProjectSource, query string) {
	s.projectSource = src.id
	s.ensureProjects(src)
	switch src.id {
	case "fork":
		s.app.ensureForkProbe()
	case "trae":
		s.app.ensureTraeProbe()
	default:
		s.app.ensureVSCodeProbe()
	}
	s.results = filterLauncherProjects(query, s.projects, launcherMaxResults)
	s.selected = clampLauncherSelection(s.selected, len(s.results), 0)
	s.fitHeight()
	if s.win != nil && s.win.IsVisible() {
		s.refresh()
	}
}

// launcherActions 返回输入模糊命中内置命令或项目触发词时的操作行。
func (s *launcherSession) launcherActions() []launcherItem {
	q := strings.TrimSpace(s.query)
	if utf8.RuneCountInString(q) < 2 {
		return nil
	}
	var items []launcherItem
	if cmd, arg, ok := matchBuiltinCommand(q); ok && cmd.enabled(s.app) {
		items = append(items, s.builtinItem(cmd, arg))
	}
	// 已经输入空格时由 projectQuery 负责项目模式，这里只提示触发词本身。
	if strings.ContainsAny(s.query, " \t") {
		return items
	}
	for _, src := range s.app.projectSources() {
		prefix := strings.TrimSpace(src.prefix)
		if prefix == "" || !strings.HasPrefix(strings.ToLower(prefix), strings.ToLower(q)) {
			continue
		}
		items = append(items, launcherItem{
			kind:     launcherKindAction,
			action:   launcherActionProject,
			project:  src.id,
			title:    "搜索 " + src.label + " 项目",
			subtitle: "当前触发词 " + prefix + "，回车进入项目模式",
			path:     "launcher-action-project-" + src.id,
		})
	}
	for _, eng := range s.app.webSearchEngines() {
		prefix := strings.TrimSpace(eng.Command)
		if prefix == "" || !strings.HasPrefix(strings.ToLower(prefix), strings.ToLower(q)) {
			continue
		}
		items = append(items, launcherItem{
			kind:     launcherKindAction,
			action:   launcherActionWebSearch,
			project:  eng.ID,
			title:    "用" + webSearchName(eng) + "搜索",
			subtitle: "当前触发词 " + prefix + "，回车进入搜索模式",
			path:     "launcher-action-websearch-" + eng.ID,
		})
	}
	return items
}

// builtinItem 把命中的命令渲染成一行结果，标题带上用户已输入的参数。
func (s *launcherSession) builtinItem(cmd builtinCommand, arg string) launcherItem {
	title := cmd.Title
	if arg != "" {
		title = cmd.Names[0] + " " + arg
	}
	return launcherItem{
		kind:     launcherKindCommand,
		command:  cmd.ID,
		arg:      arg,
		title:    title,
		subtitle: cmd.Desc,
		// 结果行用 path 当 UI key，命令行必须给一个稳定且唯一的值。
		path: "cmd-" + string(cmd.ID),
	}
}

// runBuiltin 执行一条内置命令。KeepOpen 的命令（重建索引）跑完保留浮窗，
// 其余命令执行后隐藏。
func (s *launcherSession) runBuiltin(item launcherItem) {
	cmd, ok := lookupBuiltinCommand(item.command)
	if !ok || !cmd.enabled(s.app) {
		return
	}
	if err := cmd.Run(s, item.arg); err != nil {
		s.notice = err.Error()
		s.fitHeight()
		s.refresh()
		return
	}
	if !cmd.KeepOpen {
		s.hide()
	}
}

// desiredHeight 让浮窗跟着结果数量伸缩：空查询只占搜索框，
// 一两条结果就只占一两行，结果多时封顶在当前尺寸，超出的部分由 Scroll 承接。
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
	if item.kind == launcherKindCommand {
		s.runBuiltin(item)
		return
	}
	if item.kind == launcherKindSearch {
		if item.path != "" {
			if s.openSearch == nil {
				return
			}
			if err := s.openSearch(item.path); err != nil {
				s.notice = "打开浏览器失败：" + err.Error()
				s.fitHeight()
				s.refresh()
				return
			}
			s.hide()
			return
		}
		// 只输入了触发词：补全成「触发词 + 空格」，等用户输入关键词。
		s.completeWebSearch(item.project)
		return
	}
	if item.kind == launcherKindAction {
		switch item.action {
		case launcherActionProject:
			// 补全成完整触发词 + 空格。直接改绑定值时光标会停在输入长度处，
			// 所以递增 inputSeq 重建输入框，让光标落在补全后的末尾。
			prefix := s.app.projectPrefix(item.project)
			if prefix == "" {
				return
			}
			s.query = prefix + " "
			s.inputSeq++
			s.selected = 0
			s.notice = ""
			s.rebuildResults()
		case launcherActionWebSearch:
			s.completeWebSearch(item.project)
		}
		return
	}
	var err error
	switch item.kind {
	case launcherKindApp:
		if s.launchApp != nil {
			err = s.launchApp(item.app)
		}
	case launcherKindProject:
		launch := s.launchProject
		switch s.projectSource {
		case "fork":
			launch = s.launchForkProject
		case "trae":
			launch = s.launchTraeProject
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
			// 没有输入时只保留搜索框，不展示结果列表、命令提示和底部状态。
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
		case src.id == "trae" && s.app.trae.checking:
			return "正在检测 Trae 命令…", "检测完成后即可打开项目。"
		case src.id == "trae" && s.app.trae.err != "":
			return "Trae 命令不可用", s.app.trae.err
		case src.id == "vscode" && settings.LauncherVSCodeEnabled && s.app.vscode.checking:
			return "正在检测 VS Code 命令…", "检测完成后即可打开项目。"
		case src.id == "vscode" && settings.LauncherVSCodeEnabled && s.app.vscode.err != "":
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
	if item.kind == launcherKindCommand {
		if cmd, ok := lookupBuiltinCommand(item.command); ok {
			iconTile(c, cmd.Icon, 28, 8, colAccentSoft, colAccentStr)
			return
		}
		iconTile(c, "launcher", 28, 8, colBgSoft, colTextSoft)
		return
	}
	if item.kind == launcherKindSearch {
		iconTile(c, "language", 28, 8, colAccentSoft, colAccentStr)
		return
	}
	if item.kind == launcherKindAction {
		if item.action == launcherActionProject {
			if toolIcon := launcherToolIcons[item.project]; toolIcon != nil {
				ui.Image(c, toolIcon).Size(28, 28)
				return
			}
			iconTile(c, "code", 28, 8, colAccentSoft, colAccentStr)
			return
		}
		iconTile(c, "refresh", 28, 8, colBgSoft, colTextSoft)
		return
	}
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
		} else if s.selected < len(s.results) {
			sel := s.results[s.selected]
			if sel.kind == launcherKindCommand || sel.kind == launcherKindAction || sel.kind == launcherKindSearch {
				hint = "↵ 执行 · Esc 关闭"
			}
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

// startLauncher 唤起启动器浮窗。启动器是主入口，不再受 LauncherEnabled
// 控制——用户已经用全局快捷键或托盘点到这里，拒绝打开只会显得坏掉。
func (a *app) startLauncher() {
	if a.launcher == nil {
		a.launcher = newLauncherSession(a)
	}
	a.launcher.toggle()
}

// showLauncher 幂等地打开启动器（第二次启动应用时用它，不能把已开的浮窗关掉）。
func (a *app) showLauncher() {
	if a.launcher == nil {
		a.launcher = newLauncherSession(a)
	}
	a.launcher.show()
}

func (a *app) disposeLauncher() {
	if a.launcher == nil {
		return
	}
	a.launcher.dispose()
	a.launcher = nil
}

// homeFeature 是功能中心两列卡片里的一项。status 只给没有配置项的卡片用
// （应用搜索、文件搜索），把加载/索引状态常显在描述下一行。
type homeFeature struct {
	id, title, desc, status string
	on                      *bool
	hasSettings             bool
}

// homeFeaturesGroup 是功能中心的功能与启动项卡片区：一行两张卡片，每张卡片
// 只有标题、描述和右侧的齿轮 + 开关。配置内容不再内联展开，点齿轮开弹框，
// 版面因此短一半，窗口窄时也只需要降列。
func (a *app) homeFeaturesGroup(c *ui.Context) {
	features := []homeFeature{
		{id: "translate", title: "划词翻译", desc: "在任意应用选中文本按快捷键读取，也可翻译剪贴板。", on: &a.settings.TranslationEnabled, hasSettings: true},
		{id: "screenshot", title: "截图标注", desc: "冻结整屏后框选，支持矩形、箭头、文字和马赛克。", on: &a.settings.ScreenshotEnabled, hasSettings: true},
		{id: "image-compress", title: "图片压缩", desc: "批量压缩 PNG / JPEG / WebP，支持本地与 TinyPNG 云。", on: &a.settings.ImageCompress.Enabled, hasSettings: true},
		{id: "websearch", title: "网页搜索", desc: "输入触发词 + 空格，用配置的搜索引擎搜网页。", on: &a.settings.LauncherWebSearchEnabled, hasSettings: true},
		{id: "apps", title: "应用搜索", desc: "搜索已安装应用", status: a.launcherAppStatus(), on: &a.settings.LauncherSearchApps},
		{id: "files", title: "文件搜索", desc: "搜索桌面、下载、文档和主目录浅层", status: a.launcherFileStatus(), on: &a.settings.LauncherSearchFiles},
		{id: "vscode", title: "VS Code 快速打开", desc: "输入触发词 + 空格，快速打开项目", on: &a.settings.LauncherVSCodeEnabled, hasSettings: true},
		{id: "trae", title: "Trae 快速打开", desc: "输入触发词 + 空格，快速打开项目", on: &a.settings.LauncherTraeEnabled, hasSettings: true},
		{id: "fork", title: "Fork 快速打开", desc: "输入触发词 + 空格，用 Fork 打开 Git 仓库", on: &a.settings.LauncherForkEnabled, hasSettings: true},
	}
	ui.Column(c).Gap(10).Children(func() {
		for i := 0; i < len(features); i += 2 {
			end := min(i+2, len(features))
			ui.Row(c).Gap(10).AlignItems(ui.Stretch).Children(func() {
				for j := i; j < end; j++ {
					a.homeFeatureCard(c, features[j])
				}
				// 奇数项时补一个空块，最后一张卡片保持半宽、不会被拉满整行。
				if end-i < 2 {
					ui.Box(c).Grow(1)
				}
			})
		}
	})
}

// homeFeatureCard 画一张功能卡片。整张卡片不可点击：开关负责启停，
// 齿轮负责配置，两者分开避免点开关时误开弹框。
func (a *app) homeFeatureCard(c *ui.Context, f homeFeature) {
	ui.Column(c.Key("home-feature-"+f.id)).Grow(1).MinWidth(0).Padding(16).Radius(12).
		Background(colBg).Border(1, colLine).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			if toolIcon := launcherToolIcons[f.id]; toolIcon != nil {
				ui.Image(c, toolIcon).Size(28, 28)
			} else {
				iconTile(c, homeFeatureIcon(f.id), 28, 8, colBgSoft, colTextSoft)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, f.title).FontSize(13).Bold()
					// 不开设置也能看到是否真的生效，避免开了开关却一直没配置。
					if badge, tone := a.launcherSourceBadge(f.id); badge != "" {
						badgeBg, badgeFg := colBgSoft, colTextMuted
						switch tone {
						case "warn":
							badgeBg, badgeFg = colAccentSoft, colAccentStr
						case "danger":
							badgeBg, badgeFg = ui.RGBA(255, 107, 104, 0.14), colDanger
						}
						pill(c, badge, badgeBg, badgeFg)
					}
				})
				ui.Text(c, f.desc).FontSize(11).TextColor(colTextMuted).MaxLines(2)
				if f.status != "" {
					ui.Text(c, f.status).FontSize(11).TextColor(colTextMuted).MaxLines(2)
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if f.hasSettings {
					gear := ui.Button(c.Key("home-gear-"+f.id), "").Label(f.title+"设置").Tooltip("设置").
						Size(30, 30).Padding(0).Radius(8).Background(colBgSoft).Border(1, colLine)
					gear.Children(func() {
						ui.Icon(c, icon("settings")).FontSize(15).TextColor(colTextMuted)
					})
					if gear.Clicked() {
						a.openFeatureModal(f.id)
					}
				}
				if ui.Switch(c, f.on).Label(f.title + "开关").Changed() {
					switch f.id {
					case "translate", "screenshot":
						a.afterFeatureChange(f.id)
					case "image-compress":
						a.afterImageCompressToggle()
					case "vscode":
						a.afterVSCodeToggle()
					case "trae":
						a.afterTraeToggle()
					case "fork":
						a.afterForkToggle()
					default:
						a.afterLauncherSourceChange()
					}
				}
			})
		})
	})
}

// homeFeatureIcon 给没有品牌图标的卡片选一个线性图标。
func homeFeatureIcon(id string) string {
	switch id {
	case "files":
		return "file"
	case "websearch":
		return "language"
	case "image-compress":
		return "image-compress"
	case "translate":
		return "translate"
	case "screenshot":
		return "screenshot"
	}
	return "app"
}

// featureModalView 渲染功能设置弹框：卡片上的齿轮从这里打开，一次只开一个。
func (a *app) featureModalView(c *ui.Context) {
	if a.featureModal == "" {
		return
	}
	id := a.featureModal
	open := true
	lightModal(c, &open, func() {
		ui.Column(c).Width(620).Gap(16).Padding(4).Children(func() {
			a.featureModalHeader(c, homeFeatureTitle(id))
			// 内容可能很长（翻译服务商网格、项目文件夹列表），超出部分滚动。
			ui.Scroll(c).MaxHeight(560).Children(func() {
				ui.Column(c).Gap(16).Children(func() {
					a.featureSettingsBody(c, id)
				})
			})
		})
	})
}

// featureModalHeader 是功能设置弹框的标题行，关闭按钮清掉当前弹框。
func (a *app) featureModalHeader(c *ui.Context, title string) {
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(5).Children(func() {
			ui.Text(c, "功能设置").FontSize(11).Bold().TextColor(colTextMuted).LetterSpacing(1.4)
			ui.Text(c, title).FontSize(21).Bold()
		})
		// 标签带上功能名，无障碍读起来明确，测试里也不会和供应商弹窗的「关闭」混淆。
		closeBtn := ghostBtn(c, "").Label("关闭 "+title).Size(34, 34).Center()
		closeBtn.Children(func() {
			ui.Icon(c, icon("close")).FontSize(15).TextColor(colTextSoft)
		})
		if closeBtn.Clicked() {
			a.featureModal = ""
		}
	})
}

func homeFeatureTitle(id string) string {
	switch id {
	case "translate":
		return "划词翻译"
	case "screenshot":
		return "截图标注"
	case "image-compress":
		return "图片压缩"
	case "websearch":
		return "网页搜索"
	case "vscode":
		return "VS Code 快速打开"
	case "trae":
		return "Trae 快速打开"
	case "fork":
		return "Fork 快速打开"
	}
	return "功能设置"
}

// featureSettingsBody 是各功能卡片齿轮弹框里的设置内容。
// 关闭状态下只给一句提示，避免弹框里出现一整屏失效的配置。
func (a *app) featureSettingsBody(c *ui.Context, id string) {
	switch id {
	case "translate":
		if !a.settings.TranslationEnabled {
			ui.Text(c, "开启后按快捷键划词翻译，也可以通过「翻译剪贴板」直接翻译。").
				FontSize(12).TextColor(colTextMuted)
			return
		}
		a.hotkeyRecorder(c, "translate")
		a.langSelect(c)
		// 只有 macOS 需要系统授权；Windows 读取选区走模拟 Ctrl+C，没有授权概念。
		if runtime.GOOS == "darwin" {
			a.permissionStrip(c, "辅助功能权限", "用于读取当前应用中被选中的文本。", "accessibility", permissions().Accessibility)
		}
		// 弹框宽度固定，服务商网格直接按两列排。
		a.providerGridCols(c, 2)
		if primaryBtn(c, "翻译剪贴板").Clicked() {
			a.translateClipboard()
		}
	case "screenshot":
		if !a.settings.ScreenshotEnabled {
			ui.Text(c, "开启后按快捷键进入截图标注。").FontSize(12).TextColor(colTextMuted)
			return
		}
		a.hotkeyRecorder(c, "screenshot")
		// Windows 的屏幕捕获不需要授权。
		if runtime.GOOS == "darwin" {
			a.permissionStrip(c, "屏幕录制权限", "用于捕获当前显示器画面并进入标注编辑。", "screenRecording", permissions().ScreenRecording)
		}
		if primaryBtn(c, "立即截图").Clicked() {
			a.startCapture()
		}
	case "image-compress":
		if !a.settings.ImageCompress.Enabled {
			ui.Text(c, "开启后在启动器输入 tiny，或从这里打开图片压缩窗口。").
				FontSize(12).TextColor(colTextMuted)
			return
		}
		ui.Text(c, "支持批量选择或拖入图片、质量与最长边调整、单张另存、覆盖原图和 ZIP 打包。").
			FontSize(12).TextColor(colTextSoft)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			engine := "本地压缩"
			if a.settings.ImageCompress.Engine == "tinypng" {
				engine = "TinyPNG 云"
			}
			pill(c, engine, colAccentSoft, colAccentStr)
			if primaryBtn(c, "打开图片压缩").Clicked() {
				a.showImageCompressor()
			}
		})
		ui.Text(c, "压缩参数在图片压缩窗口右上角的齿轮里调整。").
			FontSize(12).TextColor(colTextMuted)
	case "websearch":
		if !a.settings.LauncherWebSearchEnabled {
			ui.Text(c, "开启后可添加多个搜索引擎，默认提供百度、Google、Bing，也可以自定义。").
				FontSize(12).TextColor(colTextMuted)
			return
		}
		a.webSearchSettings(c)
	case "vscode":
		if !a.settings.LauncherVSCodeEnabled {
			ui.Text(c, "开启后配置命令和项目文件夹，首次开启会自动检测 VS Code。").
				FontSize(12).TextColor(colTextMuted)
			return
		}
		a.vscodeCommandRow(c)
		a.vscodeFolderSection(c)
	case "trae":
		if !a.settings.LauncherTraeEnabled {
			ui.Text(c, "开启后配置命令和项目文件夹，首次开启会自动检测 Trae。").
				FontSize(12).TextColor(colTextMuted)
			return
		}
		a.traeCommandRow(c)
		a.traeFolderSection(c)
	case "fork":
		if !a.settings.LauncherForkEnabled {
			ui.Text(c, "开启后配置命令和项目文件夹；Fork 只能打开 Git 仓库，列表只列含 .git 的目录。").
				FontSize(12).TextColor(colTextMuted)
			return
		}
		a.forkCommandRow(c)
		a.forkFolderSection(c)
	}
}

// homeCommandsSection 是功能中心里「内置命令」的折叠小节，放在功能卡片上方：
// 默认收起，点标题行展开只读的命令说明，平时不占版面。
func (a *app) homeCommandsSection(c *ui.Context) {
	expanded := a.homeCommandsExpanded
	bg, border := colCard, colLine
	if expanded {
		bg, border = ui.Hex("#26272b"), colLineStrong
	}
	ui.Column(c.Key("home-commands")).Radius(12).Background(bg).
		Border(1, border).Shadow(0, 2, 12, 0, shadowCard).Children(func() {
		header := ui.Row(c).Padding(14, 16).Gap(12).AlignItems(ui.Center)
		header.Label("展开或收起 内置命令")
		header.Children(func() {
			iconTile(c, "code", 30, 9, colBgSoft, colTextSoft)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, "内置命令").FontSize(13).Bold()
				ui.Text(c, "在启动器浮窗中输入后回车执行；命令固定，不可修改。").
					FontSize(11).TextColor(colTextMuted).MaxLines(1)
			})
			ui.Icon(c, icon("chevron-down")).FontSize(16).TextColor(colTextMuted).
				Rotate(map[bool]float32{true: 180}[expanded])
		})
		if header.Clicked() {
			a.homeCommandsExpanded = !a.homeCommandsExpanded
		}
		if expanded {
			ui.Column(c).Padding(0, 16, 16, 16).Gap(8).Children(func() {
				for _, cmd := range builtinCommandTable() {
					if cmd.enabled(a) {
						builtinCommandRow(c, cmd.Names, cmd.Desc)
					}
				}
			})
		}
	})
}

// builtinCommandRow 展示一条只读的启动器命令。commands 是同一命令的多种
// 写法（如英文名和中文名），并排展示让用户一眼看出都能用；命令本身不可编辑。
func builtinCommandRow(c *ui.Context, commands []string, desc string) {
	ui.Row(c).Padding(11, 12).Gap(12).AlignItems(ui.Center).
		Radius(10).Background(colBg).Border(1, colLine).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			for i, command := range commands {
				if i > 0 {
					ui.Text(c, "或").FontSize(11).TextColor(colTextMuted)
				}
				ui.Row(c).Padding(4, 9).Radius(7).Background(colBgSoft).Border(1, colLine).Children(func() {
					ui.Text(c, command).Font("SF Mono, Menlo, monospace").FontSize(12).TextColor(colTextSoft)
				})
			}
		})
		ui.Text(c, desc).FontSize(12).TextColor(colTextMuted).Grow(1)
	})
}

// 启动项卡片使用的品牌图标，没有时退回通用线性图标。
var launcherToolIcons = map[string]ui.ImageSource{
	"vscode": vscodeIcon,
	"fork":   forkIcon,
	"trae":   traeIcon,
}

// launcherSourceBadge 返回启动项卡片标题行上的状态标签。检测中、配置
// 不可用或缺少项目文件夹时直接显示，用户不用展开详情就能发现没生效。
// tone 决定颜色：muted 中性、warn 待配置、danger 不可用。
func (a *app) launcherSourceBadge(id string) (text, tone string) {
	switch id {
	case "translate":
		if !a.settings.TranslationEnabled {
			return "", ""
		}
		if len(translationProviders(a.resolvedProviders())) == 0 {
			return "未配置服务", "warn"
		}
		return "", ""
	case "screenshot":
		if !a.settings.ScreenshotEnabled {
			return "", ""
		}
		if !screenRecordingGranted() {
			return "缺少屏幕录制权限", "warn"
		}
		return "", ""
	case "websearch":
		if !a.settings.LauncherWebSearchEnabled {
			return "", ""
		}
		if len(a.webSearchEngines()) == 0 {
			return "未配置引擎", "warn"
		}
		return "", ""
	case "vscode":
		return launcherToolBadge(
			a.settings.LauncherVSCodeEnabled, a.settings.LauncherVSCodeFolders,
			a.vscode.checking, a.vscode.checked, a.vscodeIsReady(),
		)
	case "trae":
		return launcherToolBadge(
			a.settings.LauncherTraeEnabled, a.settings.LauncherTraeFolders,
			a.trae.checking, a.trae.checked, a.traeIsReady(),
		)
	case "fork":
		return launcherToolBadge(
			a.settings.LauncherForkEnabled, a.settings.LauncherForkFolders,
			a.fork.checking, a.fork.checked, a.forkIsReady(),
		)
	}
	return "", ""
}

func launcherToolBadge(enabled bool, folders []string, checking, checked, ready bool) (text, tone string) {
	if !enabled {
		return "", ""
	}
	if checking {
		return "检测中", "muted"
	}
	if checked && !ready {
		return "未生效", "danger"
	}
	// 检测通过但还没有项目文件夹时，功能同样用不了。
	if ready && len(normalizeProjectFolders(folders)) == 0 {
		return "未配置文件夹", "warn"
	}
	return "", ""
}

// projectTool 汇总一个项目工具（VS Code / Fork）在设置页需要的状态与回调。
// 两者的设置内容只有命令、检测状态和文件夹列表，抽成一份避免重复。
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

func (a *app) traeTool() projectTool {
	return projectTool{
		id: "trae", label: "Trae", placeholder: "trae",
		command:   &a.settings.LauncherTraeCommand,
		folders:   &a.settings.LauncherTraeFolders,
		status:    a.traeStatusText(),
		checking:  a.trae.checking,
		failed:    a.trae.err != "",
		ready:     a.traeIsReady(),
		onCommand: a.afterToolCommandChange,
		onRetry:   a.detectTraeCommand,
		onPick:    a.pickTraeFolders,
		onRemove:  func(i int) { a.removeProjectFolder(&a.settings.LauncherTraeFolders, i, a.afterTraeFoldersChange) },
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

func (a *app) traeCommandRow(c *ui.Context) { a.projectCommandRow(c, a.traeTool()) }

func (a *app) traeFolderSection(c *ui.Context) { a.projectFolderSection(c, a.traeTool()) }

func (a *app) forkCommandRow(c *ui.Context) { a.projectCommandRow(c, a.forkTool()) }

func (a *app) forkFolderSection(c *ui.Context) { a.projectFolderSection(c, a.forkTool()) }

func (a *app) projectCommandRow(c *ui.Context, t projectTool) {
	ui.Column(c).Gap(7).Children(func() {
		ui.Text(c, "触发词").FontSize(11).Bold().TextColor(colTextSoft)
		in := ui.TextInput(c.Key(t.id+"-command"), t.command).
			Placeholder(t.placeholder).Label(t.label + " 触发词").Height(40).WidthPercent(100)
		if in.Changed() {
			// 触发词在浮窗里必须唯一，占用内置命令或搜索触发词后「命令 + 空格」
			// 会被别的功能截走，所以这里拒绝保存并回退到默认触发词。
			if reason := a.projectCommandConflict(*t.command); reason != "" {
				*t.command = ""
				t.onCommand()
				a.toast(reason)
				return
			}
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

// webSearchSettings 是「网页搜索」卡片齿轮弹框里的设置：引擎列表 + 预设添加。
func (a *app) webSearchSettings(c *ui.Context) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, "搜索引擎").FontSize(11).Bold().TextColor(colTextSoft)
			ui.Text(c, "输入触发词 + 空格 + 关键词搜索网页；地址用 %s 表示关键词。").
				FontSize(11).TextColor(colTextMuted)
		})

		if len(a.settings.LauncherWebSearchEngines) == 0 {
			ui.Row(c).Padding(12).Radius(10).Background(ui.RGBA(255, 255, 255, 0.035)).
				Border(1, colLine).Children(func() {
				ui.Text(c, "还没有添加搜索引擎，点击下方预设开始。").FontSize(12).TextColor(colTextMuted)
			})
		}
		for i := range a.settings.LauncherWebSearchEngines {
			eng := &a.settings.LauncherWebSearchEngines[i]
			ui.Column(c.Key("websearch-"+eng.ID)).Gap(8).Padding(12).Radius(10).
				Background(ui.RGBA(255, 255, 255, 0.035)).Border(1, colLine).Children(func() {
				fieldLabel := func(text string) {
					ui.Text(c, text).FontSize(11).TextColor(colTextMuted).Width(52)
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					fieldLabel("名称")
					if in := ui.TextInput(c.Key("websearch-name-"+eng.ID), &eng.Name).
						Placeholder("如 百度").Height(36).Grow(1); in.Changed() {
						a.afterWebSearchChange()
					}
					if ghostBtn(c, "移除").Label("移除 " + webSearchName(*eng)).MinHeight(26).Clicked() {
						a.removeWebSearchEngine(eng.ID)
					}
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					fieldLabel("触发词")
					if in := ui.TextInput(c.Key("websearch-command-"+eng.ID), &eng.Command).
						Placeholder("如 bd").Height(36).Grow(1); in.Changed() {
						// 触发词在浮窗里必须唯一，冲突时回退，避免永远匹配不到。
						if reason := a.webSearchCommandConflict(eng.ID, eng.Command); reason != "" {
							eng.Command = ""
							a.afterWebSearchChange()
							a.toast(reason)
						} else {
							a.afterWebSearchChange()
						}
					}
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					fieldLabel("搜索地址")
					if in := ui.TextInput(c.Key("websearch-url-"+eng.ID), &eng.URL).
						Placeholder("https://www.baidu.com/s?wd=%s").
						Height(36).Grow(1); in.Changed() {
						a.afterWebSearchChange()
					}
				})
				if strings.TrimSpace(eng.URL) != "" && !strings.Contains(eng.URL, "%s") {
					ui.Text(c, "搜索地址需要包含 %s 占位符").FontSize(11).TextColor(colDanger)
				}
			})
		}

		ui.Row(c).Gap(8).Children(func() {
			added := make(map[string]bool, len(a.settings.LauncherWebSearchEngines))
			for _, eng := range a.settings.LauncherWebSearchEngines {
				added[eng.ID] = true
			}
			for _, preset := range webSearchPresets {
				if added[preset.ID] {
					continue
				}
				if secondaryBtn(c, "添加 "+preset.Name).Clicked() {
					a.addWebSearchPreset(preset.ID)
				}
			}
			if secondaryBtn(c, "自定义").Clicked() {
				a.addCustomWebSearch()
			}
		})
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

func (a *app) pickTraeFolders() {
	a.pickProjectFolders(&a.settings.LauncherTraeFolders, a.afterTraeFoldersChange)
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

func (a *app) afterTraeFoldersChange() { a.afterProjectFoldersChange() }

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

func (a *app) afterTraeToggle() {
	if err := a.persist(); err != nil {
		a.toast("保存失败：" + err.Error())
		return
	}
	if a.settings.LauncherTraeEnabled {
		a.detectTraeCommand()
		a.toast("Trae 快速打开已开启")
	} else {
		a.trae = traeState{}
		a.refreshMain()
		a.toast("Trae 快速打开已关闭")
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
