package acosmi

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// v0.15.1 regression: ensureToken 必须区分三种状态
//   1. tokens 已就绪              → 立即返回
//   2. tokens=nil, Login 进行中   → 等待 tokenReady (修复点)
//   3. tokens=nil, Login 未启动   → fail-fast (保留旧行为)

// EnsureTokenWait_FailFast 用户忘了 Login → 立即报错, 错误信息保留 "call Login() first"
func TestEnsureTokenWait_FailFast(t *testing.T) {
	c, err := NewClient(Config{ServerURL: "http://127.0.0.1:0", Store: &memStore{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err = c.ensureToken(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "call Login() first") {
		t.Fatalf("expected 'call Login() first' message, got: %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected fail-fast (<100ms), got %v", elapsed)
	}
}

// EnsureTokenWait_Concurrent Login 并发中 → ensureToken 阻塞等到 token 就绪, 不再误报
func TestEnsureTokenWait_Concurrent(t *testing.T) {
	c, err := NewClient(Config{ServerURL: "http://127.0.0.1:0", Store: &memStore{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// 模拟 Login 进行中: 手动设置 loginInFlight, 50ms 后 close tokenReady
	c.mu.Lock()
	c.loginInFlight = true
	c.mu.Unlock()

	go func() {
		time.Sleep(50 * time.Millisecond)
		c.mu.Lock()
		c.tokens = &TokenSet{
			AccessToken: "wait-test",
			ExpiresAt:   time.Now().Add(time.Hour),
		}
		c.loginInFlight = false
		c.mu.Unlock()
		c.tokenOnce.Do(func() { close(c.tokenReady) })
	}()

	// 4 个并发 ensureToken — 模拟用户痛点 (4 调用者)
	var (
		wg      sync.WaitGroup
		errors_ atomic.Int32
		oks     atomic.Int32
	)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := c.ensureToken(ctx)
			if err != nil {
				errors_.Add(1)
				return
			}
			if tok == "wait-test" {
				oks.Add(1)
			}
		}()
	}
	wg.Wait()

	if errors_.Load() != 0 {
		t.Fatalf("expected 0 errors, got %d", errors_.Load())
	}
	if oks.Load() != 4 {
		t.Fatalf("expected 4 successful waits, got %d", oks.Load())
	}
}

// EnsureTokenWait_CtxDone Login 进行中但 ctx 先超时 → 报 context deadline 错, 不挂死
func TestEnsureTokenWait_CtxDone(t *testing.T) {
	c, err := NewClient(Config{ServerURL: "http://127.0.0.1:0", Store: &memStore{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.mu.Lock()
	c.loginInFlight = true // 永不结束的 Login
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = c.ensureToken(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected ctx error, got nil")
	}
	if !strings.Contains(err.Error(), "waiting for token") {
		t.Fatalf("expected 'waiting for token' wrap, got: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded chain, got: %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("ctx should fire ~50ms, took %v", elapsed)
	}
}

// EnsureTokenWait_PreloadedToken NewClient 加载已存 token → tokenReady 已 close, 零等待
func TestEnsureTokenWait_PreloadedToken(t *testing.T) {
	tok := &TokenSet{
		AccessToken: "preloaded",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	c, err := NewClient(Config{ServerURL: "http://127.0.0.1:0", Store: &memStore{t: tok}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	start := time.Now()
	got, err := c.ensureToken(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ensureToken: %v", err)
	}
	if got != "preloaded" {
		t.Fatalf("got %q, want preloaded", got)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("preloaded token must skip wait, took %v", elapsed)
	}
}

// EnsureTokenWait_LogoutResetsChannel Logout 后 fail-fast 恢复, 第二次 Login 仍能等待
func TestEnsureTokenWait_LogoutResetsChannel(t *testing.T) {
	tok := &TokenSet{
		AccessToken: "first",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	c, err := NewClient(Config{ServerURL: "http://127.0.0.1:0", Store: &memStore{t: tok}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// 模拟 Logout (避免真实 OIDC discover 调用): 直接走结构清零
	c.mu.Lock()
	c.tokens = nil
	c.tokenReady = make(chan struct{})
	c.tokenOnce = sync.Once{}
	c.mu.Unlock()

	// Logout 后未发起新 Login → fail-fast
	ctx1, cancel1 := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel1()
	_, err = c.ensureToken(ctx1)
	if err == nil || !strings.Contains(err.Error(), "call Login() first") {
		t.Fatalf("expected fail-fast after Logout, got: %v", err)
	}

	// 二次 Login 模拟: loginInFlight=true 再 close
	c.mu.Lock()
	c.loginInFlight = true
	c.mu.Unlock()
	go func() {
		time.Sleep(30 * time.Millisecond)
		c.mu.Lock()
		c.tokens = &TokenSet{AccessToken: "second", ExpiresAt: time.Now().Add(time.Hour)}
		c.loginInFlight = false
		c.mu.Unlock()
		c.tokenOnce.Do(func() { close(c.tokenReady) })
	}()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	got, err := c.ensureToken(ctx2)
	if err != nil {
		t.Fatalf("second ensureToken: %v", err)
	}
	if got != "second" {
		t.Fatalf("expected second token, got %q", got)
	}
}

// EnsureTokenWait_LoginLogoutRace 模拟 loginInternal step 5 与 Logout 重置并发
// 复审计发现的 race: tokenOnce/tokenReady 必须在同一锁内由 Login close 与 Logout 重置串行
// 该用例必须在 -race 下绿
func TestEnsureTokenWait_LoginLogoutRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		c, err := NewClient(Config{ServerURL: "http://127.0.0.1:0", Store: &memStore{}})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		c.mu.Lock()
		c.loginInFlight = true
		c.mu.Unlock()

		var wg sync.WaitGroup
		wg.Add(2)
		// Login 模拟器: 复制 step 5 单锁内 set+close 模式
		go func() {
			defer wg.Done()
			tokens := &TokenSet{AccessToken: "race", ExpiresAt: time.Now().Add(time.Hour)}
			c.mu.Lock()
			c.tokens = tokens
			c.tokenOnce.Do(func() { close(c.tokenReady) })
			c.mu.Unlock()
		}()
		// Logout 模拟器: 复制真实 Logout 重置
		go func() {
			defer wg.Done()
			c.mu.Lock()
			c.tokens = nil
			c.tokenReady = make(chan struct{})
			c.tokenOnce = sync.Once{}
			c.mu.Unlock()
		}()
		wg.Wait()
		// 不断言最终状态 (取决于调度), 仅依赖 -race 检测器
	}
}

// EnsureTokenWait_RaceWithLogout 等待方阻塞期间 Logout 重置 → 边界返回 not authorized, 不 panic
func TestEnsureTokenWait_RaceWithLogout(t *testing.T) {
	c, err := NewClient(Config{ServerURL: "http://127.0.0.1:0", Store: &memStore{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.mu.Lock()
	c.loginInFlight = true
	c.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_, err := c.ensureToken(ctx)
		done <- err
	}()

	// 让等待方先进入 select
	time.Sleep(20 * time.Millisecond)

	// 模拟极端边界: close 旧 channel 但不 set tokens (测试 nil-recheck 分支)
	c.mu.Lock()
	c.loginInFlight = false
	c.mu.Unlock()
	c.tokenOnce.Do(func() { close(c.tokenReady) })

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "call Login() first") {
			t.Fatalf("expected re-check fail-fast after spurious close, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ensureToken hung")
	}
}
