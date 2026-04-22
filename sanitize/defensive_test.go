package sanitize

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// makeBase64 生成指定解码字节数的 base64 字符串。
func makeBase64(decodedBytes int) string {
	raw := make([]byte, decodedBytes)
	return base64.StdEncoding.EncodeToString(raw)
}

// imageMsg 构造一条含单个 image block 的 user 消息。
func imageMsg(b64 string) map[string]any {
	return map[string]any{
		"role": "user",
		"content": []any{
			map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": "image/jpeg",
					"data":       b64,
				},
			},
		},
	}
}

// textMsg 构造纯文本消息。
func textMsg(role, text string) map[string]any {
	return map[string]any{
		"role": role,
		"content": []any{
			map[string]any{"type": "text", "text": text},
		},
	}
}

// blockMsg 构造带自定义 blocks 的消息。
func blockMsg(role string, blocks ...any) map[string]any {
	return map[string]any{"role": role, "content": blocks}
}

// ---------- S-1: image 超限 ----------

func TestSanitize_ImageTooLarge(t *testing.T) {
	msgs := []any{imageMsg(makeBase64(2 * 1024 * 1024))} // 2 MB
	cfg := MinimalSanitizeConfig{MaxImageBytes: 1 * 1024 * 1024}

	_, err := Sanitize(msgs, cfg)
	if err == nil {
		t.Fatal("expected SizeError, got nil")
	}
	var se *SizeError
	if !errors.As(err, &se) {
		t.Fatalf("expected *SizeError, got %T: %v", err, err)
	}
	if se.BlockType != BlockImage {
		t.Errorf("block type = %q, want image", se.BlockType)
	}
	if se.Actual <= se.Limit {
		t.Errorf("actual %d should exceed limit %d", se.Actual, se.Limit)
	}
}

func TestSanitize_ImageUnderLimit(t *testing.T) {
	msgs := []any{imageMsg(makeBase64(512 * 1024))} // 512 KB
	cfg := MinimalSanitizeConfig{MaxImageBytes: 1 * 1024 * 1024}

	_, err := Sanitize(msgs, cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

// URL 版 image 不在本地量体积, 即使设了硬上限也放行。
func TestSanitize_ImageURLSkipsSizeCheck(t *testing.T) {
	msgs := []any{map[string]any{
		"role": "user",
		"content": []any{map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "url",
				"url":  "https://example.com/huge.jpg",
			},
		}},
	}}
	cfg := MinimalSanitizeConfig{MaxImageBytes: 1}
	if _, err := Sanitize(msgs, cfg); err != nil {
		t.Fatalf("url image should bypass size check: %v", err)
	}
}

// ---------- S-2: video 超限 ----------

func TestSanitize_VideoTooLarge(t *testing.T) {
	videoBlock := map[string]any{
		"type": "video",
		"source": map[string]any{
			"type": "base64",
			"data": makeBase64(10 * 1024 * 1024),
		},
	}
	msgs := []any{blockMsg("user", videoBlock)}
	cfg := MinimalSanitizeConfig{MaxVideoBytes: 5 * 1024 * 1024}

	_, err := Sanitize(msgs, cfg)
	var se *SizeError
	if !errors.As(err, &se) || se.BlockType != BlockVideo {
		t.Fatalf("expected SizeError video, got %v", err)
	}
}

// ---------- S-3: PermanentDenyBlocks 剥除 ----------

func TestSanitize_DenyBlocks_DropsContainerUpload(t *testing.T) {
	containerBlock := map[string]any{"type": "container_upload", "file_id": "f_1"}
	textBlock := map[string]any{"type": "text", "text": "hi"}
	msgs := []any{blockMsg("user", containerBlock, textBlock)}

	cfg := MinimalSanitizeConfig{PermanentDenyBlocks: []BlockType{BlockContainerUpload}}
	out, err := Sanitize(msgs, cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	content := out[0].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 block remaining (text), got %d", len(content))
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Errorf("remaining block should be text, got %v", content[0])
	}
}

// deny 命中的 tool_use 联动剥掉对应的 tool_result。
func TestSanitize_DenyBlocks_CascadesToToolResult(t *testing.T) {
	serverToolUse := map[string]any{
		"type": "server_tool_use",
		"id":   "stu_1",
		"name": "web_search",
	}
	toolResult := map[string]any{
		"type":        "tool_result",
		"tool_use_id": "stu_1",
		"content":     "result payload",
	}
	msgs := []any{
		blockMsg("assistant", serverToolUse),
		blockMsg("user", toolResult),
	}

	cfg := MinimalSanitizeConfig{PermanentDenyBlocks: []BlockType{BlockServerToolUse}}
	out, err := Sanitize(msgs, cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// assistant 轮无剩余 block → 整条 message 丢弃
	// user 轮 tool_result 联动剥 → 整条 message 丢弃
	if len(out) != 0 {
		t.Fatalf("expected both messages dropped, got %d: %+v", len(out), out)
	}
}

// ---------- S-9: history 超深 ----------

func TestSanitize_HistoryTooDeep(t *testing.T) {
	msgs := make([]any, 10)
	for i := range msgs {
		msgs[i] = textMsg("user", "hi")
	}
	cfg := MinimalSanitizeConfig{MaxMessagesTurns: 5}

	_, err := Sanitize(msgs, cfg)
	if !errors.Is(err, ErrHistoryTooDeep) {
		t.Fatalf("expected ErrHistoryTooDeep, got %v", err)
	}
}

func TestSanitize_HistoryAtLimit_OK(t *testing.T) {
	msgs := make([]any, 5)
	for i := range msgs {
		msgs[i] = textMsg("user", "hi")
	}
	cfg := MinimalSanitizeConfig{MaxMessagesTurns: 5}
	if _, err := Sanitize(msgs, cfg); err != nil {
		t.Fatalf("at-limit should pass: %v", err)
	}
}

// ---------- 额外: data URL 前缀兜底 ----------

func TestSanitize_DataURLPrefixHandled(t *testing.T) {
	// 非标准但实际会有: 整串 data:image/jpeg;base64,xxx 塞进 source.data
	b64 := makeBase64(512 * 1024)
	full := "data:image/jpeg;base64," + b64
	msgs := []any{map[string]any{
		"role": "user",
		"content": []any{map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "base64",
				"data": full,
			},
		}},
	}}
	cfg := MinimalSanitizeConfig{MaxImageBytes: 1 * 1024 * 1024}
	if _, err := Sanitize(msgs, cfg); err != nil {
		t.Fatalf("data URL prefix should still parse: %v", err)
	}

	// 带前缀 + 超限时仍应拦住
	b64 = makeBase64(2 * 1024 * 1024)
	full = "data:image/jpeg;base64," + b64
	msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)["source"].(map[string]any)["data"] = full
	_, err := Sanitize(msgs, cfg)
	if err == nil {
		t.Fatal("expected SizeError for oversize with data URL prefix")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error should mention image, got %v", err)
	}
}
