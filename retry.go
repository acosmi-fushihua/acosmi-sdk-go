// =============================================================================
// 文件: acosmi-sdk-go/retry.go
// L6 (2026-04-27, v0.15): SDK 内置 retry policy
//
// 设计目标:
//   1. GET 类查询 (skill-store/models/balance) 默认 2x retry, 稳定性提升
//   2. POST 类业务 (chat/messages/upload) 默认 0 retry — 计费安全红线 (双扣保护)
//   3. Stream 路径强制 MaxAttempts=1 — 流式 SSE 已部分写出, 重试 = 双 token 重复消息
//   4. 401 refresh 与 retry 互斥 — refresh 是 inner loop, 不算 attempt
//   5. ctx.Canceled 立即返回 — 用户 abort 不重试
//   6. *StreamError 显式排除 — V2 P0 SSE 中段错误不可重试
//
// 与 V2 P1 类型化协作:
//   - HTTPError 5xx/429 → 默认可重试
//   - NetworkError IsTimeout()/IsEOF() → 默认可重试
//   - 其他 → 不重试
//
// 调用方启用:
//   client, _ := NewClient(Config{
//       ServerURL: "...",
//       RetryPolicy: DefaultRetryPolicy,  // 或自定义
//   })
//
// 调用方禁用:
//   Config{RetryPolicy: nil}  // 退化到 v0.14.1 行为
// =============================================================================

package acosmi

import (
	"context"
	stderrors "errors"
	"net/http"
	"time"
)

// RetryPolicy 配置 SDK 重试行为.
//
// nil 字段使用 DefaultRetryPolicy 对应字段; nil RetryPolicy 自身禁用重试 (v0.14.1 行为).
type RetryPolicy struct {
	// MaxAttempts 总尝试次数 (含首次). 1 = 不重试; 默认 2.
	MaxAttempts int

	// Backoff 首次重试退避时长. 默认 200ms.
	Backoff time.Duration

	// BackoffMax 退避最大值 (指数增长封顶). 默认 2s.
	BackoffMax time.Duration

	// BackoffMul 指数倍数. 默认 2.0.
	BackoffMul float64

	// OnRetryable 错误层闸门: 是否值得重试.
	// 默认 defaultRetryable: HTTPError 5xx/429, NetworkError Timeout/EOF, 排除 *StreamError + ctx.Canceled.
	// 自定义可对特定 errkind/HTTPError.Type 做精细控制.
	OnRetryable func(error) bool

	// SafeToRetry 请求层闸门 (计费安全核心): 当前 *http.Request 是否值得重试.
	// 默认 defaultSafeToRetry: GET/HEAD/OPTIONS true, 其他 false.
	// chat/messages/upload POST → 默认 false 兜底, 双扣绝不发生.
	// 自定义可加白名单只读 POST 端点.
	SafeToRetry func(*http.Request) bool
}

// DefaultRetryPolicy 安全默认值.
//
// 计费安全红线: SafeToRetry 默认 POST=false → chat/messages 用户 0 行为变化.
// GET 类查询 (skill-store/models/balance) 自动得 2x 稳定性.
var DefaultRetryPolicy = &RetryPolicy{
	MaxAttempts: 2,
	Backoff:     200 * time.Millisecond,
	BackoffMax:  2 * time.Second,
	BackoffMul:  2.0,
	OnRetryable: defaultRetryable,
	SafeToRetry: defaultSafeToRetry,
}

// defaultSafeToRetry 计费安全闸门.
//
// 仅以下 method 视为幂等:
//   - GET / HEAD / OPTIONS: 天然幂等
//
// POST/PUT/DELETE/PATCH 默认 false (双扣保护). 调用方需要 GET 类 POST 重试可自定义 SafeToRetry.
func defaultSafeToRetry(req *http.Request) bool {
	if req == nil {
		return false
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// defaultRetryable 错误层闸门.
//
// 显式排除 (优先级最高):
//   - *StreamError: V2 P0 SSE 中段错误, 流已部分写出, 重试 = 双 token + 重复消息
//   - context.Canceled: 用户主动 abort, 重试无意义
//
// 视为可重试:
//   - *HTTPError 5xx 或 429
//   - *NetworkError IsTimeout() 或 IsEOF()
//
// 其他 (如 4xx 业务错误 / DNS 失败 / *BusinessError): 不重试
func defaultRetryable(err error) bool {
	if err == nil {
		return false
	}
	// 排除流式错误 (V2 P0)
	var se *StreamError
	if stderrors.As(err, &se) {
		return false
	}
	// 排除 ctx.Canceled (用户 abort, 但保留 ctx.DeadlineExceeded 经 NetworkError.Timeout 判)
	if stderrors.Is(err, context.Canceled) {
		return false
	}
	// HTTPError 5xx / 429
	var he *HTTPError
	if stderrors.As(err, &he) {
		return he.StatusCode >= 500 || he.StatusCode == http.StatusTooManyRequests
	}
	// NetworkError Timeout / EOF
	var ne *NetworkError
	if stderrors.As(err, &ne) {
		return ne.IsTimeout() || ne.IsEOF()
	}
	return false
}

// effectivePolicy 返回实际生效策略 (nil → DefaultRetryPolicy 兜底; 字段缺失填默认值).
//
// 内部 helper, 调用方 Config.RetryPolicy 可只设关心字段, 其余继承 default.
func effectivePolicy(p *RetryPolicy) *RetryPolicy {
	if p == nil {
		return nil // 显式 nil 表示禁用
	}
	out := *p // 拷贝
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = DefaultRetryPolicy.MaxAttempts
	}
	if out.Backoff <= 0 {
		out.Backoff = DefaultRetryPolicy.Backoff
	}
	if out.BackoffMax <= 0 {
		out.BackoffMax = DefaultRetryPolicy.BackoffMax
	}
	if out.BackoffMul <= 0 {
		out.BackoffMul = DefaultRetryPolicy.BackoffMul
	}
	if out.OnRetryable == nil {
		out.OnRetryable = defaultRetryable
	}
	if out.SafeToRetry == nil {
		out.SafeToRetry = defaultSafeToRetry
	}
	return &out
}

// retryAfterUpperBound Retry-After 头的硬上限 (60s) — 防止恶意服务器返回 Retry-After: 999999 卡死.
const retryAfterUpperBound = 60 * time.Second

// computeBackoff 计算第 attempt 次重试的退避时长 (attempt 从 0 起, 0 表示首次重试前的等待).
//
// 优先级: HTTPError.RetryAfter (上限 60s) > 指数退避 (Backoff * BackoffMul^attempt, 封顶 BackoffMax)
func computeBackoff(p *RetryPolicy, attempt int, err error) time.Duration {
	// Retry-After 头优先 (HTTPError 429 / 5xx)
	var he *HTTPError
	if stderrors.As(err, &he) && he.RetryAfter > 0 {
		d := time.Duration(he.RetryAfter) * time.Second
		if d > retryAfterUpperBound {
			return retryAfterUpperBound
		}
		return d
	}
	// 指数退避: Backoff * BackoffMul^attempt
	d := p.Backoff
	for i := 0; i < attempt; i++ {
		d = time.Duration(float64(d) * p.BackoffMul)
		if d > p.BackoffMax {
			return p.BackoffMax
		}
	}
	return d
}
