package main

// =============================================================================
// 目标智能体适配器 (§7.3)
//
//	Detect()   → 目标是否在本机、配置文件在哪
//	Plan()     → 将要改哪些键 (支持 --dry-run 原样打印)
//	Apply()    → 备份 → 合并 → 原子写
//	Rollback() → 只撤销自己加的补丁, 保留用户后来的修改
//
// 首批三个目标 (§14.4 冻结): crabcode / generic-openai / generic-anthropic。
//
// 合并策略的铁律: **保留未知字段**。目标配置是用户的资产, 里面可能有我们从没听说过的键;
// 我们只负责把自己那几个键放进去, 其余原样搬运。所有适配器都走同一个 mergeJSONConfig,
// 就是为了让这条铁律只有一处实现、不会被某个适配器悄悄破坏。
// =============================================================================

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AgentAdapter 一个目标智能体的接入方式。
type AgentAdapter interface {
	// ID 适配器标识 (CLI 参数里用的名字)。
	ID() string
	// DisplayName 人类可读名。
	DisplayName() string
	// Protocol "openai" 或 "anthropic" —— 决定给它配哪个 base_url 与哪套 wire。
	Protocol() string
	// ConfigPath 目标配置文件的绝对路径。
	ConfigPath() (string, error)
	// Detect 目标是否已在本机 (配置文件或其目录存在)。
	Detect() (bool, string)
	// Plan 返回将要写入的键值 (点分路径 → 值), 供 dry-run 展示与 Apply 复用。
	Plan(in AgentSetup) (map[string]any, error)
}

// AgentSetup 一次接入所需的全部输入。
type AgentSetup struct {
	BaseURL   string // 例如 https://acosmi.com/v1
	APIKey    string // 一次性明文 —— 只在内存里流转
	ModelID   string
	ServerURL string
}

// =============================================================================
// 通用 JSON 配置适配器 (crabcode / generic-*)
// =============================================================================

type jsonConfigAdapter struct {
	id          string
	displayName string
	protocol    string
	// pathFn 返回配置文件路径。
	pathFn func() (string, error)
	// planFn 生成将要写入的键值对。
	planFn func(in AgentSetup) map[string]any
}

func (a *jsonConfigAdapter) ID() string          { return a.id }
func (a *jsonConfigAdapter) DisplayName() string { return a.displayName }
func (a *jsonConfigAdapter) Protocol() string    { return a.protocol }

func (a *jsonConfigAdapter) ConfigPath() (string, error) { return a.pathFn() }

func (a *jsonConfigAdapter) Detect() (bool, string) {
	p, err := a.pathFn()
	if err != nil {
		return false, ""
	}
	if _, statErr := os.Stat(p); statErr == nil {
		return true, p
	}
	// 配置文件还不存在但目录在 → 目标已安装, 只是没配过。这仍然算"检测到"。
	if _, statErr := os.Stat(filepath.Dir(p)); statErr == nil {
		return true, p
	}
	return false, p
}

func (a *jsonConfigAdapter) Plan(in AgentSetup) (map[string]any, error) {
	if a.planFn == nil {
		return nil, fmt.Errorf("adapter %s has no plan", a.id)
	}
	return a.planFn(in), nil
}

// =============================================================================
// 首批适配器
// =============================================================================

func homeJoin(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// crabcodeAdapter OpenAcosmi / CrabCode —— 本仓自家客户端, 配置在 ~/.crabclaw/settings.json。
// 与 crabclaw-skill CLI 的技能目录 (~/.crabclaw/skills) 同一个家目录。
func crabcodeAdapter() AgentAdapter {
	return &jsonConfigAdapter{
		id: "crabcode", displayName: "CrabCode / OpenAcosmi", protocol: "anthropic",
		pathFn: func() (string, error) { return homeJoin(".crabclaw", "settings.json") },
		planFn: func(in AgentSetup) map[string]any {
			return map[string]any{
				"provider.acosmi.baseUrl":  in.BaseURL,
				"provider.acosmi.apiKey":   in.APIKey,
				"provider.acosmi.protocol": "anthropic",
				"provider.acosmi.model":    in.ModelID,
			}
		},
	}
}

// genericOpenAIAdapter 面向"任何读 OpenAI 环境变量风格 JSON 配置"的客户端。
// 落在 ~/.acosmi/agents/generic-openai.json —— 我们不猜第三方的配置位置,
// 而是产出一份标准文件让用户 include/引用。猜错别人的配置路径是不可接受的破坏。
func genericOpenAIAdapter() AgentAdapter {
	return &jsonConfigAdapter{
		id: "generic-openai", displayName: "通用 OpenAI 兼容客户端", protocol: "openai",
		pathFn: func() (string, error) { return homeJoin(".acosmi", "agents", "generic-openai.json") },
		planFn: func(in AgentSetup) map[string]any {
			return map[string]any{
				"OPENAI_BASE_URL": in.BaseURL,
				"OPENAI_API_KEY":  in.APIKey,
				"OPENAI_MODEL":    in.ModelID,
			}
		},
	}
}

// genericAnthropicAdapter 同上, Anthropic wire。
func genericAnthropicAdapter() AgentAdapter {
	return &jsonConfigAdapter{
		id: "generic-anthropic", displayName: "通用 Anthropic 兼容客户端", protocol: "anthropic",
		pathFn: func() (string, error) { return homeJoin(".acosmi", "agents", "generic-anthropic.json") },
		planFn: func(in AgentSetup) map[string]any {
			return map[string]any{
				"ANTHROPIC_BASE_URL": in.BaseURL,
				"ANTHROPIC_API_KEY":  in.APIKey,
				"ANTHROPIC_MODEL":    in.ModelID,
			}
		},
	}
}

// adapters 首批目标 (§14.4)。新增目标只需在此登记 —— 其余流程完全通用。
func adapters() map[string]AgentAdapter {
	list := []AgentAdapter{crabcodeAdapter(), genericOpenAIAdapter(), genericAnthropicAdapter()}
	out := make(map[string]AgentAdapter, len(list))
	for _, a := range list {
		out[a.ID()] = a
	}
	return out
}

func lookupAdapter(id string) (AgentAdapter, error) {
	all := adapters()
	if a, ok := all[id]; ok {
		return a, nil
	}
	names := make([]string, 0, len(all))
	for k := range all {
		names = append(names, k)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("未知目标 %q; 可用目标: %s", id, strings.Join(names, ", "))
}

// =============================================================================
// 合并 / 回滚 —— 唯一实现点
// =============================================================================

// readJSONObject 读一个 JSON 对象; 文件不存在 → 空对象。
// 文件存在但不是合法 JSON → **报错**, 绝不覆盖: 那可能是用户手写的配置,
// 我们没有权利把它当垃圾丢掉。
func readJSONObject(path string) (map[string]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%s 不是合法的 JSON 对象, 已中止 (不会覆盖你的文件): %w", path, err)
	}
	return obj, nil
}

// setDotted 把点分路径写进嵌套 map, 沿途缺失的层级自动补建。
// 返回被覆盖的旧值 (若存在) —— 供 rollback 精确还原。
func setDotted(obj map[string]json.RawMessage, dotted string, value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal value for %s: %w", dotted, err)
	}
	parts := strings.Split(dotted, ".")
	if len(parts) == 1 {
		old := obj[parts[0]]
		obj[parts[0]] = encoded
		return old, nil
	}

	// 逐层下钻: 把中间层解成 map, 改完再逐层编回去。
	head, rest := parts[0], strings.Join(parts[1:], ".")
	child := map[string]json.RawMessage{}
	if existing, ok := obj[head]; ok && len(existing) > 0 {
		if err := json.Unmarshal(existing, &child); err != nil {
			return nil, fmt.Errorf("配置中的 %q 不是对象, 无法写入 %s (已中止, 不覆盖)", head, dotted)
		}
	}
	old, err := setDotted(child, rest, value)
	if err != nil {
		return nil, err
	}
	reencoded, err := json.Marshal(child)
	if err != nil {
		return nil, fmt.Errorf("re-encode %s: %w", head, err)
	}
	obj[head] = reencoded
	return old, nil
}

// deleteDotted 删除点分路径; 若删除后中间层变成空对象也一并删掉 (不留我们造出来的空壳)。
func deleteDotted(obj map[string]json.RawMessage, dotted string) {
	parts := strings.Split(dotted, ".")
	if len(parts) == 1 {
		delete(obj, parts[0])
		return
	}
	head, rest := parts[0], strings.Join(parts[1:], ".")
	existing, ok := obj[head]
	if !ok {
		return
	}
	child := map[string]json.RawMessage{}
	if err := json.Unmarshal(existing, &child); err != nil {
		return
	}
	deleteDotted(child, rest)
	if len(child) == 0 {
		delete(obj, head)
		return
	}
	if re, err := json.Marshal(child); err == nil {
		obj[head] = re
	}
}

// restoreDotted 把旧值写回 (rollback 用)。
func restoreDotted(obj map[string]json.RawMessage, dotted string, old json.RawMessage) {
	parts := strings.Split(dotted, ".")
	if len(parts) == 1 {
		obj[parts[0]] = old
		return
	}
	head, rest := parts[0], strings.Join(parts[1:], ".")
	child := map[string]json.RawMessage{}
	if existing, ok := obj[head]; ok && len(existing) > 0 {
		_ = json.Unmarshal(existing, &child)
	}
	restoreDotted(child, rest, old)
	if re, err := json.Marshal(child); err == nil {
		obj[head] = re
	}
}

// applyPatch 备份 → 合并 → 原子写。返回被覆盖的旧值集合 (供 manifest 记录)。
//
// 配置文件权限 0600: 它现在装着一枚可以花钱的凭证。
func applyPatch(path string, patch map[string]any) (map[string]json.RawMessage, string, error) {
	backup, err := backupFile(path)
	if err != nil {
		return nil, "", err
	}
	obj, err := readJSONObject(path)
	if err != nil {
		return nil, "", err
	}

	preExisting := map[string]json.RawMessage{}
	for _, key := range sortedKeys(patch) {
		old, err := setDotted(obj, key, patch[key])
		if err != nil {
			return nil, backup, err
		}
		if len(old) > 0 {
			preExisting[key] = old
		}
	}

	encoded, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil, backup, fmt.Errorf("encode config: %w", err)
	}
	if err := atomicWriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return nil, backup, err
	}
	return preExisting, backup, nil
}

// rollbackPatch 精确撤销: 有旧值的还原旧值, 我们新加的删掉。
// **绝不**整份还原备份 —— 用户在 add 之后可能又改过别的东西, 整份还原会把那些改动一起吞掉。
func rollbackPatch(entry *AgentEntry) error {
	obj, err := readJSONObject(entry.ConfigPath)
	if err != nil {
		return err
	}
	for _, key := range entry.PatchKeys {
		if old, ok := entry.PreExisting[key]; ok && len(old) > 0 {
			restoreDotted(obj, key, old)
			continue
		}
		deleteDotted(obj, key)
	}
	encoded, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return atomicWriteFile(entry.ConfigPath, append(encoded, '\n'), 0o600)
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// maskSecret 展示用脱敏 —— dry-run 与日志里绝不打印完整密钥 (红线 9)。
func maskSecret(s string) string {
	if len(s) <= 11 {
		return "sk-acm-****"
	}
	return s[:11] + "…"
}
