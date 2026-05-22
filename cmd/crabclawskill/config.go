package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CLIConfig CLI 配置
type CLIConfig struct {
	ServerURL string `json:"serverUrl"`
	SkillDir  string `json:"skillDir"`
}

func defaultConfig() CLIConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告: 无法获取用户主目录: %v, 使用当前目录\n", err)
		home = "."
	}
	return CLIConfig{
		ServerURL: "https://acosmi.com",
		// CrabClaw / CrabCode 默认 skills 目录,安装后被本地客户端自动加载
		SkillDir: filepath.Join(home, ".crabclaw", "skills"),
	}
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "警告: 无法获取用户主目录: %v, 使用当前目录\n", err)
		home = "."
	}
	return filepath.Join(home, ".acosmi", "cli-config.json")
}

func loadConfig() CLIConfig {
	cfg := defaultConfig()

	data, err := os.ReadFile(configPath())
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	// 环境变量覆盖
	if env := os.Getenv("ACOSMI_SERVER_URL"); env != "" {
		cfg.ServerURL = env
	}

	// 确保 SkillDir 有默认值
	if cfg.SkillDir == "" {
		cfg.SkillDir = defaultConfig().SkillDir
	}

	return cfg
}

func saveConfig(cfg CLIConfig) error {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath(), data, 0600)
}
