package acosmi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- 测试辅助 ----------

// memStore 纯内存 TokenStore, 用于 httptest 场景。
type memStore struct {
	mu sync.Mutex
	t  *TokenSet
}

func (m *memStore) Load() (*TokenSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t, nil
}
func (m *memStore) Save(t *TokenSet) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t = t
	return nil
}
func (m *memStore) Clear() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t = nil
	return nil
}

// newTestClient 构造指向 httptest server 的 Client, token 永不过期。
func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	tok := &TokenSet{
		AccessToken:  "test-token",
		RefreshToken: "test-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
		ClientID:     "test-client",
		ServerURL:    serverURL,
	}
	c, err := NewClient(Config{
		ServerURL: serverURL,
		Store:     &memStore{t: tok},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// ---------- S-10: Stream 中途 ctx cancel 正确关闭 ----------

func TestChatMessagesStream_CtxCancelClosesChannels(t *testing.T) {
	// 服务端发一个事件后 block 住, 直到客户端断开。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter should be Flusher")
			return
		}
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m_1\"}}\n\n"))
		flusher.Flush()

		// Block 直到客户端 ctx 取消导致连接断开
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	evCh, errCh := c.ChatMessagesStream(ctx, "m-1", ChatRequest{
		RawMessages: []any{map[string]any{"role": "user", "content": "hi"}},
	})

	// 先收一个事件, 确认连接建立
	select {
	case ev, ok := <-evCh:
		if !ok {
			t.Fatal("eventCh closed before any event")
		}
		if ev.Event != "message_start" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case err := <-errCh:
		t.Fatalf("unexpected err: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("no event received within 3s")
	}

	// 取消 ctx
	cancel()

	// eventCh 必须在合理时间内关闭 (goroutine 退出 + defer close)
	deadline := time.After(5 * time.Second)
	drained := false
	for !drained {
		select {
		case _, ok := <-evCh:
			if !ok {
				drained = true
			}
		case <-deadline:
			t.Fatal("eventCh not closed after cancel (goroutine leak)")
		}
	}

	// errCh 也应关闭 (可能带或不带 err, 均可)
	select {
	case <-errCh:
		// drained ok
	case <-time.After(1 * time.Second):
		t.Fatal("errCh not closed after cancel")
	}
}

// ---------- S-12: 并发 ChatMessagesStream 无共享状态竞争 ----------

func TestChatMessagesStream_Concurrent(t *testing.T) {
	// 服务端每次请求都快速完结 (start + block_start text + stop)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		events := []string{
			`event: message_start
data: {"type":"message_start","message":{"id":"m_1"}}

`,
			`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

`,
			`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}

`,
			`event: content_block_stop
data: {"type":"content_block_stop","index":0}

`,
			`event: message_stop
data: {"type":"message_stop"}

`,
		}
		for _, ev := range events {
			if _, err := w.Write([]byte(ev)); err != nil {
				return
			}
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	const N = 20
	var wg sync.WaitGroup
	errsCh := make(chan error, N*2)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			evCh, errCh := c.ChatMessagesStream(ctx, fmt.Sprintf("m-%d", idx), ChatRequest{
				RawMessages: []any{map[string]any{"role": "user", "content": "hi"}},
			})

			gotStart, gotStop := false, false
			for ev := range evCh {
				switch ev.Event {
				case "message_start":
					gotStart = true
				case "message_stop":
					gotStop = true
				}
			}
			// errCh 可能有 scanner err (connection close 正常结束也可能有), 容忍。
			select {
			case e := <-errCh:
				if e != nil && !strings.Contains(e.Error(), "EOF") {
					errsCh <- fmt.Errorf("goroutine %d: %w", idx, e)
				}
			default:
			}
			if !gotStart {
				errsCh <- fmt.Errorf("goroutine %d: no message_start received", idx)
			}
			if !gotStop {
				errsCh <- fmt.Errorf("goroutine %d: no message_stop received", idx)
			}
		}(i)
	}

	wg.Wait()
	close(errsCh)
	for e := range errsCh {
		t.Error(e)
	}
}

// ---------- S-10 变体: 同时验证 BlockIndex/BlockType 在并发下无污染 ----------

func TestChatMessagesStream_ConcurrentBlockMetaIsolated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		// 三种 block type 穿插, 检验 map 隔离
		events := []string{
			`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

`,
			`event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"thinking","acosmi_ephemeral":true}}

`,
			`event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"thinking_delta"}}

`,
			`event: content_block_stop
data: {"type":"content_block_stop","index":1}

`,
			`event: content_block_stop
data: {"type":"content_block_stop","index":0}

`,
			`event: message_stop
data: {"type":"message_stop"}

`,
		}
		for _, ev := range events {
			_, _ = w.Write([]byte(ev))
			flusher.Flush()
		}
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)

	const N = 10
	var wg sync.WaitGroup
	failures := make(chan string, N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			evCh, _ := c.ChatMessagesStream(ctx, "m-x", ChatRequest{
				RawMessages: []any{map[string]any{"role": "user", "content": "hi"}},
			})

			// 记下每个 content_block_start 的 index→(type, ephemeral)
			seen := map[int][2]any{}
			for ev := range evCh {
				switch ev.Event {
				case "content_block_start":
					seen[ev.BlockIndex] = [2]any{ev.BlockType, ev.Ephemeral}
				case "content_block_delta", "content_block_stop":
					exp, ok := seen[ev.BlockIndex]
					if !ok {
						failures <- fmt.Sprintf("delta/stop for unknown index %d", ev.BlockIndex)
						return
					}
					if ev.BlockType != exp[0] || ev.Ephemeral != exp[1] {
						failures <- fmt.Sprintf("delta/stop meta drift idx=%d got(%v,%v) want(%v,%v)",
							ev.BlockIndex, ev.BlockType, ev.Ephemeral, exp[0], exp[1])
						return
					}
				}
			}
			if len(seen) != 2 {
				failures <- fmt.Sprintf("expected 2 blocks seen, got %d", len(seen))
			}
		}()
	}
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Error(msg)
	}
}
