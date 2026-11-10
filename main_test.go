package main

import (
	"runtime"
	"strings"
	"testing"

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
	if err := tt.Click("概览"); err != nil {
		t.Fatalf("点击“概览”导航失败：%v", err)
	}
	if !tt.HasText("插件概览") {
		t.Errorf("应回到概览页，实际文本：%q", tt.Texts())
	}
}

// 翻译页展示 Provider 卡片，点击“配置”打开编辑弹窗并显示对应字段。
func TestProviderModal(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 980, 680)

	if err := tt.Click("翻译"); err != nil {
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

// 只填 API Key 的 OpenAI 服务必须补上默认 Base URL，且判定为已配置。
func TestMigrateOpenAIBaseURL(t *testing.T) {
	if providerConfigured(Provider{
		Kind:        "openai-compatible",
		Credentials: map[string]string{"apiKey": "k"},
	}) {
		t.Fatal("没有 Base URL 的 OpenAI 服务不应判定为已配置")
	}

	cfg := defaultConfig()
	cfg.Providers = []Provider{{
		ID:          "openai",
		Kind:        "openai-compatible",
		Credentials: map[string]string{"apiKey": "k"},
	}}
	migrateConfig(&cfg)
	p := cfg.Providers[0]
	if p.BaseURL != defaultOpenAIBaseURL {
		t.Errorf("Base URL 应补为 %q，实际 %q", defaultOpenAIBaseURL, p.BaseURL)
	}
	if !p.Configured {
		t.Error("补上 Base URL 后应判定为已配置")
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
