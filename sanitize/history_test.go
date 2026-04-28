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

// ---------- 基础: thinking 永不剥 (硬豁免) ----------
//
// 历史背景: 早期 v0.13~v0.15.1 期间, 网关 Anthropic preset 给 thinking 块注入
// acosmi_ephemeral=true, 客户端 StripEphemeral 据此剥除。这导致 extended thinking
// + tool_use 续轮场景下, 上游强制要求保留 thinking 但客户端已剥 → 400:
//   "The content[].thinking in the thinking mode must be passed back to the API."
// v0.15.2 起 thinking / redacted_thinking 内置硬豁免, 即使带 ephemeral 标记
// 也不剥 (网关 commit 55fe8090 已停止注入, 此豁免兜底历史污染会话)。

func TestStripEphemeral_NeverStripsThinking(t *testing.T) {
	msgs := []any{
		map[string]any{
			"role": "assistant",
			"content": []any{
				thinkingBlock("internal reasoning", true), // 带 ephemeral 标记
				ephemeralTextBlock("ephemeral note", true), // 也带标记 (作为对照)
				map[string]any{"type": "text", "text": "hello"},
			},
		},
	}
	out := StripEphemeral(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	content := out[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("expected 2 blocks (thinking 豁免 + 普通 text), got %d: %+v", len(content), content)
	}
	// 第一块应是 thinking, 第二块是 hello (ephemeral text 被剥)
	if content[0].(map[string]any)["type"] != "thinking" {
		t.Errorf("thinking must be preserved, got %v", content[0])
	}
	if content[1].(map[string]any)["text"] != "hello" {
		t.Errorf("non-ephemeral text must remain, got %v", content[1])
	}
}

func TestStripEphemeral_NeverStripsRedactedThinking(t *testing.T) {
	msgs := []any{
		map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{
					"type":               "redacted_thinking",
					"data":               "encrypted-blob",
					EphemeralMarkerField: true,
				},
				map[string]any{"type": "text", "text": "answer"},
			},
		},
	}
	out := StripEphemeral(msgs)
	content := out[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("redacted_thinking must survive ephemeral marker, got %d blocks", len(content))
	}
	if content[0].(map[string]any)["type"] != "redacted_thinking" {
		t.Errorf("first block must be redacted_thinking, got %v", content[0])
	}
}

// thinking 是 tool_use 类 block 之外的类型, 不应触发 droppedToolUseIDs 收集,
// 即不能联动剥 user 轮的 tool_result。
func TestStripEphemeral_ThinkingDoesNotCascade(t *testing.T) {
	msgs := []any{
		map[string]any{
			"role": "assistant",
			"content": []any{
				thinkingBlock("plan...", true),
				map[string]any{
					"type": "tool_use",
					"id":   "tu_1",
					"name": "calc",
				},
			},
		},
		map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{
					"type":        "tool_result",
					"tool_use_id": "tu_1",
					"content":     "42",
				},
			},
		},
	}
	out := StripEphemeral(msgs)
	if len(out) != 2 {
		t.Fatalf("both turns must survive (thinking 豁免, tool_use 无 ephemeral), got %d", len(out))
	}
	a := out[0].(map[string]any)["content"].([]any)
	if len(a) != 2 {
		t.Errorf("assistant turn must keep thinking + tool_use, got %d", len(a))
	}
	u := out[1].(map[string]any)["content"].([]any)
	if len(u) != 1 {
		t.Errorf("user tool_result must NOT be cascade-dropped, got %d", len(u))
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
