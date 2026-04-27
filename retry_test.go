// =============================================================================
// 文件: acosmi-sdk-go/retry_test.go
// 职责: L6 (v0.15) — RetryPolicy / SafeToRetry / OnRetryable / 指数退避 / 计费安全红线
// =============================================================================

package acosmi

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ----------------- defaultSafeToRetry -----------------

func TestDefaultSafeToRetry_Methods(t *testing.T) {
	cases := []struct {
		method string
		want   bool
	}{
		{http.MethodGet, true},
		{http.MethodHead, true},
		{http.MethodOptions, true},
		// POST/PUT/DELETE/PATCH 默认 false (计费安全红线)
		{http.MethodPost, false},
		{http.MethodPut, false},
		{http.MethodDelete, false},
		{http.MethodPatch, false},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			req, _ := http.NewRequest(tc.method, "http://x", nil)
			if got := defaultSafeToRetry(req); got != tc.want {
				t.Errorf("%s: got %v, want %v", tc.method, got, tc.want)
			}
		})
	}
}

func TestDefaultSafeToRetry_NilReq(t *testing.T) {
	if defaultSafeToRetry(nil) {
		t.Error("nil req should not be retryable")
	}
}

// ----------------- defaultRetryable -----------------

func TestDefaultRetryable_NilErr(t *testing.T) {
	if defaultRetryable(nil) {
		t.Error("nil err should not be retryable")
	}
}

func TestDefaultRetryable_StreamErrorExcluded(t *testing.T) {
	se := &StreamError{Code: "upstream_timeout", Message: "..."}
	if defaultRetryable(se) {
		t.Error("*StreamError 必须显式排除 (V2 P0 红线): SSE 中段错误重试 = 双 token")
	}
	// 即使 wrap 多层
	wrapped := fmt.Errorf("outer: %w", fmt.Errorf("middle: %w", se))
	if defaultRetryable(wrapped) {
		t.Error("wrapped *StreamError 也必须排除 (errors.As 链)")
	}
}

func TestDefaultRetryable_ContextCanceled(t *testing.T) {
	if defaultRetryable(context.Canceled) {
		t.Error("ctx.Canceled (用户 abort) 不应重试")
	}
	wrapped := fmt.Errorf("op: %w", context.Canceled)
	if defaultRetryable(wrapped) {
		t.Error("wrapped ctx.Canceled 也不应重试")
	}
}

func TestDefaultRetryable_HTTPError5xx429(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{
		{500, true}, {502, true}, {503, true}, {504, true},
		{429, true}, // rate limit
		// 4xx 业务错误不重试
		{400, false}, {401, false}, {403, false}, {404, false},
		// 2xx/3xx 不应进入此判断, 但保险起见
		{200, false}, {302, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("HTTP_%d", tc.status), func(t *testing.T) {
			err := &HTTPError{StatusCode: tc.status}
			if got := defaultRetryable(err); got != tc.want {
				t.Errorf("HTTP %d: got %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestDefaultRetryable_NetworkError(t *testing.T) {
	cases := []struct {
		name string
		ne   *NetworkError
		want bool
	}{
		{"timeout", &NetworkError{Timeout: true}, true},
		{"eof", &NetworkError{EOF: true}, true},
		{"both", &NetworkError{Timeout: true, EOF: true}, true},
		{"neither (DNS etc)", &NetworkError{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultRetryable(tc.ne); got != tc.want {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// ----------------- effectivePolicy -----------------

func TestEffectivePolicy_NilDisables(t *testing.T) {
	if effectivePolicy(nil) != nil {
		t.Error("nil policy 应当保持 nil (禁用重试)")
	}
}

func TestEffectivePolicy_PartialDefaults(t *testing.T) {
	// 只设 MaxAttempts, 其他应继承 default
	p := &RetryPolicy{MaxAttempts: 5}
	out := effectivePolicy(p)
	if out.MaxAttempts != 5 {
		t.Errorf("MaxAttempts: got %d", out.MaxAttempts)
	}
	if out.Backoff != DefaultRetryPolicy.Backoff {
		t.Errorf("Backoff should fallback to default")
	}
	if out.OnRetryable == nil {
		t.Error("OnRetryable should fallback to defaultRetryable")
	}
	if out.SafeToRetry == nil {
		t.Error("SafeToRetry should fallback to defaultSafeToRetry")
	}
}

// ----------------- computeBackoff -----------------

func TestComputeBackoff_Exponential(t *testing.T) {
	p := effectivePolicy(DefaultRetryPolicy)
	// attempt=0: 200ms; attempt=1: 400ms; attempt=2: 800ms; attempt=3: 1600ms; attempt=4: 2000ms (cap)
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 200 * time.Millisecond},
		{1, 400 * time.Millisecond},
		{2, 800 * time.Millisecond},
		{3, 1600 * time.Millisecond},
		{4, 2 * time.Second}, // cap
		{5, 2 * time.Second}, // cap
	}
	for _, tc := range cases {
		got := computeBackoff(p, tc.attempt, nil)
		if got != tc.want {
			t.Errorf("attempt=%d: got %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestComputeBackoff_RetryAfterPriority(t *testing.T) {
	p := effectivePolicy(DefaultRetryPolicy)
	he := &HTTPError{StatusCode: 429, RetryAfter: 5}
	got := computeBackoff(p, 0, he)
	want := 5 * time.Second
	if got != want {
		t.Errorf("RetryAfter 应当优先于指数退避: got %v, want %v", got, want)
	}
}

func TestComputeBackoff_RetryAfterUpperBound(t *testing.T) {
	p := effectivePolicy(DefaultRetryPolicy)
	// 服务器返回过长 Retry-After (3600s), 应当上限到 retryAfterUpperBound (60s)
	he := &HTTPError{StatusCode: 429, RetryAfter: 3600}
	got := computeBackoff(p, 0, he)
	if got > retryAfterUpperBound {
		t.Errorf("Retry-After 上限保护失效: got %v, max %v", got, retryAfterUpperBound)
	}
	if got != retryAfterUpperBound {
		t.Errorf("Retry-After 应当被截断为 60s, got %v", got)
	}
}

// ----------------- doRequestWithRetry 集成 -----------------

func TestDoRequestWithRetry_PolicyNilSingleCall(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{ServerURL: srv.URL, Store: &memStore{}, RetryPolicy: nil})
	req, _ := http.NewRequest("GET", srv.URL+"/x", nil)
	resp, _ := c.doRequestWithRetry(req, nil)
	if resp != nil {
		resp.Body.Close()
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("RetryPolicy=nil 应当 1 次调用 (老行为), 实际 %d", got)
	}
}

func TestDoRequestWithRetry_POSTNotRetried_BillingSafety(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{ServerURL: srv.URL, Store: &memStore{}, RetryPolicy: DefaultRetryPolicy})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/chat", nil)
	_, _ = c.doRequestWithRetry(req, nil)
	if got := calls.Load(); got != 1 {
		t.Errorf("POST 默认 SafeToRetry=false (计费安全), 应 1 次调用, 实际 %d", got)
	}
}

func TestDoRequestWithRetry_GETRetriedOn500(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := calls.Add(1)
		if c < 2 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		ServerURL: srv.URL,
		Store:     &memStore{},
		RetryPolicy: &RetryPolicy{
			MaxAttempts: 2,
			Backoff:     1 * time.Millisecond,
			BackoffMax:  2 * time.Millisecond,
			BackoffMul:  2.0,
		},
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/q", nil)
	resp, err := c.doRequestWithRetry(req, nil)
	if err != nil {
		t.Fatalf("expected success after retry: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("GET 503 应当 retry 1 次后成功 (总 2 调用), 实际 %d", got)
	}
}

func TestDoRequestWithRetry_GETStaysFailedAfterMax(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		ServerURL: srv.URL,
		Store:     &memStore{},
		RetryPolicy: &RetryPolicy{
			MaxAttempts: 3,
			Backoff:     1 * time.Millisecond,
			BackoffMax:  2 * time.Millisecond,
			BackoffMul:  2.0,
		},
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/q", nil)
	_, err := c.doRequestWithRetry(req, nil)
	if err == nil {
		t.Fatal("expected err after MaxAttempts exhausted")
	}
	var he *HTTPError
	if !stderrors.As(err, &he) {
		t.Fatalf("expected *HTTPError, got %T: %v", err, err)
	}
	if he.StatusCode != 503 {
		t.Errorf("StatusCode: got %d", he.StatusCode)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("MaxAttempts=3 应当 3 次调用, 实际 %d", got)
	}
}

func TestDoRequestWithRetry_GET4xxNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(404)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		ServerURL:   srv.URL,
		Store:       &memStore{},
		RetryPolicy: DefaultRetryPolicy,
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/q", nil)
	resp, err := c.doRequestWithRetry(req, nil)
	if err != nil {
		t.Fatalf("4xx 应当返回 resp 给 caller 解析, 不应包成 retry err: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("4xx 不应重试 (业务错误), 实际 %d 次", got)
	}
}

func TestDoRequestWithRetry_CtxCanceledStopsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		ServerURL: srv.URL,
		Store:     &memStore{},
		RetryPolicy: &RetryPolicy{
			MaxAttempts: 5,
			Backoff:     50 * time.Millisecond,
			BackoffMax:  100 * time.Millisecond,
			BackoffMul:  2.0,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/q", nil)
	_, err := c.doRequestWithRetry(req, nil)
	if err == nil {
		t.Fatal("ctx canceled 应当返回 err")
	}
}

func TestDoRequestWithRetry_CustomSafeToRetryOverride(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := calls.Add(1)
		if c < 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 自定义 SafeToRetry: POST /search 也算幂等
	custom := &RetryPolicy{
		MaxAttempts: 2,
		Backoff:     1 * time.Millisecond,
		BackoffMax:  2 * time.Millisecond,
		BackoffMul:  2.0,
		SafeToRetry: func(r *http.Request) bool {
			if r.Method == http.MethodPost && r.URL.Path == "/search" {
				return true
			}
			return defaultSafeToRetry(r)
		},
	}
	c, _ := NewClient(Config{ServerURL: srv.URL, Store: &memStore{}, RetryPolicy: custom})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/search", nil)
	resp, err := c.doRequestWithRetry(req, nil)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("custom SafeToRetry POST /search retry, expected 2 calls, got %d", got)
	}
}

func TestDoRequestWithRetry_RetryAfterHeader(t *testing.T) {
	var calls atomic.Int32
	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := calls.Add(1)
		if c == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{
		ServerURL: srv.URL,
		Store:     &memStore{},
		RetryPolicy: &RetryPolicy{
			MaxAttempts: 2,
			Backoff:     10 * time.Millisecond,
			BackoffMax:  20 * time.Millisecond,
			BackoffMul:  2.0,
		},
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/q", nil)
	resp, err := c.doRequestWithRetry(req, nil)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	elapsed := time.Since(start)
	// 应当至少等 1s (Retry-After)
	if elapsed < 900*time.Millisecond {
		t.Errorf("Retry-After=1 应当至少等 ~1s, 实际 %v", elapsed)
	}
}

func TestDoRequestWithRetry_BodyResetOnRetry(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if len(bodies) < 2 {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	// 自定义 POST /q 可重试
	custom := &RetryPolicy{
		MaxAttempts: 2,
		Backoff:     1 * time.Millisecond,
		BackoffMax:  2 * time.Millisecond,
		BackoffMul:  2.0,
		SafeToRetry: func(r *http.Request) bool { return true },
	}
	c, _ := NewClient(Config{ServerURL: srv.URL, Store: &memStore{}, RetryPolicy: custom})
	bodyBytes := []byte(`{"q":"hello"}`)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/q",
		// 首次 body 在 caller 设
		nil)
	req.Body = io.NopCloser(stringReader(bodyBytes))
	resp, err := c.doRequestWithRetry(req, bodyBytes)
	if err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Errorf("body reset 失败: first=%q second=%q", bodies[0], bodies[1])
	}
}

// ----------------- helpers -----------------

func stringReader(b []byte) io.Reader { return &bufReader{b: b} }

type bufReader struct {
	b []byte
	p int
}

func (r *bufReader) Read(p []byte) (int, error) {
	if r.p >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.p:])
	r.p += n
	return n, nil
}

// memStore 已在 client_stream_test.go 定义, 此处不重复
