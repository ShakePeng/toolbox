package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	if cfg.Settings.Hotkeys.Translate == "" {
		cfg.Settings.Hotkeys.Translate = "Alt+D"
	}
	if cfg.Settings.Hotkeys.Screenshot == "" {
		cfg.Settings.Hotkeys.Screenshot = "F1"
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
		p.Configured = providerConfigured(*p)
	}
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
