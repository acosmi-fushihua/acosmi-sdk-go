package acosmi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	// v0.15.1: token 就绪等待机制
	// tokenReady: NewClient(已有 token)/loginInternal 成功后 close, 等待方解除阻塞
	// tokenOnce:  保证 close 幂等; Logout 时与 tokenReady 一同重置
	// loginInFlight: true=Login 进行中, 等待方需等; false=未调 Login, 等待方 fail-fast
	tokenReady    chan struct{}
	tokenOnce     sync.Once
	loginInFlight bool

	// 模型能力缓存 (CrabCode 扩展)
	modelCache     []ManagedModel // ListModels 缓存
	modelCacheTime time.Time      // 缓存写入时间

	// v0.11.0: 请求前防御钩子。nil = 未启用, 零开销。
	defensiveCfg       *sanitize.MinimalSanitizeConfig
	autoStripEphemeral bool

	// L6 (v0.15): 重试策略. nil = 禁用 (v0.14.1 行为).
	retryPolicy *RetryPolicy

	// V29 系数缓存 (TTL 8s, ListCoefficients 内部用)
	coefCacheMu   sync.Mutex
	coefCacheData []ModelCoefficient
	coefCacheAt   time.Time
}

const coefCacheTTL = 8 * time.Second

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

	// RetryPolicy 重试策略 (L6, v0.15).
	//
	// nil = 禁用重试 (v0.14.1 行为, 老调用方 0 影响).
	// 非 nil = 启用 — 默认 SafeToRetry POST=false 兜底, chat/messages POST 仍 0 retry.
	// GET 类查询 (skill-store/models/balance) 自动得 2x 稳定性.
	//
	// 计费安全: 自定义 SafeToRetry 时, 严禁让 POST chat/messages 通过, 否则双扣.
	RetryPolicy *RetryPolicy
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
		serverURL:   strings.TrimRight(cfg.ServerURL, "/"),
		store:       store,
		http:        httpClient,
		retryPolicy: effectivePolicy(cfg.RetryPolicy),
		tokenReady:  make(chan struct{}),
	}

	// 尝试加载已保存的 token
	if tokens, err := store.Load(); err == nil && tokens != nil {
		c.tokens = tokens
		c.tokenOnce.Do(func() { close(c.tokenReady) })
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
	// v0.15.1: 标记 Login 进行中, 让并发的 ensureToken 等待方知道"应等"而非"立即报错"
	c.mu.Lock()
	c.loginInFlight = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.loginInFlight = false
		c.mu.Unlock()
	}()
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

	// 5. 持久化 + 通知等待方 (单锁内完成, 防与 Logout 重置 tokenOnce/tokenReady 竞争)
	tokens := NewTokenSet(tokenResp, clientID, c.serverURL)
	c.mu.Lock()
	c.tokens = tokens
	c.tokenOnce.Do(func() { close(c.tokenReady) })
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
	// v0.15.1: 重置等待信号 — 下次 Login 重新触发等待→唤醒流程
	c.tokenReady = make(chan struct{})
	c.tokenOnce = sync.Once{}
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
//
// v0.15.1: 当 tokens==nil 且 Login 正在并发进行中时, 阻塞等待 token 就绪,
// 避免应用启动期 "Login + 多个 API 调用" 并发场景下 4+ 条 "not authorized" 误报.
// Login 未启动时仍 fail-fast (保留原错误信息).
func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	tokens := c.tokens
	ready := c.tokenReady
	inFlight := c.loginInFlight
	c.mu.RUnlock()

	if tokens == nil {
		if !inFlight {
			return "", fmt.Errorf("not authorized, call Login() first")
		}
		// Login 进行中, 等待就绪或 ctx 超时
		select {
		case <-ready:
			c.mu.RLock()
			tokens = c.tokens
			c.mu.RUnlock()
			if tokens == nil {
				// 边界: 等待期间被 Logout 重置, 当作未授权
				return "", fmt.Errorf("not authorized, call Login() first")
			}
		case <-ctx.Done():
			return "", fmt.Errorf("waiting for token: %w", ctx.Err())
		}
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

// ListModels 获取可用的托管模型列表.
//
// V30 二轮审计 D-P1-2: 此方法不返回 entitlement-filter-status header. UI 想根据 fallback
// 状态显示降级提示 (e.g. "tk-dist 离线, 临时显示全部模型"), 请改调 ListModelsWithStatus.
func (c *Client) ListModels(ctx context.Context) ([]ManagedModel, error) {
	models, _, err := c.ListModelsWithStatus(ctx)
	return models, err
}

// FilterStatus 是 X-Entitlement-Filter-Status 响应头的取值集 (V30 二轮审计 D-P1-2 引入).
//
// 客户端用此区分: 正常过滤路径 (Ok) / 全量返回的多种降级原因 / 未知值 (上游版本更新引入新值时
// fall through 到 FilterStatusUnknown 而非崩溃).
//
// 与 nexus-v4 backend setEntitlementFilterStatusHeader 字面量必须保持一致, 任一端漂移都会
// 让 SDK 用户拿到 FilterStatusUnknown — UI 应该 graceful 处理而非硬编码完整集.
type FilterStatus string

const (
	FilterStatusOK                   FilterStatus = "ok"                                  // 正常按用户 entitlement 过滤
	FilterStatusAdminBypass          FilterStatus = "admin-bypass"                        // admin 路径 (V30 二轮后仅 ListAdmin 端点能命中)
	FilterStatusInternalBypass       FilterStatus = "internal-bypass"                     // X-Internal-Bypass header 命中 (CI/bot)
	FilterStatusDisabledByFlag       FilterStatus = "disabled-by-flag"                    // ENTITLEMENT_LIST_FILTER_ENABLED=false 灰度回滚
	FilterStatusFallbackTkdistError  FilterStatus = "fallback-tkdist-error"               // tk-dist RPC 失败 fail-OPEN, UI 应 toast 提示
	FilterStatusFallbackTkdistSkew   FilterStatus = "fallback-tkdist-deployment-skew"     // tk-dist 返 404 (V30 二轮 B-P1-D), 部署版本不一致, SRE 需查 tk-dist
	FilterStatusFallbackNoBuckets    FilterStatus = "fallback-no-buckets"                 // V9 老用户无桶 fallback
	FilterStatusFallbackMissingUser  FilterStatus = "fallback-missing-userid"             // 防御性, 应永远不出现
	FilterStatusUnknown              FilterStatus = ""                                    // 未知/缺失 — 老 nexus / 非 V30 端点
)

// ListModelsWithStatus 获取可用模型列表, 同时返回 X-Entitlement-Filter-Status header.
//
// V30 二轮审计 D-P1-2: 老 ListModels 丢弃 header 让 SDK 用户无法识别 fail-OPEN 降级状态,
// 此方法暴露 status 让 UI 可:
//   - status == FilterStatusOK → 正常显示 BucketInfo 余量
//   - status == FilterStatusFallbackTkdistError/Skew → 灰显余量 + toast "tk-dist 离线, 模型列表降级"
//   - status == FilterStatusDisabledByFlag → 不显示余量 (运维灰度中)
//   - status == FilterStatusUnknown → 老 nexus, 按老 v0.17 行为 (不显示 BucketInfo)
//
// 底层 BucketInfo 字段仅 status==Ok 时由上游填充, 其他状态下为 nil.
func (c *Client) ListModelsWithStatus(ctx context.Context) ([]ManagedModel, FilterStatus, error) {
	var resp APIResponse[[]ManagedModel]
	header, err := c.doJSONFull(ctx, http.MethodGet, "/managed-models", nil, &resp)
	if err != nil {
		return nil, FilterStatusUnknown, err
	}
	// 写入模型缓存 (供 getCachedCapabilities / GetModelCapabilities 使用)
	c.mu.Lock()
	c.modelCache = resp.Data
	c.modelCacheTime = time.Now()
	c.mu.Unlock()

	status := FilterStatusUnknown
	if header != nil {
		if h := header.Get("X-Entitlement-Filter-Status"); h != "" {
			status = FilterStatus(h)
		}
	}
	return resp.Data, status, nil
}

// GetQuotaSummary 查询当前用户账户级权益总览 (v0.19+).
//
// 返回 *QuotaSummary 含免费/付费各自总额 + 桶详情 + 各自最近到期时间.
// 用于个人中心钱包栏: UI 一次拿完整钱包视图, 不需要遍历 ListModels 客户端聚合.
//
// 与 ListModels 的区别:
//   - ListModels: 按 modelId 聚合 BucketInfo, 答"a 模型还能用多少", 走 entitlement 过滤
//   - GetQuotaSummary: 账户级钱包概览, 答"我整体还有多少免费 + 付费", 不过滤 (用户必须看自己钱包)
//
// 鉴权: JWT 或 Desktop OAuth (entitlements scope).
// 失败语义: tk-dist 不可达 → 500 + non-nil err; 空桶用户 → 200 + 全 0 + 空切片.
func (c *Client) GetQuotaSummary(ctx context.Context) (*QuotaSummary, error) {
	var resp APIResponse[QuotaSummary]
	if err := c.doJSON(ctx, http.MethodGet, "/entitlements/quota-summary", nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
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
	// V29: 当前模型剩余 token (raw + ETU)
	if v := headers.Get("X-Token-Remaining-Model"); v != "" {
		if n, parseErr := strconv.ParseInt(v, 10, 64); parseErr == nil {
			resp.ModelTokenRemaining = n
		}
	}
	if v := headers.Get("X-Token-Remaining-Model-ETU"); v != "" {
		if n, parseErr := strconv.ParseInt(v, 10, 64); parseErr == nil {
			resp.ModelTokenRemainingETU = n
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

	resp, err := c.doRequest(httpReq)
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
		errCh <- parseHTTPErrorWithHeader(resp.StatusCode, bodyBytes, resp.Header)
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

	resp, err := c.doRequest(httpReq)
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
		errCh <- parseHTTPErrorWithHeader(resp.StatusCode, bodyBytes, resp.Header)
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
			// 失败/错误事件: 解析错误信息发送到 errCh
			//
			// v0.14.1 (V2 P0.7): "event: error" 是 Anthropic 协议 (/anthropic 端点) 流式失败语义,
			// "event: failed" 是 acosmi managed-model 协议 (OpenAI wrapper) 失败语义。两个 event
			// 名都路由到 errCh, parseStreamError 同时兼容 Anthropic 标准 {error.type, error.message}
			// 与 acosmi 私有扩展 {errorCode, retryable, message, stage}, 缺字段自动零值。
			//
			// 历史版本 (≤v0.14.0) 仅识别 failed, 拿不到 Anthropic 协议结构化错误 → /managed-models/<id>/anthropic
			// 路径上的 transport 错误 (EOF/超时/disconnect) 经网关转化后仍当成 content 透传, 客户端
			// 无法 errors.As(*StreamError) 决策重试。本分支补齐这个口子, 无破坏性 (老网关响应仅出现
			// "failed" event, 此分支不影响; 新网关同时下发 "error" + 私有扩展, 此分支正确路由)。
			if ev.Event == "failed" || ev.Event == "error" {
				se := parseStreamError(ev.Data)
				select {
				case errCh <- se:
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

// StreamError 流式失败事件的结构化表示。
//
// 由 gateway 的 `managed_model_stream_failed` 事件解析得到。客户端可通过
// errors.As(err, &se) 提取并:
//   - 按 Code 做 i18n / 重试决策 (例: Code == "empty_response" → 自动重试)
//   - 按 Retryable 做退避决策
//   - 按 Message 做用户可见提示 (gateway 已下发中文文案; 为空时由调用方兜底)
//
// 实现 error 接口, 与历史 Go error 兼容, 旧调用方 `err.Error()` 文案保持稳定。
type StreamError struct {
	Code      string // 例: "empty_response" / "rate_limit" / "overloaded" / ""
	Stage     string // 例: "provider" / "settlement"
	Message   string // 用户友好提示 (中文); 历史字段, 与 RawError 区分
	RawError  string // gateway 原始 error 字符串 (含 provider/model/latency 等 debug 信息)
	Retryable bool   // 客户端是否值得重试
}

// Error 实现 error 接口, 文案保持向后兼容: "stream failed: <stage>: <raw>".
func (e *StreamError) Error() string {
	if e == nil {
		return ""
	}
	body := e.RawError
	if body == "" {
		body = e.Message
	}
	if e.Stage != "" {
		return "stream failed: " + e.Stage + ": " + body
	}
	return "stream failed: " + body
}

// parseStreamError 从 failed/error 事件 JSON 中提取结构化错误。
//
// 兼容三种 schema (按优先级):
//
//  1. acosmi managed-model 协议 ("event: failed"):
//     {errorCode, stage, error: <string>, message, retryable}
//     example: gateway 流式失败事件 (model_gateway.go 经 handler 写入)
//
//  2. Anthropic 协议扩展 ("event: error", v0.14.1 起):
//     {type:"error", error:{type, message}, errorCode, retryable, message, stage}
//     example: handler/managed_model.go P0.7 在 Anthropic 标准之上叠加 acosmi 私有字段
//
//  3. Anthropic 标准纯净格式 (老网关 / 官方上游直返):
//     {type:"error", error:{type, message}}
//
// 实现策略: 用 json.RawMessage 接 error 字段 — 既可能是 string 也可能是 object。
// 解析失败时退化为 RawError=原始数据 (向后兼容 v0.14.0 行为)。
func parseStreamError(data string) *StreamError {
	var payload struct {
		ErrorCode string          `json:"errorCode"`
		Stage     string          `json:"stage"`
		Error     json.RawMessage `json:"error"` // string OR {type, message}
		Message   string          `json:"message"`
		Retryable bool            `json:"retryable"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return &StreamError{RawError: data}
	}

	se := &StreamError{
		Code:      payload.ErrorCode,
		Stage:     payload.Stage,
		Message:   payload.Message,
		Retryable: payload.Retryable,
	}

	// error 字段三态: string / object / 缺失
	if len(payload.Error) > 0 {
		// 试 string (acosmi 老协议)
		var asString string
		if err := json.Unmarshal(payload.Error, &asString); err == nil {
			se.RawError = asString
		} else {
			// 试 object (Anthropic 标准: {type, message})
			var asObject struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(payload.Error, &asObject); err == nil {
				// Anthropic 协议: error.message 是用户面文案, error.type 是错误类别
				// 把 object 序列化回 string 存 RawError, 保留全部原始信息便于排查
				se.RawError = string(payload.Error)
				// 私有 message 字段空时, 用 Anthropic error.message 兜底
				if se.Message == "" && asObject.Message != "" {
					se.Message = asObject.Message
				}
				// errorCode 空 + Anthropic error.type 非空时, 兜底用 type 作 Code
				// (避免客户端 errors.As 拿到 Code="" 无法做决策)
				if se.Code == "" && asObject.Type != "" {
					se.Code = asObject.Type
				}
			} else {
				// 既不是 string 也不是已知 object — 整段塞 RawError 兜底
				se.RawError = string(payload.Error)
			}
		}
	}

	return se
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

// ----------------------------------------------------------------------------
// V29 Per-Model Bucket APIs
// ----------------------------------------------------------------------------

// GetByModel 查询当前用户在指定模型下的剩余 token (raw + ETU)。
// 应用层切换模型时调用; HasQuota=false 时建议给用户提示并阻止该模型请求。
func (c *Client) GetByModel(ctx context.Context, modelID string) (*ModelByQuotaResponse, error) {
	if modelID == "" {
		return nil, fmt.Errorf("modelID required")
	}
	path := "/entitlements/by-model?modelId=" + url.QueryEscape(modelID)
	var resp APIResponse[ModelByQuotaResponse]
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp, false); err != nil {
		return nil, err
	}
	return &resp.Data, nil
}

// ListBuckets 列出当前用户的全部桶 (个人中心多桶 hero 数据源)。
func (c *Client) ListBuckets(ctx context.Context) ([]ModelBucket, error) {
	var resp APIResponse[[]ModelBucket]
	if err := c.doJSON(ctx, http.MethodGet, "/entitlements/buckets", nil, &resp, false); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ListCoefficients 拉取模型系数表; 客户端可本地按 modelID 索引并按 OutputCoef 反向估算 raw token。
// SDK 自带 8s TTL 内存缓存以减小调用风暴。
func (c *Client) ListCoefficients(ctx context.Context) ([]ModelCoefficient, error) {
	c.coefCacheMu.Lock()
	if c.coefCacheData != nil && time.Since(c.coefCacheAt) < coefCacheTTL {
		out := append([]ModelCoefficient(nil), c.coefCacheData...)
		c.coefCacheMu.Unlock()
		return out, nil
	}
	c.coefCacheMu.Unlock()

	var resp APIResponse[[]ModelCoefficient]
	if err := c.doJSON(ctx, http.MethodGet, "/entitlements/coefficients", nil, &resp, false); err != nil {
		return nil, err
	}
	c.coefCacheMu.Lock()
	c.coefCacheData = append([]ModelCoefficient(nil), resp.Data...)
	c.coefCacheAt = time.Now()
	c.coefCacheMu.Unlock()
	return resp.Data, nil
}

// InvalidateCoefficientCache 手动失效系数缓存 (admin 调价后建议立即调一次)。
func (c *Client) InvalidateCoefficientCache() {
	c.coefCacheMu.Lock()
	c.coefCacheData = nil
	c.coefCacheAt = time.Time{}
	c.coefCacheMu.Unlock()
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

	resp, err := c.doRequest(req)
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
		return nil, "", fmt.Errorf("download skill: %w", parseHTTPErrorWithHeader(resp.StatusCode, bodyBytes, resp.Header))
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

	// L6 (v0.15) 注: bodyBytes 用 buf.Bytes() 快照, 但 SafeToRetry POST 默认 false →
	// 即使 RetryPolicy 启用也走单次 (与 doRequest 等价); 此处升级仅为统一调用模式 + 错误类型化.
	bodyBytes := buf.Bytes()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.apiURL("/skill-store/upload"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.doRequestWithRetry(req, bodyBytes)
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
		return nil, fmt.Errorf("upload: %w", parseHTTPErrorWithHeader(resp.StatusCode, bodyBytes, resp.Header))
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

	// L6 (v0.15): 序列化 body 为 []byte 以便重试时重 wrap reader.
	var bodyBytes []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyBytes = data
	}

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path), bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.doRequestWithRetry(req, bodyBytes)
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
		return nil, parseHTTPErrorWithHeader(resp.StatusCode, bodyBytes, resp.Header)
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

// doRequest 是 c.http.Do 的统一包装 — 错误经 classifyTransport 转 *NetworkError.
//
// L6 V2 P1 (2026-04-27, v0.15): 6 处原始 c.http.Do(req) 全部走此 helper, 给后续 L6 retry policy
// 提供统一可分类的错误源. SDK 内部统一调用点; 老调用方调 c.http.Do 直接也能编译, 但 err 不分类.
func (c *Client) doRequest(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, classifyTransport(req.Method+" "+req.URL.Path, req.URL.String(), err)
	}
	return resp, nil
}

// doRequestWithRetry 带 RetryPolicy 的 doRequest 包装 — 仅用于同步 (非流式) 路径.
//
// L6 (v0.15): 内部对 SafeToRetry+OnRetryable 闸门评估, 不通过则单次调 doRequest.
//
// 重试触发:
//   - transport 层 err (NetworkError Timeout/EOF) → 重试
//   - HTTP 5xx / 429 → 主动构造 *HTTPError 喂给 OnRetryable, 默认重试
//   - 其他 (HTTP 2xx/3xx/4xx 非 429 / DNS / *StreamError) → 不重试
//
// 参数 bodyBytes 必须传 (即使 nil) — 用于重试时重新构造 Body reader.
// req.Body 调用本函数前应当为 nil 或预读完毕; 函数内部会 reset.
//
// 流式路径 (chatMessagesStreamInternal / chatStreamInternal) **不得**调用此函数, 必须直接用 doRequest.
// 流式重试 = 双 token + 重复消息 (V2 P0 *StreamError 已通过 OnRetryable 显式排除, 但流路径还需路径层硬编码 bypass).
//
// 返回的 resp 可能 body 已被消费 (5xx 重试探测时已读); caller 必须处理 nil resp 时的错误.
func (c *Client) doRequestWithRetry(req *http.Request, bodyBytes []byte) (*http.Response, error) {
	policy := c.retryPolicy
	// nil policy 或 SafeToRetry 不通过 → 直接走单次 (老路径, 0 行为变化)
	if policy == nil || !policy.SafeToRetry(req) {
		return c.doRequest(req)
	}

	var lastErr error
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		// 重置 body (除首次外)
		if attempt > 0 && bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err := c.doRequest(req)

		// 探测 HTTP 状态: 仅 5xx/429 才进 retry 评估; 2xx/3xx/4xx 直接返回 caller
		// 注: 这里若 5xx 进入 retry, body 必须读完释放连接 (defer Close 也需要)
		if err == nil && resp != nil {
			if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return resp, nil // 成功 / 客户端业务错误 → caller 处理
			}
			// 5xx 或 429 → 进 retry 评估
			bodyPeek, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
			resp.Body.Close()
			err = parseHTTPErrorWithHeader(resp.StatusCode, bodyPeek, resp.Header)
		}

		lastErr = err

		// 最后一次不再重试
		if attempt+1 == policy.MaxAttempts {
			break
		}

		if !policy.OnRetryable(err) {
			break
		}

		// 退避 — Retry-After 优先 (HTTPError) > 指数退避
		backoff := computeBackoff(policy, attempt, err)
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(backoff):
		}
	}
	return nil, lastErr
}

// classifyTransport 包装 c.http.Do 返回的 err 为 *NetworkError, 便于 L6 retry policy 判定.
//
// 分类规则 (按优先级):
//   - ctx.DeadlineExceeded / err.Timeout() 为 true → Timeout=true
//   - errors.Is(io.EOF) / "unexpected EOF" / "connection reset" → EOF=true
//   - 其他: Timeout/EOF 都 false (不重试)
//
// op 描述用于 Error() 输出 (e.g. "POST /v1/messages"); url 用于错误定位 (脱敏由调用方负责).
// 文案兼容: NetworkError.Error() 包含 cause.Error() 原文, 老 fmt.Errorf 风格调用方字符串匹配仍工作.
func classifyTransport(op, urlStr string, err error) *NetworkError {
	if err == nil {
		return nil
	}
	ne := &NetworkError{
		Op:    op,
		URL:   urlStr,
		Cause: err,
	}
	// Timeout 检测: ctx.DeadlineExceeded + net.Error.Timeout()
	if errors.Is(err, context.DeadlineExceeded) {
		ne.Timeout = true
		return ne
	}
	type timeoutErr interface{ Timeout() bool }
	var te timeoutErr
	if errors.As(err, &te) && te.Timeout() {
		ne.Timeout = true
		return ne
	}
	// EOF 检测: io.EOF + 字符串匹配 (跨平台 darwin/linux 兼容, 不用 syscall.ECONNRESET)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		ne.EOF = true
		return ne
	}
	msg := err.Error()
	if strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") {
		ne.EOF = true
	}
	return ne
}

// parseHTTPError 解析 HTTP 错误响应体，兼容 Anthropic 和 OpenAI 错误格式
// Anthropic: {"type":"error","error":{"type":"...","message":"..."}}
// OpenAI:    {"error":{"message":"...","type":"...","code":"..."}}
// 通用回退: HTTP {status}: {body}
//
// L6 V2 P1 (2026-04-27, v0.15): 改返回 *HTTPError 结构化错误, 调用方可 errors.As 提取.
// 文案兼容承诺: Error() 字符串与老 fmt.Errorf 输出一致, 老调用方字符串匹配 0 破坏.
func parseHTTPError(statusCode int, body []byte) error {
	return parseHTTPErrorWithHeader(statusCode, body, nil)
}

// parseHTTPErrorWithHeader 同 parseHTTPError 但额外解析 Retry-After 头到 HTTPError.RetryAfter.
// L6 retry policy 用此字段做指数退避降级.
func parseHTTPErrorWithHeader(statusCode int, body []byte, header http.Header) error {
	he := &HTTPError{
		StatusCode: statusCode,
		Body:       string(body),
	}
	// Retry-After 头: 仅支持 "120" 秒形式 (HTTP 日期形式罕见, 暂不支持)
	if header != nil {
		if ra := header.Get("Retry-After"); ra != "" {
			if sec, err := strconv.Atoi(ra); err == nil && sec > 0 {
				he.RetryAfter = sec
			}
		}
	}
	if len(body) == 0 {
		return he
	}
	var obj map[string]interface{}
	if json.Unmarshal(body, &obj) == nil {
		// Anthropic 格式: {"type":"error","error":{"type":"...","message":"..."}}
		// OpenAI 格式:    {"error":{"message":"...","type":"...","code":"..."}}
		if errObj, ok := obj["error"].(map[string]interface{}); ok {
			if msg, ok := errObj["message"].(string); ok {
				he.Message = msg
			}
			if errType, ok := errObj["type"].(string); ok {
				he.Type = errType
			}
		}
	}
	return he
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

	// L6 (v0.15): 序列化 body 为 []byte 以便重试时重 wrap reader.
	var bodyBytes []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyBytes = data
	}

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
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

	resp, err := c.doRequestWithRetry(req, bodyBytes)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return parseHTTPErrorWithHeader(resp.StatusCode, bodyBytes, resp.Header)
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
