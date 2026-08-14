package main

// =============================================================================
// 配置合并与回滚的行为锁定。
//
// 这是整个 CLI 里唯一会**改动用户既有文件**的部分, 也是最容易造成不可逆损害的地方:
// 一次错误的合并可以让用户的智能体再也起不来。所以这里逐条钉死:
//   ① 未知字段必须原样保留;
//   ② 被我们覆盖的旧值必须能精确还原, 而不是整份还原备份 (那会吞掉用户后来的改动);
//   ③ 我们新加的键在 remove 后必须消失, 且不留空壳中间层;
//   ④ 目标文件不是合法 JSON 时必须**中止**, 绝不覆盖。
// =============================================================================

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("准备测试文件失败: %v", err)
	}
	return path
}

func readBack(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("读回的内容不是合法 JSON: %v\n%s", err, raw)
	}
	return out
}

// ① 未知字段必须原样保留 —— 配置是用户的资产, 我们只补自己那几行。
func TestApplyPatch_PreservesUnknownFields(t *testing.T) {
	path := writeTemp(t, "settings.json", `{
	  "theme": "dark",
	  "customThing": {"a": 1, "b": [1,2,3]},
	  "provider": {"other": {"apiKey": "keep-me"}}
	}`)

	patch := map[string]any{
		"provider.acosmi.baseUrl": "https://acosmi.com/v1",
		"provider.acosmi.apiKey":  "sk-acm-TEST",
	}
	if _, _, err := applyPatch(path, patch); err != nil {
		t.Fatalf("applyPatch 失败: %v", err)
	}

	got := readBack(t, path)
	if got["theme"] != "dark" {
		t.Errorf("顶层未知字段 theme 丢失: %#v", got["theme"])
	}
	custom, ok := got["customThing"].(map[string]any)
	if !ok || custom["a"] != float64(1) {
		t.Errorf("嵌套未知字段 customThing 丢失或被改写: %#v", got["customThing"])
	}
	provider := got["provider"].(map[string]any)
	other := provider["other"].(map[string]any)
	if other["apiKey"] != "keep-me" {
		t.Errorf("同级兄弟键 provider.other 被破坏: %#v", other)
	}
	acosmiCfg := provider["acosmi"].(map[string]any)
	if acosmiCfg["baseUrl"] != "https://acosmi.com/v1" || acosmiCfg["apiKey"] != "sk-acm-TEST" {
		t.Errorf("补丁未正确写入: %#v", acosmiCfg)
	}
}

// ② + ③ 回滚: 覆盖过的还原原值, 新加的删掉, 用户后来的改动保持不动。
func TestRollbackPatch_RestoresOldAndRemovesAdded(t *testing.T) {
	path := writeTemp(t, "settings.json", `{"provider":{"acosmi":{"baseUrl":"https://old.example/v1"}}}`)

	patch := map[string]any{
		"provider.acosmi.baseUrl": "https://acosmi.com/v1", // 覆盖既有值
		"provider.acosmi.apiKey":  "sk-acm-TEST",           // 全新键
	}
	preExisting, _, err := applyPatch(path, patch)
	if err != nil {
		t.Fatalf("applyPatch 失败: %v", err)
	}

	// 模拟用户在 add 之后又改了别的东西 —— remove 绝不能把它一起吞掉。
	obj := readBack(t, path)
	obj["userAddedLater"] = "must survive"
	raw, _ := json.MarshalIndent(obj, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("写入用户改动失败: %v", err)
	}

	entry := &AgentEntry{
		ConfigPath:  path,
		PatchKeys:   sortedKeys(patch),
		PreExisting: preExisting,
	}
	if err := rollbackPatch(entry); err != nil {
		t.Fatalf("rollbackPatch 失败: %v", err)
	}

	got := readBack(t, path)
	if got["userAddedLater"] != "must survive" {
		t.Error("回滚吞掉了用户后来的改动 —— 绝不允许整份还原备份")
	}
	provider, ok := got["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider 层被误删: %#v", got)
	}
	acosmiCfg, ok := provider["acosmi"].(map[string]any)
	if !ok {
		t.Fatalf("acosmi 层被误删 (它原本就存在, 只能还原不能删): %#v", provider)
	}
	if acosmiCfg["baseUrl"] != "https://old.example/v1" {
		t.Errorf("被覆盖的旧值未精确还原: %#v", acosmiCfg["baseUrl"])
	}
	if _, still := acosmiCfg["apiKey"]; still {
		t.Error("我们新加的 apiKey 在回滚后仍然存在")
	}
}

// ③ 补充: 全新的中间层在回滚后不该留下空壳。
func TestRollbackPatch_DropsEmptyIntermediateObjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.json") // 文件不存在 = 全新配置

	patch := map[string]any{"provider.acosmi.apiKey": "sk-acm-TEST"}
	preExisting, _, err := applyPatch(path, patch)
	if err != nil {
		t.Fatalf("applyPatch 失败: %v", err)
	}
	entry := &AgentEntry{ConfigPath: path, PatchKeys: sortedKeys(patch), PreExisting: preExisting}
	if err := rollbackPatch(entry); err != nil {
		t.Fatalf("rollbackPatch 失败: %v", err)
	}
	got := readBack(t, path)
	if _, still := got["provider"]; still {
		t.Errorf("回滚后残留我们自己造出来的空壳中间层: %#v", got)
	}
}

// ④ 目标不是合法 JSON 时必须中止 —— 那可能是用户手写的配置, 我们没有权利丢弃它。
func TestApplyPatch_RefusesToClobberInvalidJSON(t *testing.T) {
	original := "{ this is not json, maybe a half-edited file "
	path := writeTemp(t, "broken.json", original)

	_, _, err := applyPatch(path, map[string]any{"a": "b"})
	if err == nil {
		t.Fatal("对非法 JSON 应中止, 实际却继续写入")
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("读回失败: %v", readErr)
	}
	if string(raw) != original {
		t.Errorf("原文件被破坏了。原内容:\n%s\n现内容:\n%s", original, raw)
	}
}

// 数组/标量类型冲突时同样中止, 不硬把标量当对象往下钻。
func TestSetDotted_RefusesToDescendIntoNonObject(t *testing.T) {
	obj := map[string]json.RawMessage{"provider": json.RawMessage(`"a string, not an object"`)}
	if _, err := setDotted(obj, "provider.acosmi.apiKey", "x"); err == nil {
		t.Fatal("往标量里钻应报错")
	}
}

// 脱敏: 任何展示路径都不得出现完整密钥 (红线 9)。
func TestMaskSecret(t *testing.T) {
	full := "sk-acm-ABCDEFGHIJKLMNOP"
	masked := maskSecret(full)
	if masked == full {
		t.Fatal("maskSecret 原样返回了完整密钥")
	}
	if len(masked) >= len(full) {
		t.Errorf("脱敏结果不应更长: %q", masked)
	}
	if maskSecret("sk-acm-") == "" {
		t.Error("短输入不应返回空串 (会让日志失去上下文)")
	}
}
