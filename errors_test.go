// =============================================================================
// 文件: acosmi-sdk-go/errors_test.go
// 职责: L6 V2 P1 — HTTPError / NetworkError / parseHTTPError / classifyTransport 矩阵
// =============================================================================

package acosmi

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPError_ErrorFormatBackwardCompat(t *testing.T) {
	cases := []struct {
		name string
		he   *HTTPError
		want string
	}{
		{
			name: "anthropic-style with type+message",
			he:   &HTTPError{StatusCode: 429, Type: "rate_limit_error", Message: "Too many"},
			want: "HTTP 429: [rate_limit_error] Too many",
		},
		{
			name: "openai-style message only",
			he:   &HTTPError{StatusCode: 500, Message: "internal error"},
			want: "HTTP 500: internal error",
		},
		{
			name: "body fallback when no type/message",
			he:   &HTTPError{StatusCode: 502, Body: "Bad Gateway"},
			want: "HTTP 502: Bad Gateway",
		},
		{
			name: "status code only",
			he:   &HTTPError{StatusCode: 401},
			want: "HTTP 401",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.he.Error(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseHTTPError_AnthropicFormat(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"invalid_request_error","message":"missing field"}}`)
	err := parseHTTPError(400, body)
	var he *HTTPError
	if !stderrors.As(err, &he) {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if he.StatusCode != 400 {
		t.Errorf("StatusCode: got %d, want 400", he.StatusCode)
	}
	if he.Type != "invalid_request_error" {
		t.Errorf("Type: got %q", he.Type)
	}
	if he.Message != "missing field" {
		t.Errorf("Message: got %q", he.Message)
	}
}

func TestParseHTTPError_OpenAIFormat(t *testing.T) {
	body := []byte(`{"error":{"message":"invalid model","type":"invalid_request_error","code":"model_not_found"}}`)
	err := parseHTTPError(404, body)
	var he *HTTPError
	if !stderrors.As(err, &he) {
		t.Fatalf("expected *HTTPError")
	}
	if he.Type != "invalid_request_error" {
		t.Errorf("Type: got %q", he.Type)
	}
	if he.Message != "invalid model" {
		t.Errorf("Message: got %q", he.Message)
	}
}

func TestParseHTTPError_EmptyBody(t *testing.T) {
	err := parseHTTPError(503, nil)
	var he *HTTPError
	if !stderrors.As(err, &he) {
		t.Fatalf("expected *HTTPError")
	}
	if he.StatusCode != 503 {
		t.Errorf("StatusCode: got %d", he.StatusCode)
	}
	if he.Error() != "HTTP 503" {
		t.Errorf("empty body Error(): got %q", he.Error())
	}
}

func TestParseHTTPError_NonJSONBody(t *testing.T) {
	body := []byte("Service Temporarily Unavailable")
	err := parseHTTPError(502, body)
	var he *HTTPError
	if !stderrors.As(err, &he) {
		t.Fatalf("expected *HTTPError")
	}
	if !strings.Contains(he.Error(), "Service Temporarily Unavailable") {
		t.Errorf("non-JSON body should fall through to Body: got %q", he.Error())
	}
}

func TestParseHTTPErrorWithHeader_RetryAfter(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "60")
	err := parseHTTPErrorWithHeader(429, []byte(`{"error":{"message":"slow down"}}`), header)
	var he *HTTPError
	if !stderrors.As(err, &he) {
		t.Fatalf("expected *HTTPError")
	}
	if he.RetryAfter != 60 {
		t.Errorf("RetryAfter: got %d, want 60", he.RetryAfter)
	}
}

func TestParseHTTPErrorWithHeader_RetryAfterInvalid(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "Wed, 21 Oct 2026 07:28:00 GMT") // HTTP-date 形式, 暂不支持
	err := parseHTTPErrorWithHeader(429, nil, header)
	var he *HTTPError
	if !stderrors.As(err, &he) {
		t.Fatalf("expected *HTTPError")
	}
	if he.RetryAfter != 0 {
		t.Errorf("RetryAfter for HTTP-date should be 0 (unsupported), got %d", he.RetryAfter)
	}
}

// ----------------- NetworkError / classifyTransport -----------------

func TestClassifyTransport_NilErr(t *testing.T) {
	if got := classifyTransport("GET /test", "http://x", nil); got != nil {
		t.Errorf("nil err → expected nil, got %+v", got)
	}
}

func TestClassifyTransport_ContextDeadline(t *testing.T) {
	ne := classifyTransport("POST /v1/messages", "http://x", context.DeadlineExceeded)
	if ne == nil {
		t.Fatal("expected non-nil NetworkError")
	}
	if !ne.IsTimeout() {
		t.Error("DeadlineExceeded should be Timeout=true")
	}
	if ne.IsEOF() {
		t.Error("DeadlineExceeded should be EOF=false")
	}
}

func TestClassifyTransport_IOEOFEOF(t *testing.T) {
	ne := classifyTransport("POST /v1/messages", "http://x", io.EOF)
	if !ne.IsEOF() {
		t.Error("io.EOF should be EOF=true")
	}
	if ne.IsTimeout() {
		t.Error("io.EOF should be Timeout=false")
	}
}

func TestClassifyTransport_UnexpectedEOF(t *testing.T) {
	ne := classifyTransport("POST /v1/messages", "http://x", io.ErrUnexpectedEOF)
	if !ne.IsEOF() {
		t.Error("ErrUnexpectedEOF should be EOF=true")
	}
}

func TestClassifyTransport_StringConnectionReset(t *testing.T) {
	err := fmt.Errorf("read tcp 1.2.3.4:443->5.6.7.8:443: read: connection reset by peer")
	ne := classifyTransport("POST /v1/messages", "http://x", err)
	if !ne.IsEOF() {
		t.Error("connection reset 字符串匹配 should set EOF=true")
	}
}

func TestClassifyTransport_UnknownErrorNotRetryable(t *testing.T) {
	err := fmt.Errorf("DNS resolution failed: no such host")
	ne := classifyTransport("POST /v1/messages", "http://x", err)
	if ne.IsTimeout() || ne.IsEOF() {
		t.Errorf("DNS error should NOT be Timeout/EOF (not retryable), got Timeout=%v EOF=%v",
			ne.IsTimeout(), ne.IsEOF())
	}
}

func TestNetworkError_ErrorFormatHasOpAndCause(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	ne := &NetworkError{Op: "POST /v1/messages", URL: "http://x:8009/v1/messages", Cause: cause}
	got := ne.Error()
	if !strings.Contains(got, "POST /v1/messages") {
		t.Errorf("Error() should contain Op, got %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("Error() should contain cause msg, got %q", got)
	}
}

func TestNetworkError_UnwrapPreservesCause(t *testing.T) {
	cause := fmt.Errorf("base error")
	ne := &NetworkError{Op: "x", URL: "y", Cause: cause}
	if unwrapped := ne.Unwrap(); unwrapped != cause {
		t.Errorf("Unwrap should return original cause")
	}
	// errors.Is should follow Unwrap chain
	if !stderrors.Is(ne, cause) {
		t.Error("errors.Is should match the wrapped cause")
	}
}

// 模拟实际 net.Error.Timeout() 行为
type mockNetTimeoutErr struct{}

func (mockNetTimeoutErr) Error() string { return "i/o timeout" }
func (mockNetTimeoutErr) Timeout() bool { return true }
func (mockNetTimeoutErr) Temporary() bool { return false }

func TestClassifyTransport_NetErrorTimeout(t *testing.T) {
	ne := classifyTransport("GET /x", "http://x", mockNetTimeoutErr{})
	if !ne.IsTimeout() {
		t.Error("mockNetTimeoutErr.Timeout()=true should set NetworkError.Timeout=true")
	}
}

// 防止 time 包未使用警告
var _ = time.Second
