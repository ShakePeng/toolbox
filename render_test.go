package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestRenderSnapshots 是开发用辅助：把各页面在不同窗口宽度下渲染成 PNG，方便核对响应式。
// 仅在设置 RENDER=1 时运行：
//
//	RENDER=1 go test -run TestRenderSnapshots -v
func TestRenderSnapshots(t *testing.T) {
	if os.Getenv("RENDER") == "" {
		t.Skip("set RENDER=1 to render snapshots")
	}
	outDir := filepath.Join(os.TempDir(), "toolbox_render")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	type snapshot struct {
		name string
		sec  string
		w, h int
		open bool
	}
	snaps := []snapshot{
		{"overview_1080", "overview", 1080, 760, false},
		// 功能中心很长：启动项卡片和内置命令在首屏之下，用高窗口完整渲染。
		{"overview_full_1080", "overview", 1080, 1500, false},
		{"overview_vscode_expanded_1080", "overview", 1080, 1500, false},
		{"overview_apps_expanded_1080", "overview", 1080, 1500, false},
		{"overview_fork_expanded_1080", "overview", 1080, 1500, false},
		{"overview_commands_expanded_1080", "overview", 1080, 1500, false},
		{"ai_1080", "ai", 1080, 760, false},
		{"settings_1080", "settings", 1080, 760, false},
		{"overview_translate_expanded_1080", "overview", 1080, 1500, false},
		{"overview_screenshot_expanded_1080", "overview", 1080, 1500, false},
		{"overview_image_compress_expanded_1080", "overview", 1080, 1500, false},
		{"modal_launcher_hotkey_1080", "overview", 1080, 760, false},
		{"modal_provider_1080", "overview", 1080, 760, true},
		// 收窄到最小窗口，验证堆叠/降列
		{"overview_900", "overview", 900, 640, false},
		{"overview_full_900", "overview", 900, 1500, false},
	}

	for _, s := range snaps {
		a := newTestApp()
		a.section = s.sec
		if s.name == "overview_vscode_expanded_1080" {
			a.settings.LauncherVSCodeEnabled = true
			a.settings.LauncherVSCodeCommand = "code"
			a.settings.LauncherVSCodeFolders = []string{"~/projects", "~/work"}
			a.launcherSourceExpanded = "vscode"
		}
		// 展开态快照：验证配置抽屉在卡片内的排版，以及没有内容的项不会留出空抽屉。
		if s.name == "overview_apps_expanded_1080" {
			a.launcherSourceExpanded = "apps"
		}
		if s.name == "overview_fork_expanded_1080" {
			a.settings.LauncherForkEnabled = true
			a.settings.LauncherForkCommand = "fork"
			a.settings.LauncherForkFolders = []string{"~/workSpace", "~/studySpace"}
			a.launcherSourceExpanded = "fork"
		}
		if s.name == "overview_commands_expanded_1080" {
			a.homeCommandsExpanded = true
		}
		if s.name == "overview_translate_expanded_1080" {
			a.launcherSourceExpanded = "translate"
		}
		if s.name == "overview_screenshot_expanded_1080" {
			a.launcherSourceExpanded = "screenshot"
		}
		if s.name == "overview_image_compress_expanded_1080" {
			a.launcherSourceExpanded = "image-compress"
		}
		if s.name == "modal_launcher_hotkey_1080" {
			a.configModal = "launcher-hotkey"
		}
		if s.open {
			a.draft = newDraft(2, a.providers[2]) // openai
		}
		tt := ui.NewTester(a.view, s.w, s.h)
		tt.Frame()
		img := tt.Image()
		if img == nil {
			t.Fatalf("%s: empty image", s.name)
		}
		f, err := os.Create(filepath.Join(outDir, s.name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("wrote %s.png", s.name)
	}

	// 启动器浮窗使用独立会话，和主窗口分开渲染：空查询只显示搜索框，
	// 有查询时按结果数量自适应高度。
	launcher, _, _ := newTestLauncherSession()
	renderLauncher := func(name string, query string) {
		launcher.query = query
		launcher.rebuildResults()
		lt := ui.NewTester(launcher.view, launcherWidth, launcher.desiredHeight())
		lt.Frame()
		img := lt.Image()
		if img == nil {
			t.Fatalf("%s: empty image", name)
		}
		f, err := os.Create(filepath.Join(outDir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("wrote %s.png", name)
	}
	renderLauncher("launcher_window_empty", "")
	renderLauncher("launcher_window", "code")

	// VSCode 项目模式：输入 code + 空格后列出配置目录的下一级项目。
	project, _ := newTestProjectLauncher()
	project.query = "code "
	project.rebuildResults()
	renderSession("launcher_projects.png", project)

	// Fork 项目模式：只列 Git 仓库，结果图标用 Fork 应用图标。
	fork, _, _ := newTestLauncherSession()
	fork.app.settings.LauncherVSCodeEnabled = false
	fork.app.settings.LauncherForkEnabled = true
	fork.app.settings.LauncherForkCommand = "fork"
	fork.app.settings.LauncherForkFolders = []string{"/repos"}
	fork.projects = []launcherProjectEntry{
		{path: "/repos/alpha", name: "Alpha", root: "/repos", nameLower: "alpha", pathLower: "/repos/alpha"},
		{path: "/repos/beta", name: "Beta", root: "/repos", nameLower: "beta", pathLower: "/repos/beta"},
	}
	fork.projectsLoaded = true
	fork.projectRootsSig = "fork\x00/repos"
	fork.query = "fork "
	fork.rebuildResults()
	renderSession("launcher_fork_projects.png", fork)

	// 图片压缩是独立窗口，分别检查空态、混合队列和设置页。
	compressor := &imageCompressSession{app: newTestApp()}
	compressor.syncSettingDrafts()
	renderImageCompress("image_compress_empty.png", compressor)
	compressor.items = []*imageCompressItem{
		{ID: 1, SourcePath: "/tmp/example.png", Format: imageFormatPNG, OriginalSize: 240000, Data: make([]byte, 120000), Status: tinyDone, Selected: true},
		{ID: 2, SourcePath: "/tmp/photo.jpg", Format: imageFormatJPEG, OriginalSize: 180000, Status: tinyProcessing},
		{ID: 3, SourcePath: "/tmp/broken.webp", Format: imageFormatWebP, OriginalSize: 9000, Status: tinyError, Err: "读取图片失败"},
	}
	compressor.nextID = 3
	renderImageCompress("image_compress_items.png", compressor)
	settings := &imageCompressSession{app: newTestApp(), settingsOpen: true}
	settings.syncSettingDrafts()
	renderImageCompress("image_compress_settings.png", settings)
	cloud := &imageCompressSession{app: newTestApp(), settingsOpen: true}
	cloud.app.settings.ImageCompress.Engine = "tinypng"
	cloud.app.settings.ImageCompress.TinyPNGKeys = []TinyPNGKey{{Key: "0123456789abcdef0123456789abcdef", Usage: 42}}
	cloud.syncSettingDrafts()
	renderImageCompress("image_compress_settings_cloud.png", cloud)
}

func renderImageCompress(name string, s *imageCompressSession) {
	tt := ui.NewTester(s.view, 1000, 760)
	tt.Frame()
	img := tt.Image()
	if img == nil {
		panic("空快照：" + name)
	}
	f, err := os.Create(filepath.Join(os.TempDir(), "toolbox_render", name))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

// renderSession 把一个启动器会话渲染成 PNG 快照。
func renderSession(name string, s *launcherSession) {
	tt := ui.NewTester(s.view, launcherWidth, s.desiredHeight())
	tt.Frame()
	img := tt.Image()
	if img == nil {
		panic("空快照：" + name)
	}
	f, err := os.Create(filepath.Join(os.TempDir(), "toolbox_render", name))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
