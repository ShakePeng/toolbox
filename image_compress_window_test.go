package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTinyBuiltinCommandMatching(t *testing.T) {
	for _, query := range []string{"tiny", "图片压缩"} {
		cmd, _, ok := matchBuiltinCommand(query)
		if !ok || cmd.ID != cmdTiny {
			t.Errorf("%q 应匹配图片压缩，实际 %+v, %v", query, cmd, ok)
		}
	}
	if !isBuiltinCommand("tiny") || isBuiltinCommand("tin") {
		t.Error("触发词冲突校验只接受完整 tiny")
	}
}

func TestImageCompressWindowEmptyAndSettings(t *testing.T) {
	a := newTestApp()
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	s := &imageCompressSession{app: a}
	s.syncSettingDrafts()
	tt := ui.NewTester(s.view, 1000, 760)
	tt.Frame()
	for _, text := range []string{"图片压缩", "拖入图片或点击选择", "开始压缩", "队列还是空的", "本地压缩", "TinyPNG 云"} {
		if !tt.HasText(text) {
			t.Errorf("空态缺少 %q：%q", text, tt.Texts())
		}
	}
	if tt.HasText("返回压缩") {
		t.Fatal("设置改成弹框后不应再有「返回压缩」按钮")
	}
	if err := tt.Click("压缩设置"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("PNG 优化级别（0–6）") || !tt.HasText("JPEG 质量（1–100）") || !tt.HasText("最长边像素（0 不限制）") {
		t.Fatalf("设置弹框内容不完整：%q", tt.Texts())
	}
	if tt.HasText("保存 PNG 设置") {
		t.Fatal("设置应即时保存，不应出现单独保存按钮")
	}
	// 弹框右上角的图标是唯一显式关闭入口，关闭后回到工具页。
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("PNG 优化级别（0–6）") {
		t.Fatalf("关闭弹框后设置内容应消失：%q", tt.Texts())
	}
}

func TestImageCompressWindowMixedItems(t *testing.T) {
	a := newTestApp()
	s := &imageCompressSession{
		app: a, nextID: 2,
		items: []*imageCompressItem{
			{ID: 1, SourcePath: "/tmp/done.png", Format: imageFormatPNG, OriginalSize: 2000, Data: []byte("small"), Status: tinyDone, Selected: true},
			{ID: 2, SourcePath: "/tmp/bad.jpg", Format: imageFormatJPEG, OriginalSize: 100, Status: tinyError, Err: "读取图片失败"},
		},
	}
	tt := ui.NewTester(s.view, 1000, 760)
	tt.Frame()
	// 行内操作已改成图标按钮，文字不再出现在界面里，改由 Tooltip 承担说明。
	for _, text := range []string{"done.png", "减少 99%", "bad.jpg", "读取图片失败", "已完成 1/2"} {
		if !tt.HasText(text) {
			t.Errorf("列表缺少 %q：%q", text, tt.Texts())
		}
	}
}

func TestImageCompressTinyPNGSettings(t *testing.T) {
	a := newTestApp()
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	a.settings.ImageCompress.Engine = "tinypng"
	a.settings.ImageCompress.TinyPNGKeys = []TinyPNGKey{{Key: "test-key-12345678", Usage: 42}}
	s := &imageCompressSession{app: a, settingsOpen: true}
	s.syncSettingDrafts()
	tt := ui.NewTester(s.view, 1000, 760)
	tt.Frame()
	if !tt.HasText("本月已用 42/500") || tt.HasText("PNG 优化级别（0–6）") {
		t.Fatalf("云端设置展示不正确：%q", tt.Texts())
	}
	if tt.HasText("TinyPNG API Key") || tt.HasText("最长边像素（0 不限制）") {
		t.Fatalf("云端设置不应再出现额外的标题或最长边字段：%q", tt.Texts())
	}
	if tt.HasText("12345678") {
		t.Fatal("界面不能展示完整 API Key")
	}
}

func TestImageCompressSelectionWaitsForManualStart(t *testing.T) {
	a := newTestApp()
	s := &imageCompressSession{app: a}
	path := filepath.Join(t.TempDir(), "waiting.png")
	if err := os.WriteFile(path, testImageBytes(t, imageFormatPNG, 24, 24), 0o600); err != nil {
		t.Fatal(err)
	}
	s.addPaths([]string{path})
	if len(s.items) != 1 || s.items[0].Status != tinyPending || s.running {
		t.Fatalf("选图后应只进入等待队列，实际 %+v，running=%v", s.items, s.running)
	}
	tt := ui.NewTester(s.view, 1000, 760)
	tt.Frame()
	for _, text := range []string{"waiting.png", "等待压缩", "开始压缩", "1 张等待压缩"} {
		if !tt.HasText(text) {
			t.Errorf("待压缩状态缺少 %q：%q", text, tt.Texts())
		}
	}
}

func TestImageCompressManualBatchAndSingleStart(t *testing.T) {
	a := newTestApp()
	a.settings.ImageCompress.Engine = "tinypng"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &imageCompressSession{app: a, ctx: ctx, items: []*imageCompressItem{
		{ID: 1, SourcePath: "/tmp/a.png", Status: tinyPending},
		{ID: 2, SourcePath: "/tmp/b.png", Status: tinyPending},
	}}
	s.startOne(1)
	if s.items[0].Status != tinyError || s.items[1].Status != tinyPending {
		t.Fatalf("单张启动不应连带处理其他等待项：%+v", s.items)
	}
	s.startAll()
	if s.items[1].Status != tinyError || s.running {
		t.Fatalf("批量启动应处理剩余等待项，并在缺少 Key 时停止：%+v", s.items)
	}
}

func TestConfigMigrationAddsImageCompressDefaults(t *testing.T) {
	cfg := defaultConfig()
	cfg.SchemaVersion = 10
	cfg.Settings.SchemaVersion = 10
	cfg.Settings.ImageCompress = ImageCompressSettings{}
	migrateConfig(&cfg)
	got := cfg.Settings.ImageCompress
	if got.Engine != "local" || !got.Enabled || got.JPEGQuality != 80 || got.WebPQuality != 80 || got.PNGLevel != 3 || got.MaxEdge != 0 {
		t.Fatalf("v10→v13 默认值不正确：%+v", got)
	}
	if cfg.SchemaVersion != 13 {
		t.Fatalf("版本应升到 13，实际 %d", cfg.SchemaVersion)
	}
}

func TestConfigMigrationEnablesExistingImageCompressor(t *testing.T) {
	cfg := defaultConfig()
	cfg.SchemaVersion = 11
	cfg.Settings.SchemaVersion = 11
	cfg.Settings.ImageCompress.Enabled = false
	cfg.Settings.ImageCompress.Engine = "tinypng"
	migrateConfig(&cfg)
	if !cfg.Settings.ImageCompress.Enabled || cfg.Settings.ImageCompress.Engine != "tinypng" {
		t.Fatalf("v11→v12 应只补开关并保留已有参数：%+v", cfg.Settings.ImageCompress)
	}
}

func TestConfigMigrationMapsPNGOptimizeToLevel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		legacy  bool
		wantLvl int
	}{
		{"勾选过最佳无损压缩", true, 6},
		{"没勾选", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultConfig()
			cfg.SchemaVersion = 12
			cfg.Settings.SchemaVersion = 12
			legacy := tc.legacy
			cfg.Settings.ImageCompress.LegacyPNGOptimize = &legacy
			migrateConfig(&cfg)
			if cfg.Settings.ImageCompress.PNGLevel != tc.wantLvl {
				t.Fatalf("v12→v13 应映射成 %d，实际 %d", tc.wantLvl, cfg.Settings.ImageCompress.PNGLevel)
			}
			if cfg.Settings.ImageCompress.LegacyPNGOptimize != nil {
				t.Fatal("迁移后不应再保留旧勾选框字段")
			}
		})
	}
}

func TestImageCompressFeatureCardAndCommandSwitch(t *testing.T) {
	a := newTestApp()
	a.settings.HasCompletedOnboarding = true
	tt := ui.NewTester(a.view, 1100, 1200)
	tt.Frame()
	for _, text := range []string{"图片压缩", "图片压缩开关", "展开或收起 图片压缩"} {
		if !tt.HasText(text) {
			t.Errorf("功能中心缺少 %q：%q", text, tt.Texts())
		}
	}

	s := &launcherSession{app: a, query: "tiny"}
	if got := s.launcherActions(); len(got) != 1 || got[0].command != cmdTiny {
		t.Fatalf("开启时应显示 tiny 命令：%+v", got)
	}
	a.settings.ImageCompress.Enabled = false
	if got := s.launcherActions(); len(got) != 0 {
		t.Fatalf("关闭后应隐藏 tiny 命令：%+v", got)
	}
}
