package main

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// newTestApp 构造一个已加载默认配置、开启两个插件的 app，用于无窗口测试。
func newTestApp() *app {
	a := newApp()
	cfg := defaultConfig()
	cfg.Settings.TranslationEnabled = true
	cfg.Settings.ScreenshotEnabled = true
	a.settings = cfg.Settings
	a.providers = cfg.Providers
	a.langSel = langLabel(a.settings.TargetLang)
	a.loading = false
	return a
}

// 侧栏导航可在概览与设置之间切换，并显示对应内容。
func TestNavigation(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 980, 680)

	if !tt.HasText("插件概览") {
		t.Fatalf("首屏应显示概览，实际文本：%q", tt.Texts())
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatalf("点击“设置”导航失败：%v", err)
	}
	if !tt.HasText("开机自动启动") {
		t.Errorf("设置页应显示“开机自动启动”，实际文本：%q", tt.Texts())
	}
	if tt.HasText("保存设置") {
		t.Error("设置页不应再显示“保存设置”按钮")
	}
	if err := tt.Click("概览"); err != nil {
		t.Fatalf("点击“概览”导航失败：%v", err)
	}
	if !tt.HasText("插件概览") {
		t.Errorf("应回到概览页，实际文本：%q", tt.Texts())
	}
}

func TestManualSaveButtonsRemoved(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 980, 680)

	if err := tt.Click("划词翻译"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("保存翻译设置") {
		t.Error("翻译页不应再显示“保存翻译设置”按钮")
	}
	if err := tt.Click("截图标注"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("保存截图设置") {
		t.Error("截图页不应再显示“保存截图设置”按钮")
	}
}

func TestLanguageSelectionPersists(t *testing.T) {
	a := newTestApp()
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	tt := ui.NewTester(a.view, 980, 680)

	if err := tt.Click("划词翻译"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("自动判断"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("英语"); err != nil {
		t.Fatalf("%v；当前文本：%q", err, tt.Texts())
	}
	if a.settings.TargetLang != "en" {
		t.Fatalf("目标语言没有切换为英语：%q", a.settings.TargetLang)
	}

	cfg, err := loadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Settings.TargetLang != "en" {
		t.Fatalf("目标语言没有自动保存：%q", cfg.Settings.TargetLang)
	}
}

// 托盘菜单跟随最新快捷键，重新录制后无需重启应用。
func TestRefreshTrayShortcuts(t *testing.T) {
	a := newTestApp()
	translate := &mygo.MenuItem{ID: "tray-translate", Label: "划词翻译"}
	capture := &mygo.MenuItem{ID: "tray-capture", Label: "区域截图"}
	launcher := &mygo.MenuItem{ID: "tray-launcher", Label: "快速启动"}
	a.trayItems = []*mygo.MenuItem{translate, capture, launcher}
	a.settings.Hotkeys.Translate = "Cmd+Alt+D"
	a.settings.Hotkeys.Screenshot = "F1"
	a.settings.Hotkeys.Launcher = ""

	a.refreshTrayShortcuts()
	if translate.Accelerator != "Cmd+Alt+D" || capture.Accelerator != "F1" {
		t.Fatalf("托盘快捷键未同步：%q / %q", translate.Accelerator, capture.Accelerator)
	}
	if launcher.Accelerator != "" {
		t.Fatalf("未设置的启动器快捷键应为空，实际 %q", launcher.Accelerator)
	}

	a.settings.Hotkeys.Screenshot = "Ctrl+Alt+S"
	a.settings.Hotkeys.Launcher = "Ctrl+Alt+Space"
	a.refreshTrayShortcuts()
	if capture.Accelerator != "Ctrl+Alt+S" {
		t.Fatalf("托盘快捷键未更新：%q", capture.Accelerator)
	}
	if launcher.Accelerator != "Ctrl+Alt+Space" {
		t.Fatalf("启动器托盘快捷键未更新：%q", launcher.Accelerator)
	}
}

// 翻译页展示 Provider 卡片，点击“配置”打开编辑弹窗并显示对应字段。
func TestProviderModal(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 980, 680)

	if err := tt.Click("划词翻译"); err != nil {
		t.Fatalf("点击“翻译”导航失败：%v", err)
	}
	if !tt.HasText("百度翻译") {
		t.Fatalf("翻译页应显示 Provider 卡片，实际文本：%q", tt.Texts())
	}
	if err := tt.Click("百度翻译配置"); err != nil {
		t.Fatalf("点击“配置”失败：%v", err)
	}
	if !tt.HasText("App ID") {
		t.Errorf("百度 Provider 弹窗应显示 App ID 字段，实际文本：%q", tt.Texts())
	}
	if !tt.HasText("打开百度翻译接入指南") {
		t.Errorf("百度 Provider 弹窗应显示申请指引：%q", tt.Texts())
	}

	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Google 翻译配置"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("需要：Google Cloud API Key") || !tt.HasText("打开 Google Cloud 凭据页") {
		t.Errorf("Google Provider 弹窗应显示 API Key 申请指引：%q", tt.Texts())
	}
}

// 快捷键格式化：macOS 用符号，其他平台保留原样。
func TestFormatHotkey(t *testing.T) {
	got := formatHotkey("Alt+D")
	if runtime.GOOS == "darwin" {
		if got != "⌥D" {
			t.Errorf("macOS 下 Alt+D 应显示为 ⌥D，实际 %q", got)
		}
	} else if got != "Alt+D" {
		t.Errorf("非 macOS 下应保留 Alt+D，实际 %q", got)
	}
	if formatHotkey("") != "未设置" {
		t.Errorf("空快捷键应显示“未设置”")
	}
}

// 录制快捷键时，非 macOS 的 Ctrl 不能被格式化成 Cmd+Ctrl：
// mygo 里 ui.Cmd 在 Windows/Linux 上就是 Ctrl。
func TestModPrefixesCtrl(t *testing.T) {
	if got := strings.Join(modPrefixes(ui.Ctrl|ui.Alt), "+"); got != "Ctrl+Alt" {
		t.Errorf("Ctrl+Alt 应格式化为 Ctrl+Alt，实际 %q", got)
	}
	if runtime.GOOS == "darwin" {
		if got := strings.Join(modPrefixes(ui.Cmd|ui.Shift), "+"); got != "Cmd+Shift" {
			t.Errorf("macOS 下 Cmd+Shift 应格式化为 Cmd+Shift，实际 %q", got)
		}
	}
}

// 旧版录制器在 Windows/Linux 上会多写一个 Cmd（被当成 Win 键），
// 升级时要去掉；macOS 的 Cmd 是正常修饰键，不能动。
func TestMigrateLegacyHotkeys(t *testing.T) {
	cfg := defaultConfig()
	cfg.Settings.Hotkeys.Translate = "Cmd+Ctrl+Alt+A"
	cfg.Settings.Hotkeys.Screenshot = "CmdOrCtrl+Shift+S"
	migrateConfig(&cfg)

	if runtime.GOOS == "darwin" {
		if cfg.Settings.Hotkeys.Translate != "Cmd+Ctrl+Alt+A" {
			t.Errorf("macOS 不应改写 Cmd，实际 %q", cfg.Settings.Hotkeys.Translate)
		}
		return
	}
	if got := cfg.Settings.Hotkeys.Translate; got != "Ctrl+Alt+A" {
		t.Errorf("旧快捷键应迁移为 Ctrl+Alt+A，实际 %q", got)
	}
	// CmdOrCtrl 是合法写法，不能被当成多余的 Cmd 删掉。
	if got := cfg.Settings.Hotkeys.Screenshot; got != "CmdOrCtrl+Shift+S" {
		t.Errorf("CmdOrCtrl 不应被改写，实际 %q", got)
	}
}

// 旧版只填 API Key 的 OpenAI 服务应迁移成 AI 供应商并关联到 AI 翻译。
func TestMigrateLegacyOpenAIProvider(t *testing.T) {
	if providerConfigured(Provider{Kind: "openai-compatible"}) {
		t.Fatal("没有选择供应商和模型的 AI 翻译不应判定为已配置")
	}
	if !providerConfigured(Provider{Kind: "openai-compatible", SupplierID: "s1", Model: "m"}) {
		t.Error("选好供应商和模型后应判定为已配置")
	}

	cfg := defaultConfig()
	cfg.SchemaVersion = 8
	cfg.Providers = []Provider{{
		ID:          "openai",
		Kind:        "openai-compatible",
		Credentials: map[string]string{"apiKey": "k"},
		Model:       "gpt-4o",
	}}
	migrateConfig(&cfg)
	p := cfg.Providers[0]
	if p.SupplierID == "" || p.BaseURL != "" || len(p.Credentials) != 0 {
		t.Errorf("应改为引用 AI 供应商并清掉旧地址/密钥：%+v", p)
	}
	if !p.Configured {
		t.Error("迁移后应判定为已配置")
	}
	if p.Name != "AI 翻译" {
		t.Errorf("旧版名称应改成 AI 翻译，实际 %q", p.Name)
	}
	var supplier AISupplier
	for _, s := range cfg.Settings.AISuppliers {
		if s.ID == p.SupplierID {
			supplier = s
		}
	}
	if supplier.BaseURL != defaultOpenAIBaseURL || supplier.APIKey != "k" ||
		len(supplier.Models) != 1 || supplier.Models[0] != "gpt-4o" {
		t.Errorf("供应商应继承旧地址、密钥和模型：%+v", supplier)
	}
}

// 目标语言显示名与代码可互转。
func TestLangMapping(t *testing.T) {
	for _, code := range []string{"auto", "zh-CN", "en", "ja"} {
		if langCode(langLabel(code)) != code {
			t.Errorf("语言代码往返不一致：%s", code)
		}
	}
	if langCode("不存在") != "auto" {
		t.Errorf("未知显示名应回退 auto")
	}
}

// 启动器默认关闭，快捷键留空，搜索源只默认开启应用搜索。
func TestLauncherDefaults(t *testing.T) {
	cfg := defaultConfig()
	if cfg.SchemaVersion != configSchemaVersion || cfg.Settings.SchemaVersion != configSchemaVersion {
		t.Errorf("默认配置应为 v%d，实际 %d / %d", configSchemaVersion, cfg.SchemaVersion, cfg.Settings.SchemaVersion)
	}
	if cfg.Settings.LauncherEnabled {
		t.Error("启动器应默认关闭")
	}
	if cfg.Settings.Hotkeys.Launcher != "" {
		t.Errorf("启动器快捷键应默认留空，实际 %q", cfg.Settings.Hotkeys.Launcher)
	}
	if !cfg.Settings.LauncherSearchApps {
		t.Error("应用搜索应默认开启")
	}
	if cfg.Settings.LauncherSearchFiles {
		t.Error("文件搜索应默认关闭")
	}
	if cfg.Settings.LauncherVSCodeEnabled {
		t.Error("VSCode 快速打开应默认关闭")
	}
	if cfg.Settings.LauncherVSCodeCommand != "code" {
		t.Errorf("VSCode 命令应默认是 code，实际 %q", cfg.Settings.LauncherVSCodeCommand)
	}
	if len(cfg.Settings.LauncherVSCodeFolders) != 0 {
		t.Errorf("项目文件夹应默认为空，实际 %v", cfg.Settings.LauncherVSCodeFolders)
	}
}

// v1 配置没有启动器字段：迁移补搜索源默认值，但不能动快捷键和插件开关。
func TestMigrateLauncherFromV1(t *testing.T) {
	cfg := defaultConfig()
	cfg.SchemaVersion = 1
	cfg.Settings.SchemaVersion = 1
	cfg.Settings.LauncherEnabled = false
	cfg.Settings.LauncherSearchApps = false
	cfg.Settings.LauncherSearchFiles = false
	cfg.Settings.Hotkeys.Launcher = ""

	migrateConfig(&cfg)

	if !cfg.Settings.LauncherSearchApps || !cfg.Settings.LauncherSearchFiles {
		t.Error("v1 迁移后搜索源应默认开启")
	}
	if cfg.Settings.LauncherEnabled {
		t.Error("v1 迁移不应改变启动器开关")
	}
	if cfg.Settings.Hotkeys.Launcher != "" {
		t.Errorf("迁移不应给启动器快捷键填默认值，实际 %q", cfg.Settings.Hotkeys.Launcher)
	}
	if cfg.Settings.LauncherVSCodeEnabled {
		t.Error("v1 迁移不应开启 VSCode 快速打开")
	}
	if cfg.Settings.LauncherVSCodeCommand != "code" {
		t.Errorf("v1 迁移后 VSCode 命令应为 code，实际 %q", cfg.Settings.LauncherVSCodeCommand)
	}
	if cfg.SchemaVersion != configSchemaVersion || cfg.Settings.SchemaVersion != configSchemaVersion {
		t.Errorf("迁移后版本应为 v%d，实际 %d / %d", configSchemaVersion, cfg.SchemaVersion, cfg.Settings.SchemaVersion)
	}
}

// v2 升级到 v3 只能补 VSCode 命令默认值，不能把 v2 用户显式关闭的搜索源重新打开。
func TestMigrateVSCodeFromV2(t *testing.T) {
	cfg := defaultConfig()
	cfg.SchemaVersion = 2
	cfg.Settings.SchemaVersion = 2
	cfg.Settings.LauncherSearchApps = false
	cfg.Settings.LauncherSearchFiles = true
	cfg.Settings.LauncherVSCodeCommand = ""

	migrateConfig(&cfg)

	if cfg.Settings.LauncherSearchApps {
		t.Error("v2 显式关闭的应用搜索不应被 v3 迁移打开")
	}
	if !cfg.Settings.LauncherSearchFiles {
		t.Error("v2 开启的文件搜索不应被 v3 迁移修改")
	}
	if cfg.Settings.LauncherVSCodeCommand != "code" {
		t.Errorf("v2 迁移后 VSCode 命令应为 code，实际 %q", cfg.Settings.LauncherVSCodeCommand)
	}
	if cfg.SchemaVersion != configSchemaVersion || cfg.Settings.SchemaVersion != configSchemaVersion {
		t.Errorf("迁移后版本应为 v%d，实际 %d / %d", configSchemaVersion, cfg.SchemaVersion, cfg.Settings.SchemaVersion)
	}
}

// v3 升级到 v4 只补 Fork 命令默认值，开关保持关闭，不改变用户已有的 VSCode 配置。
func TestMigrateForkFromV3(t *testing.T) {
	cfg := defaultConfig()
	cfg.SchemaVersion = 3
	cfg.Settings.SchemaVersion = 3
	cfg.Settings.LauncherVSCodeEnabled = true
	cfg.Settings.LauncherVSCodeCommand = "cursor"
	cfg.Settings.LauncherForkEnabled = false
	cfg.Settings.LauncherForkCommand = ""

	migrateConfig(&cfg)

	if cfg.Settings.LauncherForkCommand != "fork" {
		t.Errorf("v3 迁移后 Fork 命令应为 fork，实际 %q", cfg.Settings.LauncherForkCommand)
	}
	if cfg.Settings.LauncherForkEnabled {
		t.Error("v3 迁移不应开启 Fork 快速打开")
	}
	if !cfg.Settings.LauncherVSCodeEnabled || cfg.Settings.LauncherVSCodeCommand != "cursor" {
		t.Errorf("v3 迁移不应改动 VSCode 配置：enabled=%v command=%q",
			cfg.Settings.LauncherVSCodeEnabled, cfg.Settings.LauncherVSCodeCommand)
	}
}

// 用户显式关闭搜索源后，磁盘已是当前版本，迁移不能再改回 true。
func TestMigrateLauncherKeepsExplicitOff(t *testing.T) {
	cfg := defaultConfig()
	cfg.SchemaVersion = configSchemaVersion
	cfg.Settings.SchemaVersion = configSchemaVersion
	cfg.Settings.LauncherSearchApps = false
	cfg.Settings.LauncherSearchFiles = true

	migrateConfig(&cfg)

	if cfg.Settings.LauncherSearchApps {
		t.Error("v2 配置中显式关闭的应用搜索不应被迁移打开")
	}
	if !cfg.Settings.LauncherSearchFiles {
		t.Error("v2 配置中开启的文件搜索不应被迁移修改")
	}
}

func TestPersistWritesLauncherCurrentSchema(t *testing.T) {
	a := newTestApp()
	a.configPath = filepath.Join(t.TempDir(), "config.json")
	a.settings.LauncherSearchApps = false
	a.settings.LauncherSearchFiles = true

	if err := a.persist(); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != configSchemaVersion {
		t.Errorf("落盘版本应为 v%d，实际 %d", configSchemaVersion, cfg.SchemaVersion)
	}
	if cfg.Settings.LauncherSearchApps {
		t.Error("显式关闭的应用搜索不应在重启后被迁移打开")
	}
	if !cfg.Settings.LauncherSearchFiles {
		t.Error("文件搜索保持开启")
	}
}

// 空快捷键不能注册；ensureHotkey 在碰 GlobalShortcut 之前就应返回。
func TestLauncherEmptyHotkeyNotRegistered(t *testing.T) {
	a := newTestApp()
	if err := a.ensureHotkey("launcher", true, "", func() {}); err != nil {
		t.Fatalf("空快捷键不应返回错误：%v", err)
	}
	if _, ok := a.registered["launcher"]; ok {
		t.Error("空快捷键不应被记录为已注册")
	}
}

func TestFindHotkeyConflict(t *testing.T) {
	a := newTestApp()
	a.settings.Hotkeys.Translate = "Alt+D"
	a.settings.Hotkeys.Screenshot = ""
	a.settings.Hotkeys.Launcher = "Alt+D"

	other, ok := a.findHotkeyConflict("launcher")
	if !ok || other != "划词翻译" {
		t.Fatalf("启动器与翻译重复时应报告“划词翻译”，实际 %q / %v", other, ok)
	}
	a.settings.Hotkeys.Launcher = ""
	if _, ok := a.findHotkeyConflict("launcher"); ok {
		t.Error("空快捷键不应参与冲突检测")
	}
}
