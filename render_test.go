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
		{"translation_1080", "translation", 1080, 760, false},
		{"screenshot_1080", "screenshot", 1080, 760, false},
		{"launcher_1080", "launcher", 1080, 760, false},
		{"launcher_vscode_1080", "launcher", 1080, 760, false},
		{"settings_1080", "settings", 1080, 760, false},
		{"modal_1080", "translation", 1080, 760, true},
		// 收窄到最小窗口，验证堆叠/降列
		{"overview_900", "overview", 900, 640, false},
		{"translation_900", "translation", 900, 640, false},
		{"screenshot_900", "screenshot", 900, 640, false},
		{"launcher_900", "launcher", 900, 640, false},
	}

	for _, s := range snaps {
		a := newTestApp()
		a.section = s.sec
		if s.sec == "launcher" {
			a.settings.LauncherEnabled = true
		}
		if s.name == "launcher_vscode_1080" {
			a.settings.LauncherVSCodeEnabled = true
			a.settings.LauncherVSCodeCommand = "code"
			a.settings.LauncherVSCodeFolders = []string{"~/projects", "~/work"}
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
	pt := ui.NewTester(project.view, launcherWidth, project.desiredHeight())
	pt.Frame()
	if img := pt.Image(); img != nil {
		f, err := os.Create(filepath.Join(outDir, "launcher_projects.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		f.Close()
		t.Logf("wrote launcher_projects.png")
	}
}
