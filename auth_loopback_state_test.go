package acosmi

// auth_loopback_state_test.go — 桌面 loopback OAuth state 全路径矩阵 (2026-08-15, 与 TS
// test/auth/desktop-loopback-state.test.ts 同契约)。
//
// 契约: /callback 的每一种形态 (成功 / OAuth error / 畸形) 一律先验 state —— 恰好一个且与
// 本次登录严格匹配; 缺失、重复、错值均以 state_mismatch 拒绝且不得结算成 auth_denied;
// 结算只取首发, 后续回调 (含洪泛) 不得二次结算或卡死 teardown。

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// startLoopbackAuthorize 启动 authorizeInternal (跳过浏览器), 返回回环 redirect_uri 与本次 state。
func startLoopbackAuthorize(t *testing.T, ctx context.Context) (resultCh chan struct {
	res *AuthorizeResult
	err error
}, redirectURI, state string, events *[]LoginEvent) {
	t.Helper()
	meta := &ServerMetadata{
		Issuer:                "https://acosmi.com",
		AuthorizationEndpoint: "https://acosmi.com/oauth/desktop/authorize",
		TokenEndpoint:         "https://acosmi.com/oauth/desktop/token",
	}
	var mu sync.Mutex
	evs := []LoginEvent{}
	events = &evs
	authURLCh := make(chan string, 1)
	cfg := &loginConfig{skipBrowser: true, handler: func(e LoginEvent) {
		mu.Lock()
		evs = append(evs, e)
		mu.Unlock()
		if e.Type == EventAuthURL && e.URL != "" {
			select {
			case authURLCh <- e.URL:
			default:
			}
		}
	}}

	resultCh = make(chan struct {
		res *AuthorizeResult
		err error
	}, 1)
	go func() {
		res, _, err := authorizeInternal(ctx, meta, "client-1", []string{"ai"}, cfg)
		resultCh <- struct {
			res *AuthorizeResult
			err error
		}{res, err}
	}()

	select {
	case raw := <-authURLCh:
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse auth url: %v", err)
		}
		redirectURI = u.Query().Get("redirect_uri")
		state = u.Query().Get("state")
		if redirectURI == "" || state == "" {
			t.Fatalf("auth url missing redirect_uri/state: %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive auth URL event")
	}
	return resultCh, redirectURI, state, events
}

func mustGet(t *testing.T, rawURL string) string {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("callback GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return string(buf[:n])
}

func awaitResult(t *testing.T, ch chan struct {
	res *AuthorizeResult
	err error
}) (res *AuthorizeResult, err error) {
	t.Helper()
	select {
	case r := <-ch:
		return r.res, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("authorize did not settle in time")
		return nil, nil
	}
}

func TestLoopbackAuthorize_SuccessWithExactState(t *testing.T) {
	ch, redirectURI, state, _ := startLoopbackAuthorize(t, context.Background())
	mustGet(t, redirectURI+"?code=good-code&state="+url.QueryEscape(state))
	res, err := awaitResult(t, ch)
	if err != nil || res == nil || res.Code != "good-code" {
		t.Fatalf("success path: res=%+v err=%v", res, err)
	}
}

func TestLoopbackAuthorize_MissingStateRejectedAsStateMismatch(t *testing.T) {
	ch, redirectURI, _, _ := startLoopbackAuthorize(t, context.Background())
	body := mustGet(t, redirectURI+"?code=attacker-code")
	if !strings.Contains(body, "授权失败") {
		t.Fatalf("failure page expected, got: %s", body)
	}
	_, err := awaitResult(t, ch)
	if err == nil || !strings.Contains(err.Error(), string(ErrStateMismatch)) ||
		!strings.Contains(err.Error(), "missing state") {
		t.Fatalf("want state_mismatch(missing state), got %v", err)
	}
}

func TestLoopbackAuthorize_DuplicateStateRejected_EvenWhenOneCorrect(t *testing.T) {
	ch, redirectURI, state, _ := startLoopbackAuthorize(t, context.Background())
	mustGet(t, redirectURI+"?code=attacker-code&state="+url.QueryEscape(state)+"&state=wrong")
	_, err := awaitResult(t, ch)
	if err == nil || !strings.Contains(err.Error(), "multiple state") {
		t.Fatalf("want state_mismatch(multiple state), got %v", err)
	}
}

func TestLoopbackAuthorize_WrongStateRejected(t *testing.T) {
	ch, redirectURI, _, _ := startLoopbackAuthorize(t, context.Background())
	mustGet(t, redirectURI+"?code=attacker-code&state=wrong-state")
	_, err := awaitResult(t, ch)
	if err == nil || !strings.Contains(err.Error(), string(ErrStateMismatch)) {
		t.Fatalf("want state_mismatch, got %v", err)
	}
}

// OAuth error 回调不带 (有效) state → 必须按 state_mismatch 拒绝, 不得结算成 auth_denied:
// 否则本机任意进程零知识即可把等待中的登录塑形成"用户已拒绝"。
func TestLoopbackAuthorize_ErrorCallbackWithoutStateIsStateMismatchNotDenied(t *testing.T) {
	ch, redirectURI, _, events := startLoopbackAuthorize(t, context.Background())
	mustGet(t, redirectURI+"?error=access_denied&error_description=nope")
	_, err := awaitResult(t, ch)
	if err == nil || !strings.Contains(err.Error(), string(ErrStateMismatch)) {
		t.Fatalf("want state_mismatch, got %v", err)
	}
	if strings.Contains(err.Error(), "denied:") {
		t.Fatalf("must not settle as authorization denied: %v", err)
	}
	for _, e := range *events {
		if e.ErrCode == ErrAuthDenied {
			t.Fatalf("must not emit auth_denied for unauthenticated error callback: %+v", e)
		}
	}
}

// 用户真拒绝 (OAuth error + 正确 state) → auth_denied, 语义保留。
func TestLoopbackAuthorize_UserDenialWithValidStateIsAuthDenied(t *testing.T) {
	ch, redirectURI, state, _ := startLoopbackAuthorize(t, context.Background())
	mustGet(t, redirectURI+"?error=access_denied&error_description=user+said+no&state="+url.QueryEscape(state))
	_, err := awaitResult(t, ch)
	if err == nil || !strings.Contains(err.Error(), "authorization denied") {
		t.Fatalf("want authorization denied, got %v", err)
	}
}

// 恶意先到 + 洪泛: 首发结算后, 大量后续回调既不得二次结算、也不得卡死 handler/teardown
// (回归钉: errCh/codeCh 阻塞 send 会让 ≥3 发回调把 goroutine 卡死, server.Shutdown 永久悬挂)。
func TestLoopbackAuthorize_FloodOfBadCallbacksSettlesOnceAndTearsDown(t *testing.T) {
	ch, redirectURI, state, _ := startLoopbackAuthorize(t, context.Background())
	// 并发齐射 6 发坏回调: 首发结算后 listener 立即关闭, 余下请求要么被 handler 非阻塞丢弃、
	// 要么直接拒连 — 两者都是通过态; 只要 authorize 正常结算且不悬挂即证明无死锁。
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if resp, gerr := http.Get(fmt.Sprintf("%s?code=attacker-%d&state=wrong-%d", redirectURI, i, i)); gerr == nil {
				resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	_, err := awaitResult(t, ch)
	if err == nil || !strings.Contains(err.Error(), string(ErrStateMismatch)) {
		t.Fatalf("want state_mismatch, got %v", err)
	}
	// 迟到的合法回调不能复活已结算的登录; 端口应最终拒绝新连接 (teardown)。
	deadline := time.Now().Add(3 * time.Second)
	closed := false
	for time.Now().Before(deadline) {
		if _, gerr := http.Get(redirectURI + "?code=legit&state=" + url.QueryEscape(state)); gerr != nil {
			closed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !closed {
		t.Fatal("loopback listener still accepting connections after settle")
	}
}

func TestLoopbackAuthorize_ContextCancelSettlesAsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, redirectURI, _, events := startLoopbackAuthorize(t, ctx)
	cancel()
	_, err := awaitResult(t, ch)
	if err == nil {
		t.Fatal("want context error, got nil")
	}
	sawTimeout := false
	for _, e := range *events {
		if e.ErrCode == ErrTimeout {
			sawTimeout = true
		}
	}
	if !sawTimeout {
		t.Fatal("cancel path must emit auth_timeout event")
	}
	_ = redirectURI
}
