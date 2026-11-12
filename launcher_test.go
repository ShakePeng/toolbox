package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func newTestLauncherSession() (*launcherSession, *string, *string) {
	a := newTestApp()
	a.settings.LauncherEnabled = true
	a.settings.LauncherSearchApps = true
	a.settings.LauncherSearchFiles = true

	launched := ""
	opened := ""
	s := &launcherSession{
		app:        a,
		appsLoaded: true,
		apps: []launcherAppEntry{
			{app: mygo.InstalledApp{Name: "Visual Studio Code", Path: "/Applications/Visual Studio Code.app"},
				nameLower: "visual studio code", pathLower: "/applications/visual studio code.app", altLower: "visual studio code"},
			{app: mygo.InstalledApp{Name: "Code Runner", Path: "/Applications/Code Runner.app"},
				nameLower: "code runner", pathLower: "/applications/code runner.app", altLower: "code runner"},
		},
		files: &launcherFileIndex{
			built: true,
			entries: []launcherFileEntry{
				{path: "/Users/me/Documents/report.pdf", name: "report.pdf", dir: "/Users/me/Documents",
					nameLower: "report.pdf", pathLower: "/users/me/documents/report.pdf", depth: 1, rank: 2},
			},
		},
		icons: make(map[string]*launcherIcon),
		launchApp: func(app mygo.InstalledApp) error {
			launched = app.Name
			return nil
		},
		openPath: func(path string) error {
			opened = path
			return nil
		},
	}
	s.rebuildResults()
	return s, &launched, &opened
}

func TestLauncherViewRender(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	s.query = "code"
	s.rebuildResults()
	tt := ui.NewTester(s.view, launcherWidth, launcherHeight)

	if !tt.HasText("Visual Studio Code") || !tt.HasText("Code Runner") {
		t.Fatalf("浮窗应显示应用结果，实际文本：%q", tt.Texts())
	}
	if !tt.HasText("↑↓ 选择 · ↵ 打开 · Esc 关闭") {
		t.Errorf("底部应显示键盘提示，实际文本：%q", tt.Texts())
	}
}

func TestLauncherEmptyQueryOnlyShowsSearch(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	tt := ui.NewTester(s.view, launcherWidth, launcherHeaderHeight)

	if tt.HasText("Visual Studio Code") || tt.HasText("Code Runner") || tt.HasText("report.pdf") {
		t.Errorf("空查询不应显示结果，实际文本：%q", tt.Texts())
	}
	if tt.HasText("↑↓ 选择 · ↵ 打开 · Esc 关闭") {
		t.Errorf("空查询不应显示底部状态，实际文本：%q", tt.Texts())
	}
}

func TestLauncherDesiredHeight(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	if got := s.desiredHeight(); got != launcherHeaderHeight {
		t.Errorf("空查询应只占搜索框高度，实际 %d", got)
	}

	s.query = "x"
	s.results = nil
	if got, want := s.desiredHeight(), launcherHeaderHeight+launcherFooterHeight+launcherEmptyHeight; got != want {
		t.Errorf("无结果时应为 %d，实际 %d", want, got)
	}

	s.results = make([]launcherItem, 1)
	if got, want := s.desiredHeight(), launcherHeaderHeight+launcherFooterHeight+launcherListPadding+launcherRowHeight; got != want {
		t.Errorf("单条结果时应为 %d，实际 %d", want, got)
	}

	s.results = make([]launcherItem, launcherMaxResults)
	if got, want := s.desiredHeight(), launcherHeaderHeight+launcherFooterHeight+launcherListPadding+launcherMaxResults*launcherRowHeight; got != want {
		t.Errorf("最大结果数时应为 %d，实际 %d", want, got)
	}

	s.results = make([]launcherItem, launcherMaxResults+2)
	if got := s.desiredHeight(); got != launcherHeight {
		t.Errorf("超过最大结果数时应封顶为 %d，实际 %d", launcherHeight, got)
	}
}

func TestLauncherFilterFollowsQuery(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	tt := ui.NewTester(s.view, launcherWidth, launcherHeight)

	s.query = "report"
	s.rebuildResults()
	tt.Frame()
	if !tt.HasText("report.pdf") {
		t.Fatalf("应显示文件结果，实际文本：%q", tt.Texts())
	}
	if tt.HasText("Code Runner") {
		t.Errorf("不匹配的应用不应显示：%q", tt.Texts())
	}
}

func TestLauncherKeyboardSelectAndOpen(t *testing.T) {
	s, launched, _ := newTestLauncherSession()
	s.query = "code"
	s.rebuildResults()
	if len(s.results) < 2 {
		t.Fatalf("应至少有两个匹配结果，实际 %d", len(s.results))
	}

	if !s.handleTextKey(ui.KeyDown, 0) {
		t.Fatal("Down 键应被消费")
	}
	if s.selected != 1 {
		t.Fatalf("Down 后应选中第二项，实际 %d", s.selected)
	}
	if !s.handleTextKey(ui.KeyDown, 0) {
		t.Fatal("Down 键应被消费")
	}
	if s.selected != 1 {
		t.Fatalf("末尾继续 Down 不应越界，实际 %d", s.selected)
	}
	if !s.handleTextKey(ui.KeyUp, 0) {
		t.Fatal("Up 键应被消费")
	}
	if s.selected != 0 {
		t.Fatalf("Up 后应回到第一项，实际 %d", s.selected)
	}

	if !s.handleTextKey(ui.KeyEnter, 0) {
		t.Fatal("Enter 应被消费")
	}
	if *launched != s.results[0].app.Name {
		t.Fatalf("应打开选中应用 %q，实际 %q", s.results[0].app.Name, *launched)
	}
}

func TestLauncherOpensFile(t *testing.T) {
	s, _, opened := newTestLauncherSession()
	s.query = "report"
	s.rebuildResults()
	if len(s.results) != 1 {
		t.Fatalf("应只匹配一个文件，实际 %d", len(s.results))
	}
	if !s.handleTextKey(ui.KeyEnter, 0) {
		t.Fatal("Enter 应被消费")
	}
	if *opened != "/Users/me/Documents/report.pdf" {
		t.Fatalf("应打开文件路径，实际 %q", *opened)
	}
}

func TestLauncherEscapeAndNilWindow(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	if !s.handleTextKey(ui.KeyEscape, 0) {
		t.Fatal("Esc 应被消费")
	}
	s.hide() // 测试没有窗口，不应 panic
}

func TestLauncherDisabledSourcesHint(t *testing.T) {
	s, _, _ := newTestLauncherSession()
	s.app.settings.LauncherSearchApps = false
	s.app.settings.LauncherSearchFiles = false
	s.query = "x"
	s.rebuildResults()
	tt := ui.NewTester(s.view, launcherWidth, launcherHeight)
	if !tt.HasText("未启用任何搜索源") {
		t.Fatalf("两个搜索源都关闭时应显示引导，实际文本：%q", tt.Texts())
	}
}

func TestLauncherSettingsPageHasNoSaveButton(t *testing.T) {
	a := newTestApp()
	a.settings.LauncherEnabled = true
	tt := ui.NewTester(a.view, 980, 680)

	if err := tt.Click("快速启动"); err != nil {
		t.Fatalf("点击“启动”导航失败：%v", err)
	}
	if !tt.HasText("唤起快捷键") || !tt.HasText("启动项") {
		t.Fatalf("启动页应显示快捷键和启动项，实际文本：%q", tt.Texts())
	}
	if tt.HasText("保存启动器设置") {
		t.Error("启动页不应显示保存按钮")
	}
}

func newTestProjectLauncher() (*launcherSession, *string) {
	s, _, _ := newTestLauncherSession()
	s.app.settings.LauncherVSCodeEnabled = true
	s.app.settings.LauncherVSCodeCommand = "code"
	s.app.settings.LauncherVSCodeFolders = []string{"/projects"}
	s.projects = []launcherProjectEntry{
		{path: "/projects/alpha", name: "Alpha", root: "/projects",
			nameLower: "alpha", pathLower: "/projects/alpha"},
		{path: "/projects/beta", name: "Beta", root: "/projects",
			nameLower: "beta", pathLower: "/projects/beta"},
	}
	s.projectsLoaded = true
	s.projectRootsSig = strings.Join(normalizeProjectFolders(s.app.settings.LauncherVSCodeFolders), "\x00")
	launched := ""
	s.launchProject = func(path string) error {
		launched = path
		return nil
	}
	return s, &launched
}

func TestLauncherProjectMode(t *testing.T) {
	s, launched := newTestProjectLauncher()

	s.query = "code "
	s.rebuildResults()
	if len(s.results) != 2 {
		t.Fatalf("code + 空格应列出全部项目，实际 %d 条：%+v", len(s.results), s.results)
	}
	if s.results[0].kind != launcherKindProject {
		t.Errorf("结果类型应为项目：%+v", s.results[0])
	}

	s.query = "code bet"
	s.rebuildResults()
	if len(s.results) != 1 || s.results[0].title != "Beta" {
		t.Fatalf("应只匹配 Beta：%+v", s.results)
	}
	if !s.handleTextKey(ui.KeyEnter, 0) {
		t.Fatal("Enter 应被消费")
	}
	if *launched != "/projects/beta" {
		t.Fatalf("应用 VSCode 打开 /projects/beta，实际 %q", *launched)
	}
}

func TestLauncherProjectModeRequiresSpace(t *testing.T) {
	s, _ := newTestProjectLauncher()
	s.query = "code"
	s.rebuildResults()
	if _, _, ok := s.projectQuery(); ok {
		t.Fatal("没有空格时不应进入项目模式")
	}
	for _, item := range s.results {
		if item.kind == launcherKindProject {
			t.Errorf("普通搜索不应返回项目：%+v", item)
		}
	}
}

func TestLauncherProjectModeEmptyHint(t *testing.T) {
	s, _ := newTestProjectLauncher()
	s.app.settings.LauncherVSCodeFolders = nil
	s.projectRootsSig = ""
	s.projectsLoaded = false
	s.query = "code "
	s.rebuildResults()
	tt := ui.NewTester(s.view, launcherWidth, launcherHeight)
	if !tt.HasText("还没有配置项目文件夹") {
		t.Fatalf("没有配置文件夹时应显示引导，实际文本：%q", tt.Texts())
	}
}

func TestLauncherVSCodeSettingsVisible(t *testing.T) {
	a := newTestApp()
	a.settings.LauncherEnabled = true
	a.settings.LauncherVSCodeEnabled = true
	a.settings.LauncherVSCodeCommand = "code"
	a.settings.LauncherVSCodeFolders = []string{"/projects"}

	tt := ui.NewTester(a.view, 1080, 760)
	if err := tt.Click("快速启动"); err != nil {
		t.Fatalf("点击“启动”失败：%v", err)
	}
	// 未展开时只显示标题行，配置内容收在抽屉里。
	for _, want := range []string{"VS Code 快速打开", "应用搜索", "文件搜索"} {
		if !tt.HasText(want) {
			t.Errorf("启动项列表应显示 %q，实际文本：%q", want, tt.Texts())
		}
	}
	if tt.HasText("项目文件夹") {
		t.Errorf("未展开时不应显示 VSCode 配置，实际文本：%q", tt.Texts())
	}

	// 点击整行展开抽屉，露出命令与项目文件夹配置。
	if err := tt.Click("展开或收起 VS Code 快速打开"); err != nil {
		t.Fatalf("点击 VSCode 启动项行失败：%v", err)
	}
	for _, want := range []string{"触发词", "项目文件夹", "/projects"} {
		if !tt.HasText(want) {
			t.Errorf("展开后应显示 %q，实际文本：%q", want, tt.Texts())
		}
	}
	if tt.HasText("保存启动器设置") {
		t.Error("启动页不应显示保存按钮")
	}
}
