package acosmi

// =============================================================================
// 会员委托 Key (agent access key) 控制面客户端
//
// 服务端契约: nexus-v4 /api/v4/agent-access-keys (见 handler/agent_access_key_admin.go)
// 需要 OAuth token 携带 scope `agent_access:manage`, 且必须是**有同意页**的桌面/设备授权流
// 或同源登录态 —— web_oauth token 会被服务端拒 (方案红线 12)。
//
// 明文只在创建/轮换时返回一次, 之后服务端只存 HMAC。调用方必须立刻把它写进目标配置,
// 且**绝不**落日志 (方案红线 9)。
// =============================================================================

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// AgentAccessKey 会员委托 Key 的元数据 (不含明文)。
type AgentAccessKey struct {
	ID             string     `json:"id"`
	UserID         string     `json:"userId"`
	TenantID       string     `json:"tenantId"`
	Name           string     `json:"name"`
	KeyPrefix      string     `json:"keyPrefix"`
	Scopes         string     `json:"scopes"`
	BillingMode    string     `json:"billingMode"`
	CredentialKind string     `json:"credentialKind"`
	TargetAgent    string     `json:"targetAgent,omitempty"`
	Purpose        string     `json:"purpose,omitempty"`
	ClientID       string     `json:"clientId,omitempty"`
	RateLimitRPM   *int       `json:"rateLimitRpm,omitempty"`
	Concurrency    *int       `json:"concurrencyLimit,omitempty"`
	LastUsedAt     *time.Time `json:"lastUsedAt,omitempty"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty"`
	RevokedAt      *time.Time `json:"revokedAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
}

// AgentAccessKeyCreated 创建/轮换结果 —— Plaintext 仅此一次可得。
type AgentAccessKeyCreated struct {
	Key       AgentAccessKey `json:"key"`
	Plaintext string         `json:"plaintext"`
}

// CreateAgentAccessKeyRequest 签发请求。
//
// 刻意**没有** billingMode / credentialKind 字段: 资金来源由端点决定, 客户端无法表达 (红线 5)。
type CreateAgentAccessKeyRequest struct {
	Name        string   `json:"name"`
	TargetAgent string   `json:"targetAgent,omitempty"`
	Purpose     string   `json:"purpose,omitempty"`
	Scopes      string   `json:"scopes,omitempty"`      // 空格分隔; 空 = models:chat
	ExpiresDays int      `json:"expiresDays,omitempty"` // 30/60/90; 其它值服务端归 90 (硬上限)
	Models      []string `json:"models,omitempty"`      // 模型 allowlist; 空 = 不额外限制
	RateLimitRPM     *int `json:"rateLimitRpm,omitempty"`     // 可下调不可上调
	ConcurrencyLimit *int `json:"concurrencyLimit,omitempty"` // 可下调不可上调
}

// AgentAccessKeyUsageRow 单模型用量聚合 (观测口径, 非账单)。
type AgentAccessKeyUsageRow struct {
	ModelID      string `json:"modelId"`
	Calls        int64  `json:"calls"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// ListAgentAccessKeys 列出当前用户未吊销的会员委托 Key。
func (c *Client) ListAgentAccessKeys(ctx context.Context) ([]AgentAccessKey, error) {
	var out []AgentAccessKey
	if err := c.doJSON(ctx, "GET", "/api/v4/agent-access-keys", nil, &out, false); err != nil {
		return nil, fmt.Errorf("list agent access keys: %w", err)
	}
	return out, nil
}

// CreateAgentAccessKey 签发一枚会员委托 Key。返回值里的 Plaintext 只此一次。
func (c *Client) CreateAgentAccessKey(ctx context.Context, req CreateAgentAccessKeyRequest) (*AgentAccessKeyCreated, error) {
	var out AgentAccessKeyCreated
	if err := c.doJSON(ctx, "POST", "/api/v4/agent-access-keys", req, &out, false); err != nil {
		return nil, fmt.Errorf("create agent access key: %w", err)
	}
	return &out, nil
}

// RotateAgentAccessKey 轮换: 签发新 Key 并吊销旧的。返回新明文 (仅此一次)。
func (c *Client) RotateAgentAccessKey(ctx context.Context, id string) (*AgentAccessKeyCreated, error) {
	var out AgentAccessKeyCreated
	path := "/api/v4/agent-access-keys/" + url.PathEscape(id) + "/rotate"
	if err := c.doJSON(ctx, "POST", path, nil, &out, false); err != nil {
		return nil, fmt.Errorf("rotate agent access key: %w", err)
	}
	return &out, nil
}

// RevokeAgentAccessKey 吊销一枚会员委托 Key (幂等)。
func (c *Client) RevokeAgentAccessKey(ctx context.Context, id string) error {
	path := "/api/v4/agent-access-keys/" + url.PathEscape(id)
	if err := c.doJSON(ctx, "DELETE", path, nil, nil, false); err != nil {
		return fmt.Errorf("revoke agent access key: %w", err)
	}
	return nil
}

// AgentAccessKeyUsage 查一枚 Key 最近 days 天的用量 (按模型聚合)。
func (c *Client) AgentAccessKeyUsage(ctx context.Context, id string, days int) ([]AgentAccessKeyUsageRow, error) {
	if days <= 0 {
		days = 7
	}
	var out struct {
		Days int                      `json:"days"`
		Rows []AgentAccessKeyUsageRow `json:"rows"`
	}
	path := fmt.Sprintf("/api/v4/agent-access-keys/%s/usage?days=%d", url.PathEscape(id), days)
	if err := c.doJSON(ctx, "GET", path, nil, &out, false); err != nil {
		return nil, fmt.Errorf("agent access key usage: %w", err)
	}
	return out.Rows, nil
}
