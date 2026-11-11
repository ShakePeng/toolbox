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
	if len(cfg.Providers) == 0 {
		cfg.Providers = defaultProviders()
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		if p.Credentials == nil {
			p.Credentials = map[string]string{}
		}
		// 早期版本把 OpenAI 的 Base URL 留空，导致只填 API Key 的服务
		// 标记为“已配置”却无法请求；升级时补上默认地址。
		if p.Kind == "openai-compatible" && strings.TrimSpace(p.BaseURL) == "" {
			p.BaseURL = defaultOpenAIBaseURL
		}
		p.Configured = providerConfigured(*p)
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
