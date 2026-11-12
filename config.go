package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
)

// configPath 返回本地配置文件路径。放在 mygo 的 userData 目录下，
// 与原版 Tauri 的 app_data_dir/config.json 语义一致。
func configPath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// loadConfig 读取配置；文件不存在时写入一份默认配置并返回。
// 解析失败时回退默认值而不是崩溃，避免旧/损坏配置卡死启动。
func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := defaultConfig()
		return cfg, saveConfig(path, cfg)
	}
	if err != nil {
		return defaultConfig(), err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), fmt.Errorf("解析配置失败：%w", err)
	}
	migrateConfig(&cfg)
	return cfg, nil
}

// migrateConfig 兜底补齐缺失字段并剔除已下线的服务，语义对齐原 migrate_provider_status。
func migrateConfig(cfg *Config) {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = 1
	}
	// v1 没有启动器的搜索源字段。bool 零值无法区分“字段缺失”和“用户关闭”，
	// 所以只在 v1 配置上补默认值；用户关闭后 persist 会写成新版本，不再被覆盖。
	if cfg.SchemaVersion < 2 {
		cfg.Settings.LauncherSearchApps = true
		cfg.Settings.LauncherSearchFiles = true
	}
	// v2 没有 VSCode 自定义命令字段；命令留空时按 code 处理。
	if cfg.SchemaVersion < 3 {
		if strings.TrimSpace(cfg.Settings.LauncherVSCodeCommand) == "" {
			cfg.Settings.LauncherVSCodeCommand = "code"
		}
	}
	// v3 没有 Fork 配置；只补命令默认值，开关保持关闭，不覆盖用户后续的修改。
	if cfg.SchemaVersion < 4 {
		if strings.TrimSpace(cfg.Settings.LauncherForkCommand) == "" {
			cfg.Settings.LauncherForkCommand = "fork"
		}
	}
	// v4 没有 Trae 配置；同样只补命令默认值，开关保持关闭。
	if cfg.SchemaVersion < 5 {
		if strings.TrimSpace(cfg.Settings.LauncherTraeCommand) == "" {
			cfg.Settings.LauncherTraeCommand = "trae"
		}
	}
	// v5 没有网页搜索配置；新字段零值就是「未启用」，不需要补数据。
	// v6 没有 AI 供应商配置；列表零值即空，同样不需要补数据。
	// v7 的 AI 供应商带 defaultModel，v8 起改用模型多选，旧字段直接忽略。
	if len(cfg.Providers) == 0 {
		cfg.Providers = defaultProviders()
	}
	// v8 的 AI 翻译直接存 Base URL/API Key；v9 起改为引用 AI 供应商。
	// 必须在版本号统一改写之前判断。
	if cfg.SchemaVersion < 9 {
		migrateLegacyOpenAIProvider(cfg)
	}
	cfg.SchemaVersion = configSchemaVersion
	cfg.Settings.SchemaVersion = configSchemaVersion

	defaults := defaultHotkeys()
	if cfg.Settings.Hotkeys.Translate == "" {
		cfg.Settings.Hotkeys.Translate = defaults.Translate
	}
	if cfg.Settings.Hotkeys.Screenshot == "" {
		cfg.Settings.Hotkeys.Screenshot = defaults.Screenshot
	}
	if runtime.GOOS != "darwin" {
		// 旧版录制器在 Windows/Linux 上会多写一个 Cmd（= Win 键），
		// 注册出来的组合键用户按不到，这里迁移掉。
		cfg.Settings.Hotkeys.Translate = stripLegacyCmd(cfg.Settings.Hotkeys.Translate)
		cfg.Settings.Hotkeys.Screenshot = stripLegacyCmd(cfg.Settings.Hotkeys.Screenshot)
	}
	if cfg.Settings.TargetLang == "" {
		cfg.Settings.TargetLang = "auto"
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.Credentials == nil {
			p.Credentials = map[string]string{}
		}
		p.Configured = providerConfigured(*p)
	}
}

// migrateLegacyOpenAIProvider 把旧版 AI 翻译里直接保存的地址和密钥迁到
// 一个 AI 供应商，并让翻译引用它。已经是新配置的 Provider 会跳过。
func migrateLegacyOpenAIProvider(cfg *Config) {
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.Kind != "openai-compatible" {
			continue
		}
		if p.Name == "" || p.Name == "OpenAI" {
			p.Name = "AI 翻译"
			p.Description = "选择 AI 供应商中已启用的模型进行翻译。"
		}
		if strings.TrimSpace(p.SupplierID) != "" {
			continue
		}
		apiKey := strings.TrimSpace(p.Credentials["apiKey"])
		model := strings.TrimSpace(p.Model)
		// 早期版本 Base URL 留空时按官方地址处理。
		base := strings.TrimSpace(p.BaseURL)
		if base == "" {
			base = defaultOpenAIBaseURL
		}
		// 只有真正填过凭据或模型才迁移；旧版默认的官方地址不算配置痕迹。
		if apiKey == "" && model == "" {
			continue
		}

		id := ""
		for _, supplier := range cfg.Settings.AISuppliers {
			if strings.EqualFold(strings.TrimRight(supplier.BaseURL, "/"), strings.TrimRight(base, "/")) {
				id = supplier.ID
				break
			}
		}
		if id == "" {
			id = "ai-legacy-openai"
			supplier := AISupplier{
				ID: id, Name: "原有配置", BaseURL: base, APIKey: apiKey, Enabled: true,
			}
			if model != "" {
				supplier.Models = []string{model}
			}
			cfg.Settings.AISuppliers = append(cfg.Settings.AISuppliers, supplier)
		}
		if model == "" {
			for _, supplier := range cfg.Settings.AISuppliers {
				if supplier.ID == id && len(supplier.Models) > 0 {
					model = supplier.Models[0]
					break
				}
			}
		}
		if model == "" {
			model = "gpt-4.1-mini"
			for j := range cfg.Settings.AISuppliers {
				if cfg.Settings.AISuppliers[j].ID == id {
					cfg.Settings.AISuppliers[j].Models = append(cfg.Settings.AISuppliers[j].Models, model)
				}
			}
		}
		p.SupplierID = id
		p.Model = model
		// 地址和密钥改由供应商提供，清掉 Provider 里的旧副本。
		p.BaseURL = ""
		p.Credentials = map[string]string{}
	}
}

// stripLegacyCmd 去掉旧版录制器生成的 Cmd 修饰键。
// 只匹配完整的 "Cmd"，不会动 "CmdOrCtrl"。
func stripLegacyCmd(acc string) string {
	parts := strings.Split(acc, "+")
	if len(parts) < 2 {
		return acc
	}
	out := parts[:0]
	for _, p := range parts {
		if strings.EqualFold(strings.TrimSpace(p), "Cmd") {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return acc
	}
	return strings.Join(out, "+")
}

// saveConfig 原子写入配置：先写临时文件再 rename，避免半截文件。
func saveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败：%w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败：%w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配置失败：%w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("提交配置失败：%w", err)
	}
	return nil
}
