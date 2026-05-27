package acosmi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// authHTTPClient auth 专用 HTTP 客户端 (带 30s 超时)
// 根因修复 #1: http.DefaultClient 无超时, auth 调用可能永久阻塞
var authHTTPClient = &http.Client{Timeout: 30 * time.Second}

type TokenEndpointError struct {
	StatusCode       int
	OAuthError       string
	ErrorDescription string
}

func (e *TokenEndpointError) Error() string {
	message := e.ErrorDescription
	if message == "" {
		message = e.OAuthError
	}
	if message == "" {
		return fmt.Sprintf("token: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("token: HTTP %d: %s", e.StatusCode, message)
}

// ---------- Discovery ----------

// Discover 从 well-known 端点获取 Desktop OAuth 服务元数据。
// serverURL 可能含路径 (如 "https://acosmi.ai/api/v4")，
// well-known 端点按 RFC 8414 必须在 origin 根路径:
//
//	https://acosmi.ai/.well-known/oauth-authorization-server/desktop
func Discover(ctx context.Context, serverURL string) (*ServerMetadata, error) {
	parsed, err := url.Parse(strings.TrimRight(serverURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("discover: invalid server URL: %w", err)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	endpoint := origin + "/.well-known/oauth-authorization-server/desktop"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discover: HTTP %d", resp.StatusCode)
	}

	var meta ServerMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("discover: decode: %w", err)
	}

	// 根因修复 #12 (部分): 校验关键字段非空
	if meta.TokenEndpoint == "" || meta.AuthorizationEndpoint == "" {
		return nil, fmt.Errorf("discover: metadata missing required endpoints (token=%q, auth=%q)",
			meta.TokenEndpoint, meta.AuthorizationEndpoint)
	}

	return &meta, nil
}

// ---------- Dynamic Client Registration (RFC 7591) ----------

// Register 动态注册桌面客户端，获取 client_id
// 根因修复 #9: 使用 json.Marshal 构造请求体, 防止 appName 含引号时 JSON 注入
func Register(ctx context.Context, meta *ServerMetadata, appName string) (*ClientRegistration, error) {
	regReq := struct {
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		RedirectURIs            []string `json:"redirect_uris"`
		ResponseTypes           []string `json:"response_types"`
	}{
		ClientName:              appName,
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		RedirectURIs:            []string{"http://127.0.0.1/callback"},
		ResponseTypes:           []string{"code"},
	}
	body, err := json.Marshal(regReq)
	if err != nil {
		return nil, fmt.Errorf("register: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("register: HTTP %d", resp.StatusCode)
	}

	var reg ClientRegistration
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return nil, fmt.Errorf("register: decode: %w", err)
	}
	return &reg, nil
}

// ---------- PKCE ----------

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func codeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ---------- LoginWithHandler 事件模型 ----------

// LoginEventType 登录事件类型
type LoginEventType string

const (
	// EventAuthURL 授权 URL 已生成，调用方可展示/打开浏览器
	EventAuthURL LoginEventType = "auth_url"
	// EventComplete 登录完成
	EventComplete LoginEventType = "complete"
	// EventError 某步骤失败，附 ErrCode 分类码
	EventError LoginEventType = "error"
)

// LoginErrCode 登录错误分类码
type LoginErrCode string

const (
	ErrDiscovery     LoginErrCode = "discovery_failed"
	ErrRegistration  LoginErrCode = "registration_failed"
	ErrBrowserOpen   LoginErrCode = "browser_open_failed"
	ErrAuthDenied    LoginErrCode = "auth_denied"
	ErrTimeout       LoginErrCode = "auth_timeout"
	ErrTokenExchange LoginErrCode = "token_exchange_failed"
	ErrSSLProxy      LoginErrCode = "ssl_proxy_detected"
)

// LoginEvent 登录流程事件
type LoginEvent struct {
	Type    LoginEventType `json:"type"`
	URL     string         `json:"url,omitempty"`
	Error   string         `json:"error,omitempty"`
	ErrCode LoginErrCode   `json:"err_code,omitempty"`
}

// LoginOption 登录选项（函数选项模式）
type LoginOption func(*loginConfig)

type loginConfig struct {
	handler     func(LoginEvent)
	skipBrowser bool
	// 授权 URL 附加参数（审计 A-1 修复）
	loginHint   string // login_hint — SSO email 预填
	loginMethod string // login_method — 如 "sso"
	orgUUID     string // orgUUID — 强制组织登录
	expiresIn   int    // expires_in — 自定义 token 有效期（秒）
}

// WithSkipBrowser 跳过自动打开浏览器（由调用方控制浏览器）
func WithSkipBrowser() LoginOption {
	return func(cfg *loginConfig) { cfg.skipBrowser = true }
}

// WithLoginHint 设置 login_hint（SSO 场景下的 email 预填）
func WithLoginHint(hint string) LoginOption {
	return func(cfg *loginConfig) { cfg.loginHint = hint }
}

// WithLoginMethod 设置 login_method（如 "sso"）
func WithLoginMethod(method string) LoginOption {
	return func(cfg *loginConfig) { cfg.loginMethod = method }
}

// WithOrgUUID 设置 orgUUID（强制组织登录）
func WithOrgUUID(uuid string) LoginOption {
	return func(cfg *loginConfig) { cfg.orgUUID = uuid }
}

// WithExpiresIn 设置自定义 token 有效期（秒）
func WithExpiresIn(seconds int) LoginOption {
	return func(cfg *loginConfig) { cfg.expiresIn = seconds }
}

// isSSLError 检测 SSL/TLS 相关错误（企业代理 Zscaler 等）
func isSSLError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "tls:") ||
		strings.Contains(msg, "x509:") ||
		strings.Contains(msg, "certificate")
}

// ---------- Authorization Code Flow ----------

// AuthorizeResult 授权结果
type AuthorizeResult struct {
	Code        string
	RedirectURI string
}

// Authorize 执行 OAuth 2.1 PKCE 授权流程:
//  1. 启动本地 HTTP server
//  2. 打开浏览器让用户登录并授权
//  3. 接收回调拿到 authorization code
//  4. 返回 code 供后续 token 交换
//
// 签名不变 — CrabClaw 零影响。内部委托 authorizeInternal。
func Authorize(ctx context.Context, meta *ServerMetadata, clientID string, scopes []string) (*AuthorizeResult, string, error) {
	return authorizeInternal(ctx, meta, clientID, scopes, nil)
}

// authorizeInternal 共享实现，接受 *loginConfig 以支持事件回调和 skipBrowser
func authorizeInternal(ctx context.Context, meta *ServerMetadata, clientID string, scopes []string, cfg *loginConfig) (*AuthorizeResult, string, error) {
	if cfg == nil {
		cfg = &loginConfig{}
	}
	emit := func(e LoginEvent) {
		if cfg.handler != nil {
			cfg.handler(e)
		}
	}

	// 生成 PKCE
	verifier, err := generateCodeVerifier()
	if err != nil {
		return nil, "", fmt.Errorf("generate verifier: %w", err)
	}
	challenge := codeChallenge(verifier)

	// 启动本地 callback server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("listen: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errMsg := r.URL.Query().Get("error_description")
			if errMsg == "" {
				errMsg = r.URL.Query().Get("error")
			}
			errCh <- fmt.Errorf("authorization denied: %s", errMsg)
			// 根因修复 #8: XSS — 使用 html.EscapeString 转义用户输入
			fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>授权失败</title></head>`+
				`<body style="font-family:system-ui,sans-serif;text-align:center;padding:60px 20px">`+
				`<h2>授权失败</h2><p>%s</p>`+
				`<p style="color:#888;font-size:14px">可以关闭此窗口。</p>`+
				`</body></html>`, html.EscapeString(errMsg))
			return
		}
		codeCh <- code
		fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>授权成功</title></head>`+
			`<body style="font-family:system-ui,sans-serif;text-align:center;padding:60px 20px">`+
			`<h2>授权成功</h2>`+
			`<p>已完成身份认证，请返回应用继续使用。</p>`+
			`<p style="color:#888;font-size:14px">此窗口将在 3 秒后自动关闭…</p>`+
			`<script>setTimeout(function(){window.close()},3000)</script>`+
			`</body></html>`)
	})

	server := &http.Server{Handler: mux}
	go func() {
		if srvErr := server.Serve(listener); srvErr != http.ErrServerClosed {
			errCh <- srvErr
		}
	}()
	defer server.Shutdown(context.Background())

	// 根因修复 #12: 检查 url.Parse 错误, 防止 nil 指针解引用
	authURL, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return nil, "", fmt.Errorf("parse authorization endpoint: %w", err)
	}
	q := authURL.Query()
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	// 审计 A-1 修复: 转发授权 URL 附加参数
	if cfg.loginHint != "" {
		q.Set("login_hint", cfg.loginHint)
	}
	if cfg.loginMethod != "" {
		q.Set("login_method", cfg.loginMethod)
	}
	if cfg.orgUUID != "" {
		q.Set("orgUUID", cfg.orgUUID)
	}
	authURL.RawQuery = q.Encode()

	// 通知：授权 URL 就绪
	emit(LoginEvent{Type: EventAuthURL, URL: authURL.String()})

	// 打开浏览器（skipBrowser 时跳过）
	if cfg.skipBrowser {
		// 调用方控制浏览器，不自动打开
	} else if browserErr := openBrowser(authURL.String()); browserErr != nil {
		// 浏览器打不开不 return — 用户可通过 URL 手动打开
		emit(LoginEvent{
			Type:    EventError,
			ErrCode: ErrBrowserOpen,
			URL:     authURL.String(),
			Error:   browserErr.Error(),
		})
	}

	// 等待回调
	select {
	case code := <-codeCh:
		return &AuthorizeResult{Code: code, RedirectURI: redirectURI}, verifier, nil
	case authErr := <-errCh:
		// 所有 errCh 错误均 emit 事件
		if strings.Contains(authErr.Error(), "denied") {
			emit(LoginEvent{Type: EventError, ErrCode: ErrAuthDenied, Error: authErr.Error()})
		} else {
			emit(LoginEvent{Type: EventError, ErrCode: ErrTokenExchange, Error: authErr.Error()})
		}
		return nil, "", authErr
	case <-ctx.Done():
		emit(LoginEvent{Type: EventError, ErrCode: ErrTimeout, Error: "authorization timed out"})
		return nil, "", ctx.Err()
	}
}

// ---------- Token Exchange ----------

// ExchangeCode 用 authorization code 换取 token
func ExchangeCode(ctx context.Context, meta *ServerMetadata, clientID, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	return postToken(ctx, meta.TokenEndpoint, data)
}

// exchangeCodeWithExpiry 与 ExchangeCode 相同，但附带 expires_in 参数
// 审计 A-2 修复: setup-token 模式需要自定义 token 有效期
func exchangeCodeWithExpiry(ctx context.Context, meta *ServerMetadata, clientID, code, redirectURI, codeVerifier string, expiresIn int) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
		"expires_in":    {fmt.Sprintf("%d", expiresIn)},
	}

	return postToken(ctx, meta.TokenEndpoint, data)
}

// RefreshToken 刷新 access_token
func RefreshToken(ctx context.Context, meta *ServerMetadata, clientID, refreshToken string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
	}

	return postToken(ctx, meta.TokenEndpoint, data)
}

// RevokeToken 吊销 token
func RevokeToken(ctx context.Context, meta *ServerMetadata, token string) error {
	if meta.RevocationEndpoint == "" {
		return nil // 服务端不支持吊销, 静默跳过
	}

	data := url.Values{"token": {token}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RevocationEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	resp.Body.Close()
	return nil
}

func postToken(ctx context.Context, endpoint string, data url.Values) (*TokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, &TokenEndpointError{
			StatusCode:       resp.StatusCode,
			OAuthError:       errBody.Error,
			ErrorDescription: errBody.ErrorDescription,
		}
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("token: decode: %w", err)
	}
	return &tokenResp, nil
}

// NewTokenSet 从 TokenResponse 构造可持久化的 TokenSet
func NewTokenSet(resp *TokenResponse, clientID, serverURL string) *TokenSet {
	// 根因修复 #14: ExpiresIn=0 会导致 token 立即过期 → 无限刷新循环
	// 最少保证 60 秒有效期
	expiresIn := resp.ExpiresIn
	if expiresIn < 60 {
		expiresIn = 60
	}
	return &TokenSet{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
		Scope:        resp.Scope,
		ClientID:     clientID,
		ServerURL:    serverURL,
	}
}

// ---------- Browser ----------

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
