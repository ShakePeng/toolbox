package main

import (
	"runtime"
	"strings"
)

// 本文件对齐原项目（S-Tools）的 Rust 后端（src-tauri/src/models.rs）数据模型，
// 字段命名沿用 JSON 的 camelCase，方便日后直接迁移旧用户配置。

// configSchemaVersion 是落盘配置的版本。迁移必须按版本逐级判断，
// 不能只看“小于当前版本”，否则升级到 v3 会把 v2 用户的搜索源选择覆盖掉。
const configSchemaVersion = 9

// HotkeySettings 是各插件绑定的全局快捷键（accelerator 字符串）。
type HotkeySettings struct {
	Translate  string `json:"translate"`
	Screenshot string `json:"screenshot"`
	// Launcher 默认留空：启动器不预设组合键，必须由用户手动录制。
	Launcher string `json:"launcher"`
}

// Settings 是应用级设置，对应原 AppSettings。
type Settings struct {
	SchemaVersion       int            `json:"schemaVersion"`
	Hotkeys             HotkeySettings `json:"hotkeys"`
	TranslationEnabled  bool           `json:"translationEnabled"`
	ScreenshotEnabled   bool           `json:"screenshotEnabled"`
	LauncherEnabled     bool           `json:"launcherEnabled"`
	LauncherSearchApps  bool           `json:"launcherSearchApps"`
	LauncherSearchFiles bool           `json:"launcherSearchFiles"`
	// LauncherVSCode 系列字段属于启动器的自定义命令。默认关闭，
	// 命令留空时按 code 处理，项目文件夹可以配置多个。
	LauncherVSCodeEnabled bool     `json:"launcherVSCodeEnabled"`
	LauncherVSCodeCommand string   `json:"launcherVSCodeCommand"`
	LauncherVSCodeFolders []string `json:"launcherVSCodeFolders"`
	// LauncherFork 是 Fork（Git 客户端）的同款配置，项目文件夹独立一份，
	// 只列出 Git 仓库。命令留空时按 fork 处理。
	LauncherForkEnabled bool     `json:"launcherForkEnabled"`
	LauncherForkCommand string   `json:"launcherForkCommand"`
	LauncherForkFolders []string `json:"launcherForkFolders"`
	// LauncherTrae 是 Trae（AI IDE）的同款配置，命令留空时按 trae 处理。
	LauncherTraeEnabled bool     `json:"launcherTraeEnabled"`
	LauncherTraeCommand string   `json:"launcherTraeCommand"`
	LauncherTraeFolders []string `json:"launcherTraeFolders"`
	// LauncherWebSearch 是启动器的网页搜索源。每个引擎配置触发词和含 %s
	// 的搜索地址，可以添加多个；默认关闭。
	LauncherWebSearchEnabled bool                   `json:"launcherWebSearchEnabled"`
	LauncherWebSearchEngines []LauncherSearchEngine `json:"launcherWebSearchEngines"`
	// AISuppliers 是独立于翻译插件的 AI 供应商配置，目前只做管理。
	// 每个供应商是 OpenAI 兼容服务，需要 Base URL、API Key 和模型列表。
	AISuppliers            []AISupplier `json:"aiSuppliers"`
	TargetLang             string       `json:"targetLang"`
	LaunchAtLogin          bool         `json:"launchAtLogin"`
	HasCompletedOnboarding bool         `json:"hasCompletedOnboarding"`
}

// LauncherSearchEngine 是一个网页搜索源。URL 用 %s 表示查询词占位符。
type LauncherSearchEngine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
	URL     string `json:"url"`
}

// AISupplier 是一个 OpenAI 兼容的 AI 供应商。API Key 和翻译凭据一样
// 目前明文保存在 config.json。
type AISupplier struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	BaseURL string   `json:"baseUrl"`
	APIKey  string   `json:"apiKey"`
	Models  []string `json:"models"`
	Enabled bool     `json:"enabled"`
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
	// SupplierID 是 AI 翻译选择的 AI 供应商，地址和密钥在请求前从
	// AISuppliers 解析，不再冗余存在 Provider 里。
	SupplierID string `json:"supplierId"`
}

// Config 是落盘的完整配置，对应原 AppConfig。
type Config struct {
	SchemaVersion int        `json:"schemaVersion"`
	Settings      Settings   `json:"settings"`
	Providers     []Provider `json:"providers"`
}

// defaultOpenAIBaseURL 是内置 OpenAI 服务的默认地址。
// 用户只填 API Key 时也能直接用；自建/兼容服务可以改成自己的地址。
const defaultOpenAIBaseURL = "https://api.openai.com/v1"

// defaultHotkeys 按平台给出默认快捷键。
// Windows/Linux 上 F1 和 Alt+D 是系统/浏览器常用键，注册成全局快捷键会
// 抢走其他程序的输入，所以换成冲突更少的组合；macOS 保持原有默认值。
func defaultHotkeys() HotkeySettings {
	if runtime.GOOS == "darwin" {
		return HotkeySettings{Translate: "Alt+D", Screenshot: "F1"}
	}
	return HotkeySettings{Translate: "Ctrl+Alt+D", Screenshot: "Ctrl+Alt+S"}
}

func defaultSettings() Settings {
	return Settings{
		SchemaVersion:            configSchemaVersion,
		Hotkeys:                  defaultHotkeys(),
		TranslationEnabled:       false,
		ScreenshotEnabled:        false,
		LauncherEnabled:          false,
		LauncherSearchApps:       true,
		LauncherSearchFiles:      false,
		LauncherVSCodeEnabled:    false,
		LauncherVSCodeCommand:    "code",
		LauncherVSCodeFolders:    nil,
		LauncherForkEnabled:      false,
		LauncherForkCommand:      "fork",
		LauncherForkFolders:      nil,
		LauncherTraeEnabled:      false,
		LauncherTraeCommand:      "trae",
		LauncherTraeFolders:      nil,
		LauncherWebSearchEnabled: false,
		LauncherWebSearchEngines: nil,
		TargetLang:               "auto",
		LaunchAtLogin:            false,
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
			Name:        "AI 翻译",
			Kind:        "openai-compatible",
			Description: "选择 AI 供应商中已启用的模型进行翻译。",
			Credentials: map[string]string{},
		},
	}
}

func defaultConfig() Config {
	return Config{
		SchemaVersion: configSchemaVersion,
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
	// AI 翻译的地址和密钥来自 AI 供应商，只需要选好供应商和模型。
	if p.Kind == "openai-compatible" {
		return strings.TrimSpace(p.SupplierID) != "" && strings.TrimSpace(p.Model) != ""
	}
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
