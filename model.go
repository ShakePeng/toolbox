package main

// 本文件对齐原项目（S-Tools）的 Rust 后端（src-tauri/src/models.rs）数据模型，
// 字段命名沿用 JSON 的 camelCase，方便日后直接迁移旧用户配置。

// HotkeySettings 是两个插件各自绑定的全局快捷键（accelerator 字符串）。
type HotkeySettings struct {
	Translate  string `json:"translate"`
	Screenshot string `json:"screenshot"`
}

// Settings 是应用级设置，对应原 AppSettings。
type Settings struct {
	SchemaVersion          int            `json:"schemaVersion"`
	Hotkeys                HotkeySettings `json:"hotkeys"`
	TranslationEnabled     bool           `json:"translationEnabled"`
	ScreenshotEnabled      bool           `json:"screenshotEnabled"`
	TargetLang             string         `json:"targetLang"`
	LaunchAtLogin          bool           `json:"launchAtLogin"`
	HasCompletedOnboarding bool           `json:"hasCompletedOnboarding"`
}

// Provider 是一个翻译服务配置，对应原 ProviderConfig / ProviderView。
// 凭据暂时写在本地 config.json（原版用系统钥匙串，后续阶段再迁移到 mygo.App 的 secrets）。
type Provider struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Enabled        bool              `json:"enabled"`
	Configured     bool              `json:"configured"`
	Model          string            `json:"model"`
	BaseURL        string            `json:"baseUrl"`
	Description    string            `json:"description"`
	Credentials    map[string]string `json:"credentials"`
	UseSystemProxy bool              `json:"useSystemProxy"`
}

// Config 是落盘的完整配置，对应原 AppConfig。
type Config struct {
	SchemaVersion int        `json:"schemaVersion"`
	Settings      Settings   `json:"settings"`
	Providers     []Provider `json:"providers"`
}

func defaultSettings() Settings {
	return Settings{
		SchemaVersion: 1,
		Hotkeys: HotkeySettings{
			Translate:  "Alt+D",
			Screenshot: "F1",
		},
		TranslationEnabled: false,
		ScreenshotEnabled:  false,
		TargetLang:         "auto",
		LaunchAtLogin:      false,
	}
}

func defaultProviders() []Provider {
	return []Provider{
		{
			ID:          "baidu",
			Name:        "百度翻译",
			Kind:        "baidu",
			Description: "百度翻译开放平台，适合中文语境。",
			Credentials: map[string]string{},
		},
		{
			ID:          "google",
			Name:        "Google 翻译",
			Kind:        "google",
			Description: "Google Cloud Translation，支持按需启用系统代理。",
			Credentials: map[string]string{},
		},
		{
			ID:          "openai",
			Name:        "OpenAI",
			Kind:        "openai-compatible",
			Description: "接入任意兼容 Chat Completions 的服务。",
			Credentials: map[string]string{},
		},
	}
}

func defaultConfig() Config {
	return Config{
		SchemaVersion: 1,
		Settings:      defaultSettings(),
		Providers:     defaultProviders(),
	}
}

// requiredCredentialKeys 返回某类服务判定“已配置”所需的凭据字段。
func requiredCredentialKeys(kind string) []string {
	switch kind {
	case "baidu":
		return []string{"appId", "secret"}
	case "google":
		return []string{"apiKey"}
	case "openai-compatible":
		return []string{"apiKey"}
	}
	return nil
}

// providerConfigured 判断凭据是否填写完整，等价于原版 configured 字段。
func providerConfigured(p Provider) bool {
	keys := requiredCredentialKeys(p.Kind)
	if len(keys) == 0 {
		return false
	}
	for _, k := range keys {
		if p.Credentials[k] == "" {
			return false
		}
	}
	return true
}

// targetLangOptions 是目标语言的显示名到语言代码的映射，顺序即下拉顺序。
var targetLangOptions = []struct {
	Label string
	Code  string
}{
	{"自动判断", "auto"},
	{"简体中文", "zh-CN"},
	{"繁体中文", "zh-TW"},
	{"英语", "en"},
	{"日语", "ja"},
	{"韩语", "ko"},
}

func langLabel(code string) string {
	for _, o := range targetLangOptions {
		if o.Code == code {
			return o.Label
		}
	}
	return targetLangOptions[0].Label
}

func langCode(label string) string {
	for _, o := range targetLangOptions {
		if o.Label == label {
			return o.Code
		}
	}
	return "auto"
}
