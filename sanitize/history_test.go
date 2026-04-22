package sanitize

import (
	"testing"
)

// ephemeralTextBlock 构造带 acosmi_ephemeral 标记的 text block。
func ephemeralTextBlock(text string, eph bool) map[string]any {
	b := map[string]any{"type": "text", "text": text}
	if eph {
		b[EphemeralMarkerField] = true
	}
	return b
}

// thinkingBlock 构造带 ephemeral 标记的 thinking block (最典型场景)。
func thinkingBlock(text string, eph bool) map[string]any {
	b := map[string]any{"type": "thinking", "thinking": text, "signature": "sig-xxx"}
	if eph {
		b[EphemeralMarkerField] = true
	}
	return b
}

// ---------- 基础: 剥 thinking ----------

func TestStripEphemeral_RemovesMarkedThinking(t *testing.T) {
	msgs := []any{
		map[string]any{
			"role": "assistant",
			"content": []any{
				thinkingBlock("internal reasoning", true),
				map[string]any{"type": "text", "text": "hello"},
			},
		},
	}
	out := StripEphemeral(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	content := out[0].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 block after strip, got %d", len(content))
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Errorf("remaining block should be text, got %v", content[0])
	}
}

// ---------- H-2 审计: tool_use 被剥 → 对应 tool_result 联动剥 ----------

func TestStripEphemeral_CascadesToolResult(t *testing.T) {
	msgs := []any{
		// assistant 轮: 含 ephemeral 的 server_tool_use
		map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "text", "text": "Let me search."},
				map[string]any{
					"type":               "server_tool_use",
					"id":                 "stu_web_1",
					"name":               "web_search",
					EphemeralMarkerField: true,
				},
			},
		},
		// user 轮: 回传 tool_result 引用 stu_web_1
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "stu_web_1",
					"content":     "search result payload",
				},
			},
		},
		// 之后还有一轮正常对话
		map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": "Based on search..."}},
		},
	}

	out := StripEphemeral(msgs)

	// assistant 第 1 轮: text 保留, server_tool_use 剥 → 1 block 剩
	assistant1 := out[0].(map[string]any)["content"].([]any)
	if len(assistant1) != 1 {
		t.Fatalf("assistant turn 1: expected 1 block (text) after strip, got %d", len(assistant1))
	}
	if assistant1[0].(map[string]any)["type"] != "text" {
		t.Errorf("remaining block should be text, got %v", assistant1[0])
	}

	// user 轮: tool_result 联动剥 → content 空 → 整条丢弃
	// 所以 out 应只剩 2 条: assistant1 (缩减后), assistant2
	if len(out) != 2 {
		t.Fatalf("expected 2 messages (user turn dropped), got %d: %+v", len(out), out)
	}
	if out[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("message[1] should be assistant turn 2, got %v", out[1])
	}
}

// ---------- 不修改原数据 ----------

func TestStripEphemeral_DoesNotMutateInput(t *testing.T) {
	original := []any{
		map[string]any{
			"role": "assistant",
			"content": []any{
				thinkingBlock("secret", true),
				map[string]any{"type": "text", "text": "visible"},
			},
		},
	}
	origContent := original[0].(map[string]any)["content"].([]any)
	origLen := len(origContent)

	_ = StripEphemeral(original)

	// 原数组不应被改短 (我们重建而非原地删)
	if got := len(original[0].(map[string]any)["content"].([]any)); got != origLen {
		t.Errorf("input mutated: content len %d → %d", origLen, got)
	}
}

// ---------- 空 / malformed 输入不 panic ----------

func TestStripEphemeral_EmptyAndMalformed(t *testing.T) {
	// 空输入
	if out := StripEphemeral(nil); len(out) != 0 {
		t.Errorf("nil input should return empty, got %v", out)
	}

	// content 是 string (非 block 数组), 跳过不 panic
	msgs := []any{map[string]any{"role": "user", "content": "plain text"}}
	out := StripEphemeral(msgs)
	if len(out) != 1 {
		t.Errorf("plain-content message should pass through")
	}

	// 非 map 消息, 透传
	msgs = []any{"garbage", 42, nil}
	out = StripEphemeral(msgs)
	if len(out) != 3 {
		t.Errorf("non-map messages should pass through untouched")
	}
}

// ---------- 无 ephemeral 标记时零拷贝 (map 引用相同) ----------

func TestStripEphemeral_ZeroCopyWhenNothingDropped(t *testing.T) {
	msg := map[string]any{
		"role":    "assistant",
		"content": []any{map[string]any{"type": "text", "text": "hi"}},
	}
	msgs := []any{msg}
	out := StripEphemeral(msgs)
	// 未命中 pred 时, 原 message map 被直接透传; 比较指针身份。
	if &out[0] == &msgs[0] {
		// Can't directly compare; out is new slice. Check msg identity via map key probe.
	}
	// 简单判据: 输出 content 应与输入是同一底层数组 (零拷贝)。
	outContent := out[0].(map[string]any)["content"].([]any)
	inContent := msg["content"].([]any)
	if len(outContent) != len(inContent) || &outContent[0] != &inContent[0] {
		t.Errorf("no-op case should keep content slice identity (zero copy)")
	}
}
