package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// tiny 不再是内置命令：触发词由 ImageCompressSettings.Command 配置，
// 匹配走 launcherActions 的图片压缩触发行，冲突校验不再把它算进内置命令。
func TestTinyTriggerWordMatching(t *testing.T) {
	a := newTestApp()
	a.settings.ImageCompress = defaultImageCompressSettings()
	if got := a.imageCompressCommand(); got != "tiny" {
		t.Errorf("默认触发词应为 tiny，实际 %q", got)
	}
	if isBuiltinCommand("tiny") {
		t.Error("tiny 已移出内置命令表，不应再被 isBuiltinCommand 命中")
	}
	s := &launcherSession{app: a, query: "ti"}
	actions := s.launcherActions()
	found := false
	for _, it := range actions {
		if it.action == launcherActionImageCompress && it.path == "launcher-action-image-compress" {
			found = true
		}
	}
	if !found {
		t.Errorf("输入 ti 应出现图片压缩触发行，实际 %+v", actions)
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
	if cfg.SchemaVersion != 14 {
		t.Fatalf("版本应升到 14，实际 %d", cfg.SchemaVersion)
	}
}

// v14 起图片压缩的触发词从内置命令变成用户配置，老配置补默认值 tiny。
func TestConfigMigrationAddsImageCompressCommand(t *testing.T) {
	cfg := defaultConfig()
	cfg.SchemaVersion = 13
	cfg.Settings.SchemaVersion = 13
	cfg.Settings.ImageCompress.Command = ""
	migrateConfig(&cfg)
	if cfg.Settings.ImageCompress.Command != "tiny" {
		t.Fatalf("v13→v14 应补默认触发词 tiny，实际 %q", cfg.Settings.ImageCompress.Command)
	}

	// v13 起版本统一升到当前值，但用户已改过的触发词不能被覆盖。
	cfg = defaultConfig()
	cfg.SchemaVersion = 14
	cfg.Settings.SchemaVersion = 14
	cfg.Settings.ImageCompress.Command = "img"
	migrateConfig(&cfg)
	if cfg.Settings.ImageCompress.Command != "img" {
		t.Fatalf("v14 用户自定义触发词不应被覆盖：%q", cfg.Settings.ImageCompress.Command)
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
	for _, text := range []string{"图片压缩", "图片压缩开关", "图片压缩设置"} {
		if !tt.HasText(text) {
			t.Errorf("功能中心缺少 %q：%q", text, tt.Texts())
		}
	}

	s := &launcherSession{app: a, query: "tiny"}
	got := s.launcherActions()
	hasTiny := len(got) == 1 && got[0].action == launcherActionImageCompress
	if !hasTiny {
		t.Fatalf("开启时应显示 tiny 触发行：%+v", got)
	}
	a.settings.ImageCompress.Enabled = false
	if got := s.launcherActions(); len(got) != 0 {
		t.Fatalf("关闭后应隐藏 tiny 触发行：%+v", got)
	}
}

// 功能卡片标题旁应外显唤起方式：快捷键 pill、触发词 pill；网页搜索
// 多引擎无单一触发词，不外显。快捷键文案按平台格式化后比较，
// 测试在 windows-latest 上也要过（非 macOS 不做符号替换）。
func TestHomeFeatureTriggerPills(t *testing.T) {
	a := newTestApp()
	a.settings.HasCompletedOnboarding = true
	a.settings.Hotkeys = defaultHotkeys()
	a.settings.LauncherVSCodeCommand = "code"
	a.settings.LauncherTraeCommand = "trae"
	a.settings.LauncherForkCommand = "fork"
	tt := ui.NewTester(a.view, 1100, 1500)
	tt.Frame()
	want := []string{
		"快捷键 " + formatHotkey(a.settings.Hotkeys.Translate),
		"快捷键 " + formatHotkey(a.settings.Hotkeys.Screenshot),
		"触发词 code", "触发词 trae", "触发词 fork",
	}
	for _, text := range want {
		if !tt.HasText(text) {
			t.Errorf("功能中心缺少外显 %q", text)
		}
	}
	// 图片压缩的触发词 pill 与其他功能同一形态（只读），编辑入口在设置弹框。
	if !tt.HasText("触发词 tiny") {
		t.Error("图片压缩卡片应有触发词 pill")
	}
}

// 改图片压缩触发词：正常保存生效；与内置命令/快速打开触发词冲突时
// 回退默认 tiny 并给出原因。
func TestImageCompressCommandConflict(t *testing.T) {
	a := newTestApp()
	a.settings.LauncherWebSearchEngines = []LauncherSearchEngine{{Name: "百度", Command: "bd", URL: "https://www.baidu.com/s?wd=%s"}}
	if reason := a.imageCompressCommandConflict("settings"); reason == "" {
		t.Error("与内置命令重名应返回冲突原因")
	}
	if reason := a.imageCompressCommandConflict("code"); reason == "" {
		t.Error("与 VS Code 触发词重名应返回冲突原因")
	}
	if reason := a.imageCompressCommandConflict("bd"); reason == "" {
		t.Error("与网页搜索触发词重名应返回冲突原因")
	}
	if reason := a.imageCompressCommandConflict("img"); reason != "" {
		t.Errorf("未冲突的触发词不应报错：%q", reason)
	}
}
