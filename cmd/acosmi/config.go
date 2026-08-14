package main

// =============================================================================
// CLI 本地状态: 配置 + manifest + 原子写
//
// 两份文件, 职责严格分开:
//   ~/.acosmi/cli-config.json  用户可编辑的 CLI 偏好 (服务器地址)
//   ~/.acosmi/agents.json      **CLI 自己拥有**的接入清单 —— remove 时据它精确回滚
//
// manifest 里**绝不存密钥明文**, 只存凭证 id (可据它调服务端吊销) 与我们改过哪个文件、
// 备份在哪。这样即便 manifest 泄露, 拿到的也只是"哪台机器接了哪个智能体"。
// =============================================================================

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CLIConfig 用户可编辑的 CLI 偏好。
type CLIConfig struct {
	ServerURL string `json:"serverUrl"`
}

const defaultServerURL = "https://acosmi.com"

func acosmiHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".acosmi"
	}
	return filepath.Join(home, ".acosmi")
}

func cliConfigPath() string { return filepath.Join(acosmiHome(), "cli-config.json") }
func manifestPath() string  { return filepath.Join(acosmiHome(), "agents.json") }

// loadCLIConfig 读偏好; 缺文件/坏文件一律回落默认值 —— CLI 不该因为一个坏配置就罢工。
// 环境变量 ACOSMI_SERVER_URL 优先于文件 (容器/CI 里常用)。
func loadCLIConfig() CLIConfig {
	cfg := CLIConfig{ServerURL: defaultServerURL}
	if raw, err := os.ReadFile(cliConfigPath()); err == nil {
		var parsed CLIConfig
		if json.Unmarshal(raw, &parsed) == nil && parsed.ServerURL != "" {
			cfg.ServerURL = parsed.ServerURL
		}
	}
	if v := os.Getenv("ACOSMI_SERVER_URL"); v != "" {
		cfg.ServerURL = v
	}
	return cfg
}

// =============================================================================
// manifest
// =============================================================================

// AgentEntry 一次 `agent add` 留下的全部可回滚痕迹。
type AgentEntry struct {
	// Target 目标智能体 (适配器 ID)。
	Target string `json:"target"`
	// CredentialID 服务端凭证 id —— remove 时据它吊销。**不存明文**。
	CredentialID string `json:"credentialId"`
	// KeyPrefix 展示用前缀 (如 sk-acm-AbCd), 便于用户在控制台核对是哪一枚。
	KeyPrefix string `json:"keyPrefix"`
	// ConfigPath 我们改过的目标配置文件。
	ConfigPath string `json:"configPath"`
	// BackupPath 改动前的完整备份 —— 出事时用户可以整份还原。
	BackupPath string `json:"backupPath,omitempty"`
	// PatchKeys 我们**新增或覆盖**过的顶层键路径 (点分), remove 时逐个撤销。
	PatchKeys []string `json:"patchKeys,omitempty"`
	// PreExisting 记录我们覆盖前那些键的原值 (JSON), 用于精确还原而非粗暴删除。
	PreExisting map[string]json.RawMessage `json:"preExisting,omitempty"`
	BaseURL     string                     `json:"baseUrl"`
	ModelID     string                     `json:"modelId,omitempty"`
	Protocol    string                     `json:"protocol"`
	ServerURL   string                     `json:"serverUrl"`
	CreatedAt   time.Time                  `json:"createdAt"`
}

// Manifest CLI-owned 接入清单。
type Manifest struct {
	Version int                    `json:"version"`
	Agents  map[string]*AgentEntry `json:"agents"`
}

func loadManifest() *Manifest {
	m := &Manifest{Version: 1, Agents: map[string]*AgentEntry{}}
	raw, err := os.ReadFile(manifestPath())
	if err != nil {
		return m
	}
	var parsed Manifest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// 坏 manifest 不覆盖、不删除 —— 它是用户唯一的回滚线索。让调用方看到空清单并报错更安全。
		return m
	}
	if parsed.Agents == nil {
		parsed.Agents = map[string]*AgentEntry{}
	}
	return &parsed
}

func saveManifest(m *Manifest) error {
	m.Version = 1
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	return atomicWriteFile(manifestPath(), raw, 0o600)
}

// =============================================================================
// 原子写 + 备份
// =============================================================================

// atomicWriteFile 写文件: 目录 0700 → 临时文件 (仅属主可读写) → fsync → rename。
//
// 为什么必须原子: 我们改的是用户智能体的**主配置**。写到一半断电/被 kill 就会留下一个
// 语法损坏的配置, 那个智能体从此起不来 —— 比"没配上"糟糕得多。rename 在同一文件系统上
// 是原子的, 读者要么看到旧内容要么看到新内容, 不存在中间态。
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".acosmi-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后这行是 no-op

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}

// backupFile 把现有文件复制到带时间戳的备份。文件不存在时返回空路径 (无需备份)。
func backupFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	backupDir := filepath.Join(acosmiHome(), "backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	name := fmt.Sprintf("%s.%s.bak", filepath.Base(path), time.Now().UTC().Format("20060102T150405Z"))
	dst := filepath.Join(backupDir, name)
	if err := atomicWriteFile(dst, raw, 0o600); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}
	return dst, nil
}
