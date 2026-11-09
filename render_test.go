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
	outDir := "/tmp/toolbox_render"
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
		{"settings_1080", "settings", 1080, 760, false},
		{"modal_1080", "translation", 1080, 760, true},
		// 收窄到最小窗口，验证堆叠/降列
		{"overview_900", "overview", 900, 640, false},
		{"translation_900", "translation", 900, 640, false},
		{"screenshot_900", "screenshot", 900, 640, false},
	}

	for _, s := range snaps {
		a := newTestApp()
		a.section = s.sec
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
}
