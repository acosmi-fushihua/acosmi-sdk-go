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
