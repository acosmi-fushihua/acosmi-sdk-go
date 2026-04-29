// =============================================================================
// 文件: bug_report.go | 模块: acosmi-sdk-go (v0.17.0)
//
// V30 CrabCode CLI bug 报告端点封装:
//   - POST /api/v4/crabcode_cli_feedback   — Bearer JWT (account scope), 限流 20/h/user
//   - GET  /api/v4/crabcode/bug/:bug_id    — 公开 (无 auth), 限流 60/min/IP
//
// 设计要点:
//   - reportData 用 any (调用方任意 JSON 可编码对象), 后端只解析为 map 用于脱敏 + 字段抽取,
//     不做严格 schema 校验 — 客户端 schema 会随版本变, SDK 不强 typed
//   - 服务端兜底脱敏 6 类正则 (anthropic-key/openai-key/github/aws/google/bearer),
//     调用方无须自行做密钥过滤
//   - 公开 GET 端点走 doPublicJSON, 无 token 也能调 (公开页 SSR / 维护者诊断 CLI 用)
//
// 文档: docs/guide.md §4.12
// =============================================================================

package acosmi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// BugReportResult POST /api/v4/crabcode_cli_feedback 返回体.
type BugReportResult struct {
	// FeedbackID 服务端生成的 UUID (写入 GitHub Issue body 用).
	FeedbackID string `json:"feedback_id"`

	// DetailURL 公开页链接, 形如 https://<base>/chat/crabcode/bug/<uuid>.
	// 维护者把它附在 GitHub Issue body 里, 跨租户/无登录可读.
	DetailURL string `json:"detail_url"`
}

// BugView GET /api/v4/crabcode/bug/:id 返回体 (公开 ViewModel).
//
// Errors / Transcript / Extras 用 []any / map[string]any: 客户端 reportData
// schema 会随版本变, SDK 不强 typed. 调用方按需做 type assertion.
type BugView struct {
	ID             string         `json:"id"`
	Description    string         `json:"description"`              // 已脱敏
	Platform       string         `json:"platform,omitempty"`       // darwin | linux | win32
	Terminal       string         `json:"terminal,omitempty"`       // iTerm.app / Terminal.app
	Version        string         `json:"version,omitempty"`        // CrabCode 版本号
	MessageCount   int            `json:"messageCount"`
	HasErrors      bool           `json:"hasErrors"`
	Status         string         `json:"status"`                   // new | triaging | fixed | wontfix
	ClientDatetime *time.Time     `json:"clientDatetime,omitempty"` // 客户端 reportData.datetime
	CreatedAt      time.Time      `json:"createdAt"`
	Errors         []any          `json:"errors,omitempty"`         // 已脱敏
	Transcript     []any          `json:"transcript,omitempty"`     // 已脱敏 (前端默认折叠)
	Extras         map[string]any `json:"extras,omitempty"`         // rawTranscriptJsonl / lastApiRequest 等非主字段
}

// SubmitBugReport 上报一份 CrabCode bug 报告.
//
// reportData 是任意 JSON 可编码对象 (map / struct), 后端只解析为 map 做脱敏 + 字段抽取,
// 不做严格 schema 校验. 服务端会对所有 string 叶子节点应用 secret-pattern 脱敏.
//
// 错误:
//   - *HTTPError 401 — token 过期 (doJSON 内部已做一次 refresh + retry, 仍 401 抛出)
//   - *HTTPError 403 Type="permission_error" Message 含 "Custom data retention settings"
//     — 用户所在组织 ZDR, 拒绝收集 (调用方应提示用户走外部渠道)
//   - *HTTPError 400 Type="invalid_request_error" — content 不是合法 JSON 或 reportData
//     编码失败
//   - *HTTPError 429 — 限流 20/h/user (RetryAfter 字段含建议等待秒数)
//   - *NetworkError — 传输层错误 (timeout / EOF / unreachable)
func (c *Client) SubmitBugReport(ctx context.Context, reportData any) (*BugReportResult, error) {
	if reportData == nil {
		return nil, errors.New("acosmi: reportData required")
	}

	// 客户端契约: 把整个 reportData 序列化后塞 request body 的 content 字段
	contentBytes, err := json.Marshal(reportData)
	if err != nil {
		return nil, fmt.Errorf("acosmi: marshal reportData: %w", err)
	}
	req := struct {
		Content string `json:"content"`
	}{Content: string(contentBytes)}

	var result BugReportResult
	if err := c.doJSON(ctx, http.MethodPost, "/crabcode_cli_feedback", req, &result, false); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetBugReport 取公开 ViewModel (无需 auth, 任意人凭 ID 可读).
//
// 用途: SSR 公开页后端 fetch / 维护者诊断 CLI / 集成测试.
//
// 错误:
//   - *HTTPError 404 — bug 不存在或被软删
//   - *HTTPError 429 — 限流 60/min/IP
//   - *NetworkError — 传输层错误
func (c *Client) GetBugReport(ctx context.Context, bugID string) (*BugView, error) {
	bugID = strings.TrimSpace(bugID)
	if bugID == "" {
		return nil, errors.New("acosmi: bugID required")
	}
	var view BugView
	// 公开端点 — 不强制 token (账号系统未登录 / token 过期场景下也能查)
	if err := c.doPublicJSON(ctx, http.MethodGet, "/crabcode/bug/"+bugID, nil, &view); err != nil {
		return nil, err
	}
	return &view, nil
}
