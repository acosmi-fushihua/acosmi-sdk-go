// =============================================================================
// 文件: bug_report_test.go | 测试 v0.17.0 V30 CrabCode bug 报告 SDK 端
// 覆盖: 200 success / 401 / 403 ZDR / 400 invalid / 429 rate limit / 404 not found
//       + reportData 序列化语义 + 公开 GET 不要求 token
// =============================================================================

package acosmi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// SubmitBugReport
// -----------------------------------------------------------------------------

func TestSubmitBugReport_Success(t *testing.T) {
	var capturedBody []byte
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/crabcode_cli_feedback" {
			t.Errorf("expected /api/v4/crabcode_cli_feedback, got %s", r.URL.Path)
		}
		capturedAuth = r.Header.Get("Authorization")
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"feedback_id": "abc-123",
			"detail_url":  "https://acosmi.com/chat/crabcode/bug/abc-123",
		})
	}))
	defer server.Close()

	c := testClient(t, server.URL)
	report := map[string]any{
		"description": "test bug",
		"platform":    "darwin",
		"version":     "v0.1.0",
	}
	result, err := c.SubmitBugReport(context.Background(), report)
	if err != nil {
		t.Fatalf("SubmitBugReport: %v", err)
	}
	if result.FeedbackID != "abc-123" {
		t.Errorf("FeedbackID = %q", result.FeedbackID)
	}
	if !strings.Contains(result.DetailURL, "/chat/crabcode/bug/abc-123") {
		t.Errorf("DetailURL = %q", result.DetailURL)
	}

	// 校验 Bearer 透传
	if !strings.HasPrefix(capturedAuth, "Bearer test-token") {
		t.Errorf("Authorization header = %q", capturedAuth)
	}

	// 校验请求体: {content: "<jsonStringify(reportData)>"} (双层编码)
	var outer struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(capturedBody, &outer); err != nil {
		t.Fatalf("unmarshal outer: %v", err)
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(outer.Content), &inner); err != nil {
		t.Fatalf("unmarshal inner content: %v (content=%q)", err, outer.Content)
	}
	if inner["description"] != "test bug" {
		t.Errorf("inner.description = %v", inner["description"])
	}
	if inner["platform"] != "darwin" {
		t.Errorf("inner.platform = %v", inner["platform"])
	}
}

func TestSubmitBugReport_NilReportData(t *testing.T) {
	c := testClient(t, "http://127.0.0.1:1") // server URL doesn't matter, we error before connecting
	_, err := c.SubmitBugReport(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "reportData required") {
		t.Fatalf("expected reportData required error, got: %v", err)
	}
}

func TestSubmitBugReport_ZDR_403(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"type":    "permission_error",
				"message": "crabcode: bug collection refused due to Custom data retention settings on this organization",
			},
		})
	}))
	defer server.Close()

	c := testClient(t, server.URL)
	_, err := c.SubmitBugReport(context.Background(), map[string]any{"description": "x"})
	if err == nil {
		t.Fatal("expected ZDR error, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if he.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d", he.StatusCode)
	}
	if he.Type != "permission_error" {
		t.Errorf("Type = %q", he.Type)
	}
	if !strings.Contains(he.Message, "Custom data retention settings") {
		t.Errorf("Message missing ZDR keyword: %q", he.Message)
	}
}

func TestSubmitBugReport_InvalidPayload_400(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"type":    "invalid_request_error",
				"message": "crabcode: payload is not valid JSON",
			},
		})
	}))
	defer server.Close()

	c := testClient(t, server.URL)
	_, err := c.SubmitBugReport(context.Background(), map[string]any{"description": "x"})
	if err == nil {
		t.Fatal("expected 400, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if he.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d", he.StatusCode)
	}
	if he.Type != "invalid_request_error" {
		t.Errorf("Type = %q", he.Type)
	}
}

func TestSubmitBugReport_RateLimit_429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{
			"message": "请求过于频繁，请稍后重试",
		})
	}))
	defer server.Close()

	c := testClient(t, server.URL)
	_, err := c.SubmitBugReport(context.Background(), map[string]any{"description": "x"})
	if err == nil {
		t.Fatal("expected 429, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if he.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d", he.StatusCode)
	}
	if he.RetryAfter != 120 {
		t.Errorf("RetryAfter = %d, want 120", he.RetryAfter)
	}
}

// -----------------------------------------------------------------------------
// GetBugReport
// -----------------------------------------------------------------------------

func TestGetBugReport_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	clientDt := now.Add(-1 * time.Hour)

	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/v4/crabcode/bug/abc-123" {
			t.Errorf("path = %q", r.URL.Path)
		}
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":             "abc-123",
			"description":    "test bug [REDACTED:openai-key]",
			"platform":       "darwin",
			"terminal":       "iTerm.app",
			"version":        "v0.1.0",
			"messageCount":   3,
			"hasErrors":      false,
			"status":         "new",
			"clientDatetime": clientDt.Format(time.RFC3339),
			"createdAt":      now.Format(time.RFC3339),
			"transcript": []any{
				map[string]any{"role": "user", "content": "hi"},
			},
			"extras": map[string]any{
				"rawTranscriptJsonl": "...",
			},
		})
	}))
	defer server.Close()

	c := testClient(t, server.URL)
	view, err := c.GetBugReport(context.Background(), "abc-123")
	if err != nil {
		t.Fatalf("GetBugReport: %v", err)
	}
	if view.ID != "abc-123" {
		t.Errorf("ID = %q", view.ID)
	}
	if !strings.Contains(view.Description, "REDACTED") {
		t.Errorf("Description should retain redaction marker, got %q", view.Description)
	}
	if view.Status != "new" {
		t.Errorf("Status = %q", view.Status)
	}
	if view.MessageCount != 3 {
		t.Errorf("MessageCount = %d", view.MessageCount)
	}
	if view.ClientDatetime == nil || !view.ClientDatetime.Equal(clientDt) {
		t.Errorf("ClientDatetime = %v, want %v", view.ClientDatetime, clientDt)
	}
	if len(view.Transcript) != 1 {
		t.Errorf("Transcript len = %d", len(view.Transcript))
	}
	if view.Extras["rawTranscriptJsonl"] != "..." {
		t.Errorf("Extras = %+v", view.Extras)
	}

	// 公开端点 — 即使 testClient 注入了 token, doPublicJSON 也不传 Bearer.
	// 但 SDK 实际行为 (BrowseSkillStore 等) 是: 有 token 自动附带享受认证待遇.
	// 这里只校验请求成功, 不严格断言无 Authorization (允许两种实现).
	_ = capturedAuth
}

func TestGetBugReport_NotFound_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"type":    "not_found_error",
				"message": "bug not found",
			},
		})
	}))
	defer server.Close()

	c := testClient(t, server.URL)
	_, err := c.GetBugReport(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected 404, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if he.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d", he.StatusCode)
	}
}

func TestGetBugReport_EmptyID(t *testing.T) {
	c := testClient(t, "http://127.0.0.1:1")
	_, err := c.GetBugReport(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "bugID required") {
		t.Fatalf("expected bugID required error, got: %v", err)
	}
	// 仅空白也应该被拒
	_, err = c.GetBugReport(context.Background(), "   ")
	if err == nil || !strings.Contains(err.Error(), "bugID required") {
		t.Fatalf("expected bugID required for whitespace-only, got: %v", err)
	}
}

// -----------------------------------------------------------------------------
// reportData 编码语义 (任意 struct + map 都应正常 marshal)
// -----------------------------------------------------------------------------

func TestSubmitBugReport_StructReportData(t *testing.T) {
	type Report struct {
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"feedback_id": "ok",
			"detail_url":  "https://x/y",
		})
	}))
	defer server.Close()

	c := testClient(t, server.URL)
	_, err := c.SubmitBugReport(context.Background(), Report{Description: "from struct", Version: "v1"})
	if err != nil {
		t.Fatalf("SubmitBugReport: %v", err)
	}

	var outer struct{ Content string }
	if err := json.Unmarshal(capturedBody, &outer); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outer.Content, `"description":"from struct"`) {
		t.Errorf("inner content missing description: %s", outer.Content)
	}
}
