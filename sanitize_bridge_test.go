package acosmi

import (
	"errors"
	"testing"

	"github.com/acosmi/acosmi-sdk-go/sanitize"
)

// ---------- 深度校验: 纯 Messages 分支 ----------

func TestApplyRequestSanitizers_HistoryDepth_MessagesBranch(t *testing.T) {
	c := &Client{}
	c.SetDefensiveSanitize(sanitize.MinimalSanitizeConfig{MaxMessagesTurns: 2})

	req := &ChatRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "a"},
			{Role: "assistant", Content: "b"},
			{Role: "user", Content: "c"},
		},
	}
	err := c.applyRequestSanitizers(req)
	if !errors.Is(err, sanitize.ErrHistoryTooDeep) {
		t.Fatalf("expected ErrHistoryTooDeep, got %v", err)
	}
}

// ---------- 深度校验: RawMessages 分支 ----------

func TestApplyRequestSanitizers_HistoryDepth_RawMessagesBranch(t *testing.T) {
	c := &Client{}
	c.SetDefensiveSanitize(sanitize.MinimalSanitizeConfig{MaxMessagesTurns: 1})

	req := &ChatRequest{
		RawMessages: []any{
			map[string]any{"role": "user", "content": "a"},
			map[string]any{"role": "assistant", "content": "b"},
		},
	}
	err := c.applyRequestSanitizers(req)
	if !errors.Is(err, sanitize.ErrHistoryTooDeep) {
		t.Fatalf("expected ErrHistoryTooDeep, got %v", err)
	}
}

// ---------- AutoStripEphemeral 从 RawMessages 中剥离标记 block ----------

func TestApplyRequestSanitizers_AutoStripEphemeral(t *testing.T) {
	c := &Client{}
	c.SetAutoStripEphemeralHistory(true)

	req := &ChatRequest{
		RawMessages: []any{
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "thinking", "thinking": "x", "acosmi_ephemeral": true},
					map[string]any{"type": "text", "text": "visible"},
				},
			},
		},
	}
	if err := c.applyRequestSanitizers(req); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	msgs := req.RawMessages.([]any)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	content := msgs[0].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 block (text), got %d: %+v", len(content), content)
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Errorf("remaining block should be text")
	}
}

// ---------- 未配置时零开销 (RawMessages 不被重写) ----------

func TestApplyRequestSanitizers_NoConfig_NoOp(t *testing.T) {
	c := &Client{}
	original := []any{map[string]any{"role": "user", "content": "hi"}}
	req := &ChatRequest{RawMessages: original}
	if err := c.applyRequestSanitizers(req); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 未配置 → RawMessages 引用保持不变
	got, _ := req.RawMessages.([]any)
	if len(got) != 1 || &got[0] != &original[0] {
		t.Errorf("no-config path should not normalize / rewrite RawMessages")
	}
}

// ---------- struct 切片 RawMessages 归一化: 走 json roundtrip ----------

func TestApplyRequestSanitizers_StructMessagesNormalization(t *testing.T) {
	c := &Client{}
	c.SetAutoStripEphemeralHistory(true) // 触发归一化路径

	// RawMessages 是 []ChatMessage (具体 struct 切片)
	req := &ChatRequest{
		RawMessages: []ChatMessage{{Role: "user", Content: "hi"}},
	}
	if err := c.applyRequestSanitizers(req); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 归一化后应为 []any
	msgs, ok := req.RawMessages.([]any)
	if !ok {
		t.Fatalf("RawMessages should be normalized to []any, got %T", req.RawMessages)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0].(map[string]any)
	if m["role"] != "user" || m["content"] != "hi" {
		t.Errorf("round-trip lost data: %+v", m)
	}
}
