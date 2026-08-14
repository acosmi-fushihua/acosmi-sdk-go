package acosmi

// =============================================================================
// OAuth 2.0 设备授权流客户端 (RFC 8628)
//
// 用途: SSH 会话 / 容器 / CI runner —— 这些环境既拉不起浏览器, 也不适合开 loopback 监听。
// 流程: 设备拿短用户码 → 人在**任意**有浏览器的设备上确认 → 设备侧轮询换 token。
// =============================================================================

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceGrantType RFC 8628 的 grant_type 字面量 (与服务端 mcp.DeviceGrantType 必须逐字一致)。
const DeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// DeviceAuthorization 设备授权请求的服务端响应 (RFC 8628 §3.2)。
type DeviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// ErrDeviceFlowUnsupported 服务端未开放设备流 (端点未注册 / metadata 未广告)。
var ErrDeviceFlowUnsupported = fmt.Errorf("authorization server does not support the device flow")

// DeviceAuthorizationEndpoint 从 AS metadata 解析设备授权端点。
// metadata 未广告该端点即视为未开放 —— 不猜路径, 猜出来的 404 只会让错误更难懂。
func DeviceAuthorizationEndpoint(meta *ServerMetadata) (string, error) {
	if meta == nil || strings.TrimSpace(meta.DeviceAuthorizationEndpoint) == "" {
		return "", ErrDeviceFlowUnsupported
	}
	return meta.DeviceAuthorizationEndpoint, nil
}

// RequestDeviceAuthorization 发起设备授权请求 (RFC 8628 §3.1)。
func RequestDeviceAuthorization(ctx context.Context, endpoint, clientID string, scopes []string) (*DeviceAuthorization, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	if len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("device authorization: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device authorization: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrDeviceFlowUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device authorization: unexpected status %d", resp.StatusCode)
	}
	var out DeviceAuthorization
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("device authorization: decode: %w", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return nil, fmt.Errorf("device authorization: server returned an incomplete response")
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	return &out, nil
}

// PollDeviceToken 按 RFC 8628 §3.4/§3.5 轮询换 token。
//
// 严格遵守服务端节流: 收到 slow_down 时**采纳服务端给的新 interval** 并继续等待。
// 客户端自作主张地快轮询是设备流最常见的实现错误 —— 它会让 AS 被迫做更严的限流,
// 最终伤害所有正常客户端。
func PollDeviceToken(ctx context.Context, tokenEndpoint, clientID string, auth *DeviceAuthorization) (*TokenResponse, error) {
	interval := time.Duration(auth.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	if auth.ExpiresIn <= 0 {
		deadline = time.Now().Add(10 * time.Minute)
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device authorization expired before approval; run login again")
		}

		form := url.Values{}
		form.Set("grant_type", DeviceGrantType)
		form.Set("device_code", auth.DeviceCode)
		form.Set("client_id", clientID)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, fmt.Errorf("device token: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		resp, err := authHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("device token: %w", err)
		}
		var payload struct {
			TokenResponse
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
			Interval         int    `json:"interval"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("device token: decode: %w", decodeErr)
		}

		if resp.StatusCode == http.StatusOK && payload.Error == "" {
			tr := payload.TokenResponse
			return &tr, nil
		}

		switch payload.Error {
		case "authorization_pending":
			// 继续等 —— 用户还没在浏览器上点确认。
		case "slow_down":
			if payload.Interval > 0 {
				interval = time.Duration(payload.Interval) * time.Second
			} else {
				interval += time.Second
			}
		case "access_denied":
			return nil, fmt.Errorf("authorization was denied in the browser")
		case "expired_token":
			return nil, fmt.Errorf("device authorization expired before approval; run login again")
		default:
			desc := payload.ErrorDescription
			if desc == "" {
				desc = payload.Error
			}
			return nil, fmt.Errorf("device token: %s", desc)
		}
	}
}

// LoginWithDeviceFlow 完整设备流: discover → register → device_authorization → 轮询 → 保存 token。
//
// onPrompt 回调拿到验证 URL 与用户码后由调用方决定怎么展示 (CLI 打印 / GUI 弹窗 / 二维码)。
// 回调返回后才开始轮询 —— 保证用户已经看到码。
func (c *Client) LoginWithDeviceFlow(ctx context.Context, clientName string, scopes []string, onPrompt func(*DeviceAuthorization)) error {
	meta, err := Discover(ctx, c.serverURL)
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	endpoint, err := DeviceAuthorizationEndpoint(meta)
	if err != nil {
		return err
	}
	reg, err := Register(ctx, meta, clientName)
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	auth, err := RequestDeviceAuthorization(ctx, endpoint, reg.ClientID, scopes)
	if err != nil {
		return err
	}
	if onPrompt != nil {
		onPrompt(auth)
	}
	tokenResp, err := PollDeviceToken(ctx, meta.TokenEndpoint, reg.ClientID, auth)
	if err != nil {
		return err
	}

	tokens := NewTokenSet(tokenResp, reg.ClientID, c.serverURL)
	c.mu.Lock()
	c.tokens = tokens
	c.tokenOnce.Do(func() { close(c.tokenReady) })
	c.mu.Unlock()
	if c.store != nil {
		if err := c.store.Save(tokens); err != nil {
			return fmt.Errorf("save tokens: %w", err)
		}
	}
	return nil
}
