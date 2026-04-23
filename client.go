package acosmi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/acosmi/acosmi-sdk-go/sanitize"
)

// 安全限制常量
const (
	maxDownloadSize   = 50 << 20 // 50MB — 技能 ZIP 包最大下载体积
	maxErrorBodySize  = 1 << 20  // 1MB — 错误响应体最大读取量
	maxSSELineSize    = 1 << 20  // 1MB — SSE 单行最大长度 (大 JSON chunk)
)

// Client Acosmi nexus-v4 统一 API 客户端
// 覆盖全域 API: 模型/权益/商城/钱包/技能/工具/WebSocket
// 自动处理 token 刷新，所有 API 调用线程安全
type Client struct {
	serverURL string
	meta      *ServerMetadata
	tokens    *TokenSet
	store     TokenStore
	http      *http.Client
	mu        sync.RWMutex
	ws        *wsState // WebSocket 长连接状态 (nil = 未连接)

	// 模型能力缓存 (CrabCode 扩展)
	modelCache     []ManagedModel // ListModels 缓存
	modelCacheTime time.Time      // 缓存写入时间

	// v0.11.0: 请求前防御钩子。nil = 未启用, 零开销。
	defensiveCfg       *sanitize.MinimalSanitizeConfig
	autoStripEphemeral bool
}

// Config 客户端配置
type Config struct {
	// ServerURL nexus-v4 API 根地址 (默认 https://acosmi.com)。
	// SDK 自动追加 /api/v4, 无需手动拼接。
	// 国际站显式传 https://acosmi.ai, 本地开发传 http://127.0.0.1:3300。
	ServerURL string

	// Store token 持久化实现，nil 则使用默认文件存储 (~/.acosmi/tokens.json)
	Store TokenStore

	// HTTPClient 自定义 HTTP 客户端，nil 则使用默认
	HTTPClient *http.Client
}

// NewClient 创建客户端 (自动加载已保存的 token)
func NewClient(cfg Config) (*Client, error) {
	if cfg.ServerURL == "" {
		cfg.ServerURL = "https://acosmi.com"
	}

	store := cfg.Store
	if store == nil {
		var storeErr error
		store, storeErr = NewFileTokenStore("")
		if storeErr != nil {
			return nil, fmt.Errorf("init token store: %w", storeErr)
		}
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		// [RC-3] 不设全局 Timeout — 全局 Timeout 含 body 读取,
		// 会截断 SSE 流式聊天和大文件下载。改为 per-request context timeout。
		httpClient = &http.Client{}
	}

	c := &Client{
		serverURL: strings.TrimRight(cfg.ServerURL, "/"),
		store:     store,
		http:      httpClient,
	}

	// 尝试加载已保存的 token
	if tokens, err := store.Load(); err == nil && tokens != nil {
		c.tokens = tokens
	}

	return c, nil
}

// ============================================================================
// 授权生命周期
// ============================================================================

// IsAuthorized 是否已授权 (有可用 token)
func (c *Client) IsAuthorized() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokens != nil
}

// getCachedClientID 获取缓存的 client_id (来自上次登录)
func (c *Client) getCachedClientID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.tokens != nil {
		return c.tokens.ClientID
	}
	return ""
}

// Login 完整授权流程: 发现 → 注册 → 授权 → 换 token → 持久化
// appName: 桌面智能体名称 (如 "CrabClaw Desktop")
// scopes: 请求的权限范围 (参考 AllScopes / ModelScopes / CommerceScopes 等预设)
//
// 签名不变 — CrabClaw 零影响。内部委托 loginInternal。
func (c *Client) Login(ctx context.Context, appName string, scopes []string) error {
	return c.loginInternal(ctx, appName, scopes, nil)
}

// LoginWithHandler 带事件回调的登录流程 — CrabCode 使用
//
// handler 在以下时刻被调用：
//   - EventAuthURL:  授权 URL 已就绪，调用方可展示/打开浏览器
//   - EventComplete: 登录成功，tokens 已持久化
//   - EventError:    某步骤失败，附 ErrCode 分类码
//
// opts 可选：WithSkipBrowser() 控制是否跳过自动打开浏览器。
// 当 handler 为 nil 时，行为与 Login() 完全一致。
func (c *Client) LoginWithHandler(ctx context.Context, appName string, scopes []string, handler func(LoginEvent), opts ...LoginOption) error {
	cfg := &loginConfig{handler: handler}
	for _, opt := range opts {
		opt(cfg)
	}
	return c.loginInternal(ctx, appName, scopes, cfg)
}

// loginInternal 共享实现
func (c *Client) loginInternal(ctx context.Context, appName string, scopes []string, cfg *loginConfig) error {
	if cfg == nil {
		cfg = &loginConfig{}
	}
	emit := func(e LoginEvent) {
		if cfg.handler != nil {
			cfg.handler(e)
		}
	}
	emitError := func(code LoginErrCode, err error) {
		emit(LoginEvent{Type: EventError, ErrCode: code, Error: err.Error()})
	}

	// 1. 发现
	meta, err := Discover(ctx, c.serverURL)
	if err != nil {
		emitError(ErrDiscovery, err)
		return fmt.Errorf("discovery failed: %w", err)
	}
	// [RC-5] 持锁写入 c.meta, 防止与 ensureToken/forceRefresh 读取产生数据竞争
	c.mu.Lock()
	c.meta = meta
	c.mu.Unlock()

	// 2. 检查是否已有 client_id; 无则注册
	clientID := c.getCachedClientID()

	if clientID == "" {
		reg, regErr := Register(ctx, meta, appName)
		if regErr != nil {
			emitError(ErrRegistration, regErr)
			return fmt.Errorf("registration failed: %w", regErr)
		}
		clientID = reg.ClientID
	}

	// 3. 授权 (PKCE + browser + callback)
	result, verifier, err := authorizeInternal(ctx, meta, clientID, scopes, cfg)
	if err != nil {
		// 授权失败 (可能是服务器重启后 client_id 失效):
		// 清除缓存的 client_id, 重新注册, 再试一次
		reg, regErr := Register(ctx, meta, appName)
		if regErr != nil {
			emitError(ErrRegistration, regErr)
			return fmt.Errorf("authorization failed (retry registration also failed): %w", err)
		}
		clientID = reg.ClientID

		result, verifier, err = authorizeInternal(ctx, meta, clientID, scopes, cfg)
		if err != nil {
			return fmt.Errorf("authorization failed: %w", err)
		}
	}

	// 4. 换 token（审计 A-2 修复: 支持自定义 expiresIn）
	var tokenResp *TokenResponse
	if cfg.expiresIn > 0 {
		tokenResp, err = exchangeCodeWithExpiry(ctx, meta, clientID, result.Code, result.RedirectURI, verifier, cfg.expiresIn)
	} else {
		tokenResp, err = ExchangeCode(ctx, meta, clientID, result.Code, result.RedirectURI, verifier)
	}
	if err != nil {
		code := ErrTokenExchange
		if isSSLError(err) {
			code = ErrSSLProxy
		}
		emitError(code, err)
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// 5. 持久化
	tokens := NewTokenSet(tokenResp, clientID, c.serverURL)
	c.mu.Lock()
	c.tokens = tokens
	c.mu.Unlock()

	if err := c.store.Save(tokens); err != nil {
		return fmt.Errorf("save tokens: %w", err)
	}

	// 6. 完成
	emit(LoginEvent{Type: EventComplete})
	return nil
}

// Logout 吊销 token 并清除本地存储
// [RC-4] meta==nil 时先 Discover 获取 revocation endpoint, 确保服务端 token 也被撤销
func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	tokens := c.tokens
	meta := c.meta
	c.tokens = nil
	c.meta = nil
	c.mu.Unlock()

	if tokens != nil {
		if meta == nil {
			// Lazy discover for revocation endpoint
			discovered, discErr := Discover(ctx, c.serverURL)
			if discErr != nil {
				fmt.Printf("[acosmi-sdk] warning: discover for revocation failed: %v\n", discErr)
			} else {
				meta = discovered
			}
		}
		if meta != nil {
			_ = RevokeToken(ctx, meta, tokens.AccessToken)
			_ = RevokeToken(ctx, meta, tokens.RefreshToken)
		}
	}

	return c.store.Clear()
}

// GetTokenSet 返回当前 token 信息 (用于 CLI whoami 显示)
func (c *Client) GetTokenSet() *TokenSet {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tokens
}

// ============================================================================
// Token 管理
// ============================================================================

// ensureToken 确保有有效的 access_token，过期则自动刷新
func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	tokens := c.tokens
	c.mu.RUnlock()

	if tokens == nil {
		return "", fmt.Errorf("not authorized, call Login() first")
	}

	if !tokens.IsExpired() {
		return tokens.AccessToken, nil
	}

	// 需要刷新
	c.mu.Lock()
	defer c.mu.Unlock()

	// 根因修复 #2: 双检锁中 c.tokens 可能已被 Logout() 置 nil → panic
	// 必须先检查 nil, 再检查过期
	if c.tokens == nil {
		return "", fmt.Errorf("not authorized, call Login() first")
	}
	if !c.tokens.IsExpired() {
		return c.tokens.AccessToken, nil
	}

	if c.meta == nil {
		meta, err := Discover(ctx, c.serverURL)
		if err != nil {
			return "", fmt.Errorf("discover for refresh: %w", err)
		}
		c.meta = meta
	}

	tokenResp, err := RefreshToken(ctx, c.meta, c.tokens.ClientID, c.tokens.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("refresh token: %w", err)
	}

	c.tokens = NewTokenSet(tokenResp, c.tokens.ClientID, c.serverURL)
	if err := c.store.Save(c.tokens); err != nil {
		fmt.Printf("[acosmi-sdk] warning: save refreshed token failed: %v\n", err)
	}

	return c.tokens.AccessToken, nil
}

// ============================================================================
// API: Managed Models (scope: models / models:chat)
// ============================================================================

// modelCacheTTL 模型列表缓存有效期
const modelCacheTTL = 5 * time.Minute

// ListModels 获取可用的托管模型列表
func (c *Client) ListModels(ctx context.Context) ([]ManagedModel, error) {
	var resp APIResponse[[]ManagedModel]
	if err := c.doJSON(ctx, http.MethodGet, "/managed-models", nil, &resp, false); err != nil {
		return nil, err
	}
	// 写入模型缓存 (供 getCachedCapabilities / GetModelCapabilities 使用)
	c.mu.Lock()
	c.modelCache = resp.Data
	c.modelCacheTime = time.Now()
	c.mu.Unlock()
	return resp.Data, nil
}

// GetModelCapabilities 查询单个模型的能力矩阵
// 优先从 ListModels 缓存读取, miss 时调用 ListModels 刷新
func (c *Client) GetModelCapabilities(ctx context.Context, modelID string) (*ModelCapabilities, error) {
	// 先尝试缓存
	if caps, ok := c.getCachedCapabilities(modelID); ok {
		return &caps, nil
	}
	// 缓存 miss: 刷新
	if _, err := c.ListModels(ctx); err != nil {
		return nil, fmt.Errorf("get model capabilities: %w", err)
	}
	if caps, ok := c.getCachedCapabilities(modelID); ok {
		return &caps, nil
	}
	// 模型不在列表中, 返回零值
	empty := ModelCapabilities{}
	return &empty, nil
}

// getCachedCapabilities 从缓存中查找模型能力 (线程安全)
func (c *Client) getCachedCapabilities(modelID string) (ModelCapabilities, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.modelCache == nil || time.Since(c.modelCacheTime) > modelCacheTTL {
		return ModelCapabilities{}, false
	}
	for _, m := range c.modelCache {
		if m.ID == modelID || m.ModelID == modelID {
			return m.Capabilities, true
		}
	}
	return ModelCapabilities{}, false
}

// [RC-2] GetModelUsage 已移除: /managed-models/usage 端点已迁移至 tk-dist 营销系统

// getCachedModel 从缓存中查找完整 ManagedModel (线程安全)。
// 未命中时返回 (零值, false), 不再硬编码 anthropic 占位。
// 调用方应用 ensureModelCached 触发 ListModels 刷新后再查。
func (c *Client) getCachedModel(modelID string) (ManagedModel, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.modelCache {
		if m.ID == modelID || m.ModelID == modelID {
			return m, true
		}
	}
	return ManagedModel{}, false
}

// primeModelCacheForTest 测试辅助: 把占位 ManagedModel 塞入缓存, 避免测试触发 ListModels。
// 仅同包 (测试) 调用, 不暴露给 SDK 使用者。非测试代码请走 ListModels。
func (c *Client) primeModelCacheForTest(ids ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range ids {
		c.modelCache = append(c.modelCache, ManagedModel{
			ID:       id,
			ModelID:  id,
			Provider: "anthropic", // 测试默认 anthropic 格式 (保持历史行为)
		})
	}
	c.modelCacheTime = time.Now()
}

// ensureModelCached 确保指定 modelID 的 ManagedModel 已在缓存中。
//
// 语义:
//  1. 若缓存命中 → 直接返回
//  2. 若未命中 → 调 ListModels(ctx) 刷新一次
//  3. 刷新后仍未命中 → 返回 *ModelNotFoundError
//
// 根因修复 (Finding 2): 消除未预热场景下 Provider="anthropic" 硬编码回退,
// 该回退会让 DashScope/Zhipu/DeepSeek 等 non-anthropic 模型被按 Anthropic
// 格式编码并打到错误的 /anthropic 端点。
//
// 幂等: 并发调用安全 (ListModels 内部有写锁; 同 modelID 可能产生多次刷新,
// 但不会破坏状态, 且首次刷新后后续调用立即命中)。
func (c *Client) ensureModelCached(ctx context.Context, modelID string) (ManagedModel, error) {
	if m, ok := c.getCachedModel(modelID); ok {
		return m, nil
	}
	// 未命中 — 刷新一次
	if _, err := c.ListModels(ctx); err != nil {
		return ManagedModel{}, fmt.Errorf("ensure model cached: refresh list failed: %w", err)
	}
	if m, ok := c.getCachedModel(modelID); ok {
		return m, nil
	}
	return ManagedModel{}, &ModelNotFoundError{ModelID: modelID}
}

// buildChatRequest 构建完整的聊天请求体（v0.5.0 adapter 模式）
//
// 根据 provider 选择 adapter，委托 BuildRequestBody 构建格式化的请求体。
// Anthropic provider → AnthropicAdapter（含 betas/ServerTools）
// 其他 provider     → OpenAIAdapter（无 betas，扩展字段透传）
//
// v0.13.x: 前置 ensureModelCached, 消除冷缓存硬编码回退。未知 modelID 返回 *ModelNotFoundError。
//
// 返回: (请求体 JSON, 使用的 adapter, 错误)
func (c *Client) buildChatRequest(ctx context.Context, modelID string, req *ChatRequest) ([]byte, ProviderAdapter, error) {
	// v0.11.0: 请求前防御 (体积 / deny-list / 深度 / ephemeral 剥离)。未配置时零开销。
	if err := c.applyRequestSanitizers(req); err != nil {
		return nil, nil, err
	}

	m, err := c.ensureModelCached(ctx, modelID)
	if err != nil {
		return nil, nil, err
	}
	adapter := getAdapterForModel(m)
	caps, _ := c.getCachedCapabilities(modelID)

	body, err := adapter.BuildRequestBody(caps, req)
	if err != nil {
		return nil, nil, err
	}

	data, err := json.Marshal(body)
	return data, adapter, err
}

// Chat 同步聊天 (适合短回复)
// 响应的 TokenRemaining / CallRemaining 字段来自服务端 Header，反映结算后余额
// v0.5.0: 根据 provider 自动路由到 /anthropic 或 /chat 端点
func (c *Client) Chat(ctx context.Context, modelID string, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	// Chat 请求可能 30-120s+，使用 5 分钟超时而非默认 30s
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}

	// 使用 adapter 构建请求
	body, adapter, err := c.buildChatRequest(ctx, modelID, &req)
	if err != nil {
		return nil, fmt.Errorf("build chat request: %w", err)
	}

	// 根据 adapter 选择端点
	endpoint := "/managed-models/" + url.PathEscape(modelID) + adapter.EndpointSuffix()

	var raw json.RawMessage
	headers, err := c.doJSONFull(ctx, http.MethodPost, endpoint, json.RawMessage(body), &raw)
	if err != nil {
		return nil, err
	}

	// 使用 adapter 解析响应
	resp, err := adapter.ParseResponse(raw)
	if err != nil {
		return nil, err
	}

	// 从 Header 提取 token 余额
	if v := headers.Get("X-Token-Remaining"); v != "" {
		if n, parseErr := strconv.ParseInt(v, 10, 64); parseErr == nil {
			resp.TokenRemaining = n
		}
	}
	if v := headers.Get("X-Call-Remaining"); v != "" {
		if n, parseErr := strconv.Atoi(v); parseErr == nil {
			resp.CallRemaining = n
		}
	}
	return resp, nil
}

// ChatMessages Anthropic 原生格式同步聊天
// v0.5.0: 根据 provider 自动路由
//   Anthropic → chatMessagesAnthropic（现有路径，POST /anthropic）
//   其他厂商 → chatMessagesOpenAI（POST /chat，响应转换为 AnthropicResponse）
// v0.13.x: 前置 ensureModelCached, 消除冷缓存硬编码回退。未知 modelID 返回 *ModelNotFoundError。
func (c *Client) ChatMessages(ctx context.Context, modelID string, req ChatRequest) (*AnthropicResponse, error) {
	m, err := c.ensureModelCached(ctx, modelID)
	if err != nil {
		return nil, err
	}
	adapter := getAdapterForModel(m)

	if adapter.Format() == FormatAnthropic {
		return c.chatMessagesAnthropic(ctx, modelID, req, adapter)
	}
	return c.chatMessagesOpenAI(ctx, modelID, req, adapter)
}

// chatMessagesAnthropic Anthropic 原生格式同步聊天（现有逻辑）
// 调用 POST /managed-models/:id/anthropic
// 兼容两种响应格式: 裸 Anthropic JSON 或 {"code":0,"data":{...}} APIResponse 包装
func (c *Client) chatMessagesAnthropic(ctx context.Context, modelID string, req ChatRequest, adapter ProviderAdapter) (*AnthropicResponse, error) {
	req.Stream = false
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}

	caps, _ := c.getCachedCapabilities(modelID)
	body, err := adapter.BuildRequestBody(caps, &req)
	if err != nil {
		return nil, fmt.Errorf("build chat request: %w", err)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	// 用 json.RawMessage 接收原始 JSON，以兼容两种响应格式
	var raw json.RawMessage
	if _, err := c.doJSONFull(ctx, http.MethodPost, "/managed-models/"+url.PathEscape(modelID)+"/anthropic", json.RawMessage(data), &raw); err != nil {
		return nil, err
	}

	// 尝试 APIResponse 包装: {"code":0,"message":"...","data":{...}}
	var wrapper struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.Data) > 0 && string(wrapper.Data) != "null" {
		if wrapper.Code != 0 {
			return nil, &BusinessError{Code: wrapper.Code, Message: wrapper.Message}
		}
		var resp AnthropicResponse
		if err := json.Unmarshal(wrapper.Data, &resp); err != nil {
			return nil, fmt.Errorf("decode anthropic response from wrapper: %w", err)
		}
		return &resp, nil
	}

	// 裸 Anthropic JSON 直解
	var resp AnthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	return &resp, nil
}

// chatMessagesOpenAI 非 Anthropic 厂商同步聊天
// 走 OpenAI 格式端点，响应转换为 AnthropicResponse 返回
// 使 Hub 层无需感知 provider 差异
func (c *Client) chatMessagesOpenAI(ctx context.Context, modelID string, req ChatRequest, adapter ProviderAdapter) (*AnthropicResponse, error) {
	req.Stream = false
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}

	caps, _ := c.getCachedCapabilities(modelID)
	body, err := adapter.BuildRequestBody(caps, &req)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := "/managed-models/" + url.PathEscape(modelID) + adapter.EndpointSuffix()

	var raw json.RawMessage
	if _, err := c.doJSONFull(ctx, http.MethodPost, endpoint, json.RawMessage(data), &raw); err != nil {
		return nil, err
	}

	// 解析 OpenAI 格式响应并转换为 AnthropicResponse
	return parseOpenAIResponseToAnthropic(raw)
}

// ChatMessagesStream Anthropic 原生格式流式聊天 (SSE)
// 调用 POST /managed-models/:id/anthropic，SSE 事件为 Anthropic 协议格式
// 无 started/settled/failed 自定义事件，无 data: [DONE]，message_stop 为自然结束
func (c *Client) ChatMessagesStream(ctx context.Context, modelID string, req ChatRequest) (<-chan StreamEvent, <-chan error) {
	eventCh := make(chan StreamEvent, 32)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		defer close(errCh)
		c.chatMessagesStreamInternal(ctx, modelID, req, eventCh, errCh, false)
	}()

	return eventCh, errCh
}

// chatMessagesStreamInternal 流式内部实现
// v0.5.0: 根据 adapter 路由端点 + SSE 格式解析
//   Anthropic → /anthropic 端点，原生 SSE 事件直透
//   OpenAI    → /chat 端点，OpenAI SSE 转换为 Anthropic 兼容事件
func (c *Client) chatMessagesStreamInternal(ctx context.Context, modelID string, req ChatRequest,
	eventCh chan<- StreamEvent, errCh chan<- error, retried bool) {

	req.Stream = true
	body, adapter, buildErr := c.buildChatRequest(ctx, modelID, &req)
	if buildErr != nil {
		errCh <- buildErr
		return
	}

	token, err := c.ensureToken(ctx)
	if err != nil {
		errCh <- err
		return
	}

	// 根据 adapter 选择端点
	endpoint := "/managed-models/" + url.PathEscape(modelID) + adapter.EndpointSuffix()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiURL(endpoint),
		bytes.NewReader(body))
	if err != nil {
		errCh <- err
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		errCh <- err
		return
	}
	defer resp.Body.Close()

	// 401 单次重试
	if resp.StatusCode == http.StatusUnauthorized && !retried {
		resp.Body.Close()
		if refreshErr := c.forceRefresh(ctx); refreshErr != nil {
			errCh <- fmt.Errorf("messages stream: unauthorized and refresh failed: %w", refreshErr)
			return
		}
		c.chatMessagesStreamInternal(ctx, modelID, req, eventCh, errCh, true)
		return
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		errCh <- parseHTTPError(resp.StatusCode, bodyBytes)
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, maxSSELineSize), maxSSELineSize)

	if adapter.Format() == FormatOpenAI {
		// OpenAI SSE: 转换为 Anthropic 兼容事件
		converter := newOpenAIStreamConverter()
		var currentEvent string
		for scanner.Scan() {
			line := scanner.Text()
			if after, ok := strings.CutPrefix(line, "event:"); ok {
				currentEvent = strings.TrimSpace(after)
				_ = currentEvent // OpenAI SSE 通常没有 event: 行
			} else if after, ok := strings.CutPrefix(line, "data:"); ok {
				data := strings.TrimSpace(after)
				events, done, parseErr := converter.Convert(data)
				if parseErr != nil {
					errCh <- parseErr
					return
				}
				for _, evt := range events {
					eventCh <- evt
				}
				if done {
					return
				}
			}
		}
	} else {
		// Anthropic SSE: 原生事件直透 + v0.11.0 content block 元数据回填
		var currentEvent string
		blockTypeMap := make(map[int]blockMeta)
		for scanner.Scan() {
			line := scanner.Text()
			if after, ok := strings.CutPrefix(line, "event:"); ok {
				currentEvent = strings.TrimSpace(after)
			} else if after, ok := strings.CutPrefix(line, "data:"); ok {
				data := strings.TrimSpace(after)
				ev := StreamEvent{Event: currentEvent, Data: data}
				idx, bt, eph := extractAnthropicBlockMeta(currentEvent, data, blockTypeMap)
				if bt != "" {
					ev.BlockIndex = idx
					ev.BlockType = bt
					ev.Ephemeral = eph
				}
				eventCh <- ev
			}
		}
	}

	if err := scanner.Err(); err != nil {
		errCh <- err
	}
}

// ChatStream 流式聊天 (SSE)，通过 channel 返回事件
// 调用方应遍历 channel 直到关闭，errCh 报告非 nil 错误
func (c *Client) ChatStream(ctx context.Context, modelID string, req ChatRequest) (<-chan StreamEvent, <-chan error) {
	eventCh := make(chan StreamEvent, 32)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		defer close(errCh)
		c.chatStreamInternal(ctx, modelID, req, eventCh, errCh, false)
	}()

	return eventCh, errCh
}

// chatStreamInternal 流式聊天内部实现
// v0.5.0: 根据 adapter 路由端点
// 根因修复 #7: ChatStream 支持 401 单次重试
// 根因修复 #4: 错误响应体使用 LimitReader 防 OOM
// 根因修复 #13: SSE scanner 使用 1MB 缓冲区, 防 ErrTooLong
func (c *Client) chatStreamInternal(ctx context.Context, modelID string, req ChatRequest,
	eventCh chan<- StreamEvent, errCh chan<- error, retried bool) {

	req.Stream = true
	body, adapter, buildErr := c.buildChatRequest(ctx, modelID, &req)
	if buildErr != nil {
		errCh <- buildErr
		return
	}

	token, err := c.ensureToken(ctx)
	if err != nil {
		errCh <- err
		return
	}

	// 根据 adapter 选择端点
	endpoint := "/managed-models/" + url.PathEscape(modelID) + adapter.EndpointSuffix()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiURL(endpoint),
		bytes.NewReader(body))
	if err != nil {
		errCh <- err
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		errCh <- err
		return
	}
	defer resp.Body.Close()

	// 401 单次重试 (根因修复 #7)
	if resp.StatusCode == http.StatusUnauthorized && !retried {
		resp.Body.Close()
		if refreshErr := c.forceRefresh(ctx); refreshErr != nil {
			errCh <- fmt.Errorf("stream: unauthorized and refresh failed: %w", refreshErr)
			return
		}
		c.chatStreamInternal(ctx, modelID, req, eventCh, errCh, true)
		return
	}

	if resp.StatusCode != http.StatusOK {
		// 根因修复 #4: 限制错误响应体大小
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		errCh <- parseHTTPError(resp.StatusCode, bodyBytes)
		return
	}

	// 根因修复 #13: 扩大 SSE scanner 缓冲区到 1MB
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, maxSSELineSize), maxSSELineSize)

	// v0.11.0: Anthropic 格式下维护 index→meta 映射, OpenAI 无 block 概念跳过。
	var blockTypeMap map[int]blockMeta
	if adapter.Format() == FormatAnthropic {
		blockTypeMap = make(map[int]blockMeta)
	}

	var currentEvent string
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "event:"); ok {
			currentEvent = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "data:"); ok {
			data := strings.TrimSpace(after)
			// 使用 adapter 解析每行
			evt, done, parseErr := adapter.ParseStreamLine(currentEvent, data)
			if parseErr != nil {
				errCh <- parseErr
				return
			}
			if done {
				return
			}
			if blockTypeMap != nil {
				idx, bt, eph := extractAnthropicBlockMeta(currentEvent, data, blockTypeMap)
				if bt != "" {
					evt.BlockIndex = idx
					evt.BlockType = bt
					evt.Ephemeral = eph
				}
			}
			eventCh <- evt
		}
	}

	if err := scanner.Err(); err != nil {
		errCh <- err
	}
}

// ChatStreamWithUsage 流式聊天，自动解析结算事件和搜索来源
// 返回: contentCh (内容增量), sourcesCh (搜索来源), settleCh (结算), errCh (错误)
// contentCh 只包含内容增量事件 (过滤掉 started/settled/failed/sources)
// sourcesCh 在检测到搜索结果时发送来源列表 (可能多次, 每次搜索一批)
// settleCh 在流结束时发送结算信息 (包含 token 消耗和剩余余额)
// errCh 报告传输错误或服务端 failed 事件
func (c *Client) ChatStreamWithUsage(ctx context.Context, modelID string, req ChatRequest) (<-chan StreamEvent, <-chan SourcesEvent, <-chan StreamSettlement, <-chan error) {
	rawCh, rawErrCh := c.ChatStream(ctx, modelID, req)
	contentCh := make(chan StreamEvent, 32)
	sourcesCh := make(chan SourcesEvent, 8)
	settleCh := make(chan StreamSettlement, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(contentCh)
		defer close(sourcesCh)
		defer close(settleCh)
		defer close(errCh)

		for ev := range rawCh {
			// 结算事件 (settled / pending_settle)
			if s := ParseSettlement(ev); s != nil {
				select {
				case settleCh <- *s:
				case <-ctx.Done():
					return
				}
				continue
			}
			// 搜索来源事件
			if src := ParseSourcesEvent(ev); src != nil {
				select {
				case sourcesCh <- *src:
				case <-ctx.Done():
					return
				}
				continue
			}
			// 控制事件: 过滤
			if ev.Event == "started" {
				continue
			}
			// 失败事件: 解析错误信息发送到 errCh
			if ev.Event == "failed" {
				errMsg := parseStreamError(ev.Data)
				select {
				case errCh <- fmt.Errorf("stream failed: %s", errMsg):
				case <-ctx.Done():
				}
				return
			}
			// 内容事件
			select {
			case contentCh <- ev:
			case <-ctx.Done():
				return
			}
		}
		if err := <-rawErrCh; err != nil {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
		}
	}()

	return contentCh, sourcesCh, settleCh, errCh
}

// parseStreamError 从 failed 事件 JSON 中提取错误描述
func parseStreamError(data string) string {
	var payload struct {
		Error string `json:"error"`
		Stage string `json:"stage"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return data // fallback: 原始数据
	}
	if payload.Stage != "" {
		return payload.Stage + ": " + payload.Error
	}
	return payload.Error
}

// ============================================================================
// API: Entitlements (scope: entitlements)
// ============================================================================

// GetBalance 查询当前用户的权益余额 (聚合)
func (c *Client) GetBalance(ctx context.Context) (*EntitlementBalance, error) {
	var resp APIResponse[EntitlementBalance]
	if err := c.doJSON(ctx, http.MethodGet, "/entitlements/balance", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// GetBalanceDetail 查询详细余额 (含每条权益明细)
func (c *Client) GetBalanceDetail(ctx context.Context) (*BalanceDetail, error) {
	var resp APIResponse[BalanceDetail]
	if err := c.doJSON(ctx, http.MethodGet, "/entitlements/balance-detail", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ListEntitlements 查询当前用户权益列表
// status: "ACTIVE" / "EXPIRED" / "" (全部)
func (c *Client) ListEntitlements(ctx context.Context, status string) ([]EntitlementItem, error) {
	path := "/entitlements"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var resp APIResponse[[]EntitlementItem]
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp, false); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ListConsumeRecords 查询核销记录 (分页)
func (c *Client) ListConsumeRecords(ctx context.Context, page, pageSize int) (*ConsumeRecordPage, error) {
	path := fmt.Sprintf("/entitlements/consume-records?page=%d&pageSize=%d", page, pageSize)
	var resp APIResponse[ConsumeRecordPage]
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ClaimMonthlyFree 领取当月免费额度
// 幂等: 已领取时返回已有权益, 不重复发放
func (c *Client) ClaimMonthlyFree(ctx context.Context) (*EntitlementItem, error) {
	var resp APIResponse[EntitlementItem]
	if err := c.doJSON(ctx, http.MethodPost, "/entitlements/claim-monthly", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ============================================================================
// API: Token Packages / 商城 (scope: token-packages)
// ============================================================================

// ListTokenPackages 获取商城流量包列表
// 兼容 yudao 分页格式和直接数组格式 (tk-dist 代理透传)
func (c *Client) ListTokenPackages(ctx context.Context) ([]TokenPackage, error) {
	var raw APIResponse[json.RawMessage]
	if err := c.doJSON(ctx, http.MethodGet, "/token-packages", nil, &raw, false); err != nil {
		return nil, err
	}
	var page YudaoPageResult[TokenPackage]
	if json.Unmarshal(raw.Data, &page) == nil && page.List != nil {
		return page.List, nil
	}
	var packages []TokenPackage
	if err := json.Unmarshal(raw.Data, &packages); err != nil {
		return nil, fmt.Errorf("decode token packages: %w", err)
	}
	return packages, nil
}

// GetTokenPackageDetail 获取流量包详情
func (c *Client) GetTokenPackageDetail(ctx context.Context, packageID string) (*TokenPackage, error) {
	var resp APIResponse[TokenPackage]
	if err := c.doJSON(ctx, http.MethodGet, "/token-packages/"+url.PathEscape(packageID), nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// BuyTokenPackage 购买流量包 (创建订单)
func (c *Client) BuyTokenPackage(ctx context.Context, packageID string, payload *PayPayload) (*Order, error) {
	var body interface{}
	if payload != nil {
		body = payload
	}
	var resp APIResponse[Order]
	if err := c.doJSON(ctx, http.MethodPost, "/token-packages/"+url.PathEscape(packageID)+"/buy", body, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// GetOrderStatus 查询订单支付状态
func (c *Client) GetOrderStatus(ctx context.Context, orderID string) (*OrderStatus, error) {
	var resp APIResponse[OrderStatus]
	if err := c.doJSON(ctx, http.MethodGet, "/token-packages/orders/"+url.PathEscape(orderID)+"/status", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ListMyOrders 查询我的订单列表
// 兼容 yudao 分页格式 {"data":{"list":[...],"total":N}} 和直接数组 {"data":[...]}
func (c *Client) ListMyOrders(ctx context.Context) ([]Order, error) {
	var raw APIResponse[json.RawMessage]
	if err := c.doJSON(ctx, http.MethodGet, "/token-packages/my", nil, &raw, false); err != nil {
		return nil, err
	}
	// 尝试 yudao 分页格式
	var page YudaoPageResult[Order]
	if json.Unmarshal(raw.Data, &page) == nil && page.List != nil {
		return page.List, nil
	}
	// 降级: 直接数组
	var orders []Order
	if err := json.Unmarshal(raw.Data, &orders); err != nil {
		return nil, fmt.Errorf("decode orders: %w", err)
	}
	return orders, nil
}

// WaitForPayment 轮询订单支付状态直到终态
// 成功支付返回 (status, nil); 终态失败返回 (status, *OrderTerminalError)
// context 超时/取消返回 (nil, ctx.Err())
// pollInterval <= 0 时默认 2 秒
//
// 购买链路典型用法:
//
//	order, _ := client.BuyTokenPackage(ctx, pkgID, nil)
//	// 用户在 order.PayURL 完成支付 ...
//	status, err := client.WaitForPayment(ctx, order.ID, 3*time.Second)
func (c *Client) WaitForPayment(ctx context.Context, orderID string, pollInterval time.Duration) (*OrderStatus, error) {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		status, err := c.GetOrderStatus(ctx, orderID)
		if err != nil {
			return nil, err
		}

		if isOrderTerminal(status.Status) {
			if isOrderSuccess(status.Status) {
				return status, nil
			}
			return status, &OrderTerminalError{OrderID: orderID, Status: status.Status}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func isOrderSuccess(status string) bool {
	switch status {
	case "PAID", "SUCCESS", "COMPLETED":
		return true
	}
	return false
}

func isOrderTerminal(status string) bool {
	switch status {
	case "PAID", "SUCCESS", "COMPLETED",
		"FAILED", "CANCELLED", "CLOSED", "EXPIRED", "REFUNDED":
		return true
	}
	return false
}

// ============================================================================
// API: Wallet (scope: wallet:readonly)
// ============================================================================

// GetWalletStats 获取钱包统计 (余额/月消费/月充值)
func (c *Client) GetWalletStats(ctx context.Context) (*WalletStats, error) {
	var resp APIResponse[WalletStats]
	if err := c.doJSON(ctx, http.MethodGet, "/wallet/stats", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// GetWalletTransactions 获取最近交易记录
func (c *Client) GetWalletTransactions(ctx context.Context) ([]Transaction, error) {
	var resp APIResponse[[]Transaction]
	if err := c.doJSON(ctx, http.MethodGet, "/wallet/transactions", nil, &resp, false); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ============================================================================
// API: Skill Store (scope: skill_store / 公共端点)
// ============================================================================

// BrowseSkillStore 浏览技能商店 (公共端点, 无需认证)
// 便捷方法: 等价于 BrowseSkills(ctx, 1, 50, query.Category, query.Keyword, query.Tag, "")
func (c *Client) BrowseSkillStore(ctx context.Context, query SkillStoreQuery) ([]SkillStoreItem, error) {
	resp, err := c.BrowseSkills(ctx, 1, 50, query.Category, query.Keyword, query.Tag, "")
	if err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// BrowseSkills 浏览公共技能商店 (V3 分页接口, 公共端点, 无需认证)
func (c *Client) BrowseSkills(ctx context.Context, page, pageSize int, category, keyword, tag, source string) (*SkillBrowseResponse, error) {
	qv := url.Values{}
	qv.Set("page", fmt.Sprintf("%d", page))
	qv.Set("pageSize", fmt.Sprintf("%d", pageSize))
	if category != "" {
		qv.Set("category", category)
	}
	if keyword != "" {
		qv.Set("keyword", keyword)
	}
	if tag != "" {
		qv.Set("tag", tag)
	}
	if source != "" {
		qv.Set("source", source)
	}

	var resp APIResponse[SkillBrowseResponse]
	if err := c.doPublicJSON(ctx, http.MethodGet, "/skill-store?"+qv.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// BrowseSkillsList 轻量浏览公共技能商店（仅返回标题、简介等展示字段）
// 等价于 BrowseSkills + fields=minimal，响应体积缩减 90%+
func (c *Client) BrowseSkillsList(ctx context.Context, page, pageSize int,
	category, keyword, tag, source string) (*SkillBrowseListResponse, error) {
	qv := url.Values{}
	qv.Set("page", fmt.Sprintf("%d", page))
	qv.Set("pageSize", fmt.Sprintf("%d", pageSize))
	qv.Set("fields", "minimal")
	if category != "" {
		qv.Set("category", category)
	}
	if keyword != "" {
		qv.Set("keyword", keyword)
	}
	if tag != "" {
		qv.Set("tag", tag)
	}
	if source != "" {
		qv.Set("source", source)
	}

	var resp APIResponse[SkillBrowseListResponse]
	if err := c.doPublicJSON(ctx, http.MethodGet, "/skill-store?"+qv.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// GetSkillDetail 获取技能商店中某个技能的详情 (公共端点)
func (c *Client) GetSkillDetail(ctx context.Context, skillID string) (*SkillStoreItem, error) {
	var resp APIResponse[SkillStoreItem]
	if err := c.doPublicJSON(ctx, http.MethodGet, "/skill-store/"+url.PathEscape(skillID), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ResolveSkill 按 key 精确查找公共技能 (公共端点)
func (c *Client) ResolveSkill(ctx context.Context, key string) (*SkillStoreItem, error) {
	var resp APIResponse[SkillStoreItem]
	if err := c.doPublicJSON(ctx, http.MethodGet, "/skill-store/resolve/"+url.PathEscape(key), nil, &resp); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// InstallSkill 安装技能到当前用户的租户空间 (需 OAuth scope: skill_store)
func (c *Client) InstallSkill(ctx context.Context, skillID string) (*SkillStoreItem, error) {
	var resp APIResponse[SkillStoreItem]
	if err := c.doJSON(ctx, http.MethodPost, "/skill-store/"+url.PathEscape(skillID)+"/install", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// DownloadSkill 下载技能 ZIP 包 (公共端点, 双模式)
// 有 token 时自动附带 (享受无限流), 无 token 时匿名 (受限流)
// 返回 *RateLimitError 表示 429 限流
// 根因修复 #5: 使用 LimitReader 限制下载体积为 50MB
// [RC-3] 5 分钟超时 (大文件下载)
func (c *Client) DownloadSkill(ctx context.Context, skillID string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	token, _ := c.ensureToken(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.apiURL("/skill-store/"+url.PathEscape(skillID)+"/download"), nil)
	if err != nil {
		return nil, "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download skill: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, "", &RateLimitError{
			Message:    "匿名下载已达限制",
			RetryAfter: resp.Header.Get("Retry-After"),
			Raw:        string(bodyBytes),
		}
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, "", fmt.Errorf("download skill: %w", parseHTTPError(resp.StatusCode, bodyBytes))
	}

	// 根因修复 #5: 限制最大下载体积
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("read download body: %w", err)
	}
	if int64(len(data)) > maxDownloadSize {
		return nil, "", fmt.Errorf("download skill: response exceeds %dMB limit", maxDownloadSize>>20)
	}

	filename := "skill.zip"
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if idx := strings.Index(cd, "filename"); idx != -1 {
			parts := strings.SplitN(cd[idx:], "=", 2)
			if len(parts) == 2 {
				filename = strings.Trim(parts[1], "\"' ")
			}
		}
	}

	return data, filename, nil
}

// UploadSkill 上传技能 ZIP 包
// scope: "TENANT"
// intent: "PERSONAL" (仅自己用) 或 "PUBLIC_INTENT" (走认证→公开)
// 根因修复 #10: retried 参数防止无限递归
func (c *Client) UploadSkill(ctx context.Context, zipData []byte, scope, intent string) (*SkillStoreItem, error) {
	return c.uploadSkillInternal(ctx, zipData, scope, intent, false)
}

// [RC-6] 使用 mime/multipart.Writer 生成随机 boundary, 防止 ZIP 内容碰撞
// [RC-3] 5 分钟超时 (大文件上传)
func (c *Client) uploadSkillInternal(ctx context.Context, zipData []byte, scope, intent string, retried bool) (*SkillStoreItem, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("scope", scope)
	_ = writer.WriteField("intent", intent)
	part, err := writer.CreateFormFile("file", "skill.zip")
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(zipData); err != nil {
		return nil, fmt.Errorf("write zip data: %w", err)
	}
	writer.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiURL("/skill-store/upload"), &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 根因修复 #10: 401 时刷新 token 并重试一次, retried 防无限递归
	if resp.StatusCode == http.StatusUnauthorized && !retried {
		resp.Body.Close()
		if refreshErr := c.forceRefresh(ctx); refreshErr != nil {
			return nil, fmt.Errorf("upload: unauthorized and refresh failed: %w", refreshErr)
		}
		return c.uploadSkillInternal(ctx, zipData, scope, intent, true)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, fmt.Errorf("upload: %w", parseHTTPError(resp.StatusCode, bodyBytes))
	}

	var result struct {
		Data struct {
			Skill SkillStoreItem `json:"skill"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result.Data.Skill, nil
}

// GetSkillSummary 获取技能统计概览 (installed/created/total/storeAvailable)
func (c *Client) GetSkillSummary(ctx context.Context) (*SkillSummary, error) {
	var resp APIResponse[SkillSummary]
	if err := c.doJSON(ctx, http.MethodGet, "/skills/summary", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// CertifySkill 触发技能认证管线 (异步)
func (c *Client) CertifySkill(ctx context.Context, skillID string) error {
	return c.doJSON(ctx, http.MethodPost, "/skill-store/"+url.PathEscape(skillID)+"/certify", nil, nil, false)
}

// GetCertificationStatus 查询技能认证状态
func (c *Client) GetCertificationStatus(ctx context.Context, skillID string) (*CertificationStatus, error) {
	var resp APIResponse[CertificationStatus]
	if err := c.doJSON(ctx, http.MethodGet, "/skill-store/"+url.PathEscape(skillID)+"/certification", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ============================================================================
// API: Skill Generator (scope: skill_store)
// ============================================================================

// GenerateSkill 根据自然语言描述生成技能定义 (基于独立 LLM)
func (c *Client) GenerateSkill(ctx context.Context, req GenerateSkillRequest) (*GenerateSkillResult, error) {
	var resp APIResponse[GenerateSkillResult]
	if err := c.doJSON(ctx, http.MethodPost, "/skill-generator/generate", req, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// OptimizeSkill 优化已有技能定义
func (c *Client) OptimizeSkill(ctx context.Context, req OptimizeSkillRequest) (*OptimizeSkillResult, error) {
	var resp APIResponse[OptimizeSkillResult]
	if err := c.doJSON(ctx, http.MethodPost, "/skill-generator/optimize", req, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ValidateSkill 校验技能定义正确性
func (c *Client) ValidateSkill(ctx context.Context, skillName string) error {
	body := map[string]string{"skillName": skillName}
	return c.doJSON(ctx, http.MethodPost, "/skill-generator/validate", body, nil, false)
}

// ============================================================================
// API: Unified Tools (scope: tools)
// ============================================================================

// ListTools 获取当前用户租户下的所有工具 (Skill 优先 + Plugin 兜底)
func (c *Client) ListTools(ctx context.Context) ([]ToolView, error) {
	var resp APIResponse[ToolListResponse]
	if err := c.doJSON(ctx, http.MethodGet, "/tools", nil, &resp, false); err != nil {
		return nil, err
	}
	return resp.Data.Skills, nil
}

// GetTool 获取单个工具详情
func (c *Client) GetTool(ctx context.Context, toolID string) (*ToolView, error) {
	var resp APIResponse[ToolView]
	if err := c.doJSON(ctx, http.MethodGet, "/tools/"+url.PathEscape(toolID), nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ============================================================================
// API: Notifications
// ============================================================================

// ListNotifications 分页查询通知列表
func (c *Client) ListNotifications(ctx context.Context, page, pageSize int, typeFilter string) (*NotificationList, error) {
	path := fmt.Sprintf("/notifications?page=%d&pageSize=%d", page, pageSize)
	if typeFilter != "" {
		path += "&type=" + url.QueryEscape(typeFilter)
	}
	var resp APIResponse[NotificationList]
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// GetUnreadCount 获取未读通知数量
func (c *Client) GetUnreadCount(ctx context.Context) (int64, error) {
	var resp APIResponse[NotificationUnreadCount]
	if err := c.doJSON(ctx, http.MethodGet, "/notifications/unread-count", nil, &resp, false); err != nil {
		return 0, err
	}
	return resp.Data.UnreadCount, nil
}

// MarkNotificationRead 标记单条通知已读
func (c *Client) MarkNotificationRead(ctx context.Context, id string) error {
	var resp APIResponse[any]
	return c.doJSON(ctx, http.MethodPut, "/notifications/"+url.PathEscape(id)+"/read", nil, &resp, false)
}

// MarkAllNotificationsRead 标记全部通知已读
func (c *Client) MarkAllNotificationsRead(ctx context.Context) error {
	var resp APIResponse[any]
	return c.doJSON(ctx, http.MethodPut, "/notifications/read-all", nil, &resp, false)
}

// DeleteNotification 删除通知
func (c *Client) DeleteNotification(ctx context.Context, id string) error {
	var resp APIResponse[any]
	return c.doJSON(ctx, http.MethodDelete, "/notifications/"+url.PathEscape(id), nil, &resp, false)
}

// RegisterDevice 注册推送设备 token
func (c *Client) RegisterDevice(ctx context.Context, reg DeviceRegistration) error {
	var resp APIResponse[any]
	return c.doJSON(ctx, http.MethodPost, "/devices/register", reg, &resp, false)
}

// UnregisterDevice 注销推送设备 token
func (c *Client) UnregisterDevice(ctx context.Context, token string) error {
	var resp APIResponse[any]
	return c.doJSON(ctx, http.MethodDelete, "/devices/"+url.PathEscape(token), nil, &resp, false)
}

// ListNotificationPreferences 获取通知偏好设置
func (c *Client) ListNotificationPreferences(ctx context.Context) ([]NotificationPreference, error) {
	var resp APIResponse[[]NotificationPreference]
	if err := c.doJSON(ctx, http.MethodGet, "/notification-preferences", nil, &resp, false); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// UpdateNotificationPreference 更新通知偏好
func (c *Client) UpdateNotificationPreference(ctx context.Context, typeCode string, pref NotificationPreference) error {
	var resp APIResponse[any]
	return c.doJSON(ctx, http.MethodPut, "/notification-preferences/"+url.PathEscape(typeCode), pref, &resp, false)
}

// ============================================================================
// Internal HTTP
// ============================================================================

// 根因修复 #11: 使用 strings.HasSuffix 精确匹配, 防止域名含 /api/v4 子串时误判
func (c *Client) apiURL(path string) string {
	base := c.serverURL
	if !strings.HasSuffix(base, "/api/v4") {
		base += "/api/v4"
	}
	return base + path
}

// doJSONFull 与 doJSON 相同，但额外返回响应 Header (用于提取 X-Token-Remaining 等)
func (c *Client) doJSONFull(ctx context.Context, method, path string, body interface{}, result interface{}) (http.Header, error) {
	header, err := c.doJSONFullInternal(ctx, method, path, body, result, false)
	return header, err
}

func (c *Client) doJSONFullInternal(ctx context.Context, method, path string, body interface{}, result interface{}, retried bool) (http.Header, error) {
	// 如果调用方已设置 deadline（如 Chat 的 5min），不覆盖；否则默认 30s
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	token, err := c.ensureToken(ctx)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path), bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && !retried {
		resp.Body.Close()
		if refreshErr := c.forceRefresh(ctx); refreshErr != nil {
			return nil, fmt.Errorf("unauthorized and refresh failed: %w", refreshErr)
		}
		return c.doJSONFullInternal(ctx, method, path, body, result, true)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, parseHTTPError(resp.StatusCode, bodyBytes)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if checker, ok := result.(businessCodeChecker); ok {
			if err := checker.businessError(); err != nil {
				return nil, err
			}
		}
	}
	return resp.Header, nil
}

// parseHTTPError 解析 HTTP 错误响应体，兼容 Anthropic 和 OpenAI 错误格式
// Anthropic: {"type":"error","error":{"type":"...","message":"..."}}
// OpenAI:    {"error":{"message":"...","type":"...","code":"..."}}
// 通用回退: HTTP {status}: {body}
func parseHTTPError(statusCode int, body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("HTTP %d", statusCode)
	}
	var obj map[string]interface{}
	if json.Unmarshal(body, &obj) == nil {
		// Anthropic 格式: {"type":"error","error":{"type":"...","message":"..."}}
		if errObj, ok := obj["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["message"].(string); ok {
				errType, _ := errObj["type"].(string)
				if errType != "" {
					return fmt.Errorf("HTTP %d: [%s] %s", statusCode, errType, msg)
				}
				return fmt.Errorf("HTTP %d: %s", statusCode, msg)
			}
		}
	}
	return fmt.Errorf("HTTP %d: %s", statusCode, string(body))
}

// 根因修复 #3: doJSON 增加 retried 参数, 401 重试只允许一次, 防无限递归栈溢出
// [RC-3] per-request 30s 超时 (替代全局 http.Client.Timeout)
// 委托到 doJSONFullInternal 消除代码重复
func (c *Client) doJSON(ctx context.Context, method, path string, body interface{}, result interface{}, retried bool) error {
	_, err := c.doJSONFullInternal(ctx, method, path, body, result, retried)
	return err
}

// doPublicJSON 公共端点请求
// 有 token 时自动附带 (享受认证用户待遇), 无 token 时匿名请求
// 不做 401 重试 (公共端点不应要求认证)
// [RC-3] per-request 30s 超时
func (c *Client) doPublicJSON(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	token, _ := c.ensureToken(ctx)

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path), bodyReader)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return parseHTTPError(resp.StatusCode, bodyBytes)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		// 根因修复 #16: 同 doJSON, 公共端点也需检查业务错误码
		if checker, ok := result.(businessCodeChecker); ok {
			if err := checker.businessError(); err != nil {
				return err
			}
		}
	}
	return nil
}

// 根因修复 #6: forceRefresh 不再静默吞掉 store.Save 错误, 打印警告
func (c *Client) forceRefresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tokens == nil {
		return fmt.Errorf("no tokens to refresh")
	}
	if c.meta == nil {
		meta, err := Discover(ctx, c.serverURL)
		if err != nil {
			return err
		}
		c.meta = meta
	}

	tokenResp, err := RefreshToken(ctx, c.meta, c.tokens.ClientID, c.tokens.RefreshToken)
	if err != nil {
		return err
	}

	c.tokens = NewTokenSet(tokenResp, c.tokens.ClientID, c.serverURL)
	if saveErr := c.store.Save(c.tokens); saveErr != nil {
		fmt.Printf("[acosmi-sdk] warning: save refreshed token failed: %v\n", saveErr)
	}
	return nil
}
