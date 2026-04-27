package acosmi

import (
	"errors"
	"strings"
	"testing"
)

// TestParseStreamError_FullSchema: gateway v2 完整 schema (errorCode/retryable/message).
func TestParseStreamError_FullSchema(t *testing.T) {
	data := `{"type":"managed_model_stream_failed","stage":"provider","errorCode":"empty_response","retryable":true,"error":"upstream returned empty response (provider=dashscope model=glm-5.1 latency_ms=3263)","message":"上游模型未返回内容，请重试或更换模型"}`
	se := parseStreamError(data)
	if se == nil {
		t.Fatal("nil StreamError")
	}
	if se.Code != "empty_response" {
		t.Errorf("Code=%q, want empty_response", se.Code)
	}
	if !se.Retryable {
		t.Error("Retryable should be true")
	}
	if se.Stage != "provider" {
		t.Errorf("Stage=%q", se.Stage)
	}
	if se.Message == "" {
		t.Error("Message should not be empty")
	}
	if se.RawError == "" {
		t.Error("RawError should hold debug detail")
	}
}

// TestParseStreamError_LegacySchema: 旧 schema 仅含 stage/error, 新字段为零值, 仍可工作.
func TestParseStreamError_LegacySchema(t *testing.T) {
	data := `{"stage":"provider","error":"some legacy message"}`
	se := parseStreamError(data)
	if se == nil {
		t.Fatal("nil")
	}
	if se.Code != "" {
		t.Error("Code should be empty for legacy event")
	}
	if se.Retryable {
		t.Error("Retryable should default to false")
	}
	if se.RawError != "some legacy message" {
		t.Errorf("RawError=%q", se.RawError)
	}
}

// TestParseStreamError_InvalidJSON: 无法解析时 RawError 兜底.
func TestParseStreamError_InvalidJSON(t *testing.T) {
	se := parseStreamError("not-a-json{")
	if se == nil {
		t.Fatal("nil")
	}
	if se.RawError != "not-a-json{" {
		t.Errorf("RawError=%q, want raw fallback", se.RawError)
	}
}

// TestStreamError_ErrorString: 文案向后兼容 — 必须以 "stream failed:" 开头.
func TestStreamError_ErrorString(t *testing.T) {
	se := &StreamError{Stage: "provider", RawError: "boom"}
	got := se.Error()
	if !strings.HasPrefix(got, "stream failed:") {
		t.Errorf("Error() must start with 'stream failed:', got %q", got)
	}
	if !strings.Contains(got, "provider") {
		t.Errorf("missing stage: %q", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("missing raw: %q", got)
	}
}

// TestStreamError_ErrorsAs: errors.As 应能从 channel 上的 error 提取结构化数据 — 这是新合约的核心.
func TestStreamError_ErrorsAs(t *testing.T) {
	var err error = parseStreamError(`{"errorCode":"empty_response","retryable":true,"message":"上游空回复"}`)
	var se *StreamError
	if !errors.As(err, &se) {
		t.Fatal("errors.As failed")
	}
	if se.Code != "empty_response" || !se.Retryable {
		t.Errorf("got Code=%s Retryable=%v", se.Code, se.Retryable)
	}
}

// TestStreamError_NilSafe.
func TestStreamError_NilSafe(t *testing.T) {
	var se *StreamError
	if se.Error() != "" {
		t.Error("nil StreamError.Error() should be empty")
	}
}

// =============================================================================
// v0.14.1 (V2 P0.7): parseStreamError 三态 schema 兼容
//
// 网关在 Anthropic 协议路径写 event:error 时叠加私有字段, error 不再是 string 而是 object。
// 这组用例固定三种 schema 的解析契约, 任何一处回归都会被这里抓住。
// =============================================================================

// 1. Anthropic 协议扩展格式 (P0.7 网关下发) — error 是 object, 含 acosmi 私有字段
func TestParseStreamError_AnthropicWithExtensions(t *testing.T) {
	data := `{"type":"error","error":{"type":"overloaded_error","message":"上游连接中断"},"errorCode":"upstream_disconnect","retryable":true,"message":"上游连接中断, 请重试或更换模型","stage":"provider"}`
	se := parseStreamError(data)
	if se == nil {
		t.Fatal("nil")
	}
	if se.Code != "upstream_disconnect" {
		t.Errorf("Code=%q, want upstream_disconnect", se.Code)
	}
	if !se.Retryable {
		t.Error("Retryable should be true (upstream_disconnect)")
	}
	if se.Stage != "provider" {
		t.Errorf("Stage=%q", se.Stage)
	}
	if se.Message != "上游连接中断, 请重试或更换模型" {
		t.Errorf("Message=%q (acosmi 私有 message 优先, 不被 Anthropic error.message 覆盖)", se.Message)
	}
	if se.RawError == "" {
		t.Error("RawError should preserve original Anthropic error object JSON")
	}
}

// 2. Anthropic 协议纯净格式 (老网关 / 官方上游直返) — 无 acosmi 私有字段
func TestParseStreamError_AnthropicPure(t *testing.T) {
	data := `{"type":"error","error":{"type":"overloaded_error","message":"Anthropic 标准消息"}}`
	se := parseStreamError(data)
	if se == nil {
		t.Fatal("nil")
	}
	// 私有字段缺失, 用 Anthropic error.type / error.message 兜底
	if se.Code != "overloaded_error" {
		t.Errorf("Code=%q, want overloaded_error (从 error.type 兜底)", se.Code)
	}
	if se.Message != "Anthropic 标准消息" {
		t.Errorf("Message=%q, want 从 error.message 兜底", se.Message)
	}
	if se.Retryable {
		t.Error("Retryable should default false when 私有字段缺失")
	}
}

// 3. acosmi managed-model 老协议 — error 是 string
func TestParseStreamError_AcosmiOldString(t *testing.T) {
	data := `{"errorCode":"empty_response","stage":"provider","error":"raw debug string","message":"用户面文案","retryable":true}`
	se := parseStreamError(data)
	if se == nil {
		t.Fatal("nil")
	}
	if se.Code != "empty_response" {
		t.Errorf("Code=%q", se.Code)
	}
	if se.RawError != "raw debug string" {
		t.Errorf("RawError=%q, want plain string", se.RawError)
	}
	if !se.Retryable {
		t.Error("Retryable should be true")
	}
}

// 4. error 字段缺失 — 仅有 errorCode/message
func TestParseStreamError_NoErrorField(t *testing.T) {
	data := `{"errorCode":"upstream_timeout","retryable":true,"message":"上游响应超时"}`
	se := parseStreamError(data)
	if se == nil {
		t.Fatal("nil")
	}
	if se.Code != "upstream_timeout" {
		t.Errorf("Code=%q", se.Code)
	}
	if se.RawError != "" {
		t.Errorf("RawError should be empty when error field absent, got %q", se.RawError)
	}
}

// 5. 私有 message 优先级 — 同时有 acosmi message 和 Anthropic error.message 时,
//    acosmi 的 (中文友好) 必须赢 (因为它是网关 P0.5 friendly 文案)
func TestParseStreamError_PrivateMessagePriority(t *testing.T) {
	data := `{"error":{"type":"overloaded_error","message":"Anthropic 英文消息"},"errorCode":"upstream_timeout","message":"中文友好提示"}`
	se := parseStreamError(data)
	if se.Message != "中文友好提示" {
		t.Errorf("acosmi 私有 message 必须优先, got %q", se.Message)
	}
}

// 6. errorCode 缺 + Anthropic error.type 在 — 兜底使用 type 作 Code
func TestParseStreamError_AnthropicTypeFallbackForCode(t *testing.T) {
	data := `{"error":{"type":"rate_limit_error","message":"hit limit"}}`
	se := parseStreamError(data)
	if se.Code != "rate_limit_error" {
		t.Errorf("Code 必须从 error.type 兜底, got %q", se.Code)
	}
}
