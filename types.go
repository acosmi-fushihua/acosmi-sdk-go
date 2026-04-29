package acosmi

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------- OAuth ----------

// ServerMetadata OAuth Authorization Server 元数据 (RFC 8414)
type ServerMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RevocationEndpoint    string   `json:"revocation_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`
}

// TokenResponse OAuth token 响应
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// TokenSet 持久化 token 对
type TokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scope        string    `json:"scope"`
	ClientID     string    `json:"client_id"`
	ServerURL    string    `json:"server_url"`
}

// IsExpired token 是否已过期 (提前 30 秒视为过期)
func (t *TokenSet) IsExpired() bool {
	return time.Now().After(t.ExpiresAt.Add(-30 * time.Second))
}

// ClientRegistration 动态注册响应
type ClientRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// ---------- Managed Models ----------

// ManagedModel 托管模型
type ManagedModel struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Provider      string            `json:"provider"`
	ModelID       string            `json:"modelId"`
	MaxTokens     int               `json:"maxTokens"`
	IsEnabled     bool              `json:"isEnabled"`
	PricePerMTok  float64           `json:"pricePerMTok,omitempty"`
	IsDefault     bool              `json:"isDefault,omitempty"`
	ContextWindow int               `json:"contextWindow,omitempty"`
	Capabilities  ModelCapabilities `json:"capabilities"` // 模型能力矩阵 (CrabCode 消费)

	// SupportedFormats 上游 gateway 为此模型启用的请求格式列表
	// 取值: "anthropic" | "openai"
	// 示例: ["anthropic"] / ["openai"] / ["anthropic","openai"]
	// 空值表示上游未声明, SDK 回落 provider 硬编码分支 (向后兼容)
	SupportedFormats []string `json:"supported_formats,omitempty"`

	// PreferredFormat 上游建议客户端优先使用的格式
	// 取值: "anthropic" | "openai"; 空值等价于 SupportedFormats[0]
	PreferredFormat string `json:"preferred_format,omitempty"`
}

// ModelCapabilities 模型能力矩阵
// 下游通过此结构决定 UI 功能开关和 Beta Header 注入
type ModelCapabilities struct {
	// 思考能力
	SupportsThinking         bool `json:"supports_thinking"`
	SupportsAdaptiveThinking bool `json:"supports_adaptive_thinking"`
	SupportsISP              bool `json:"supports_isp"` // 交错思考 (Interleaved Thinking)

	// 工具与搜索
	SupportsWebSearch        bool `json:"supports_web_search"`
	SupportsToolSearch       bool `json:"supports_tool_search"`
	SupportsStructuredOutput bool `json:"supports_structured_output"`

	// 推理控制
	SupportsEffort bool `json:"supports_effort"`
	// SupportsMaxEffort 模型是否支持 thinking_level="max" 强度档 (深度思考)。
	// 与模型家族无关, 仅取决于上游路径是否接受 effort=max 请求:
	//   - Anthropic Opus 4.6: 原生支持, 顶层 effort.level=max
	//   - DeepSeek-V4-Pro/Flash (官方 anthropic 兼容端点): 支持, gateway 经
	//     EffortHandling=ToOutputConfig 翻译为 output_config.effort=max
	//   - 上游不接受或仍未实测: false (即便置 true, adapter_anthropic.go:182
	//     仍会降到 effort=high, 不会引发 400)
	// SDK 单点消费: adapter_anthropic.go ChatRequest 序列化时控制是否发 effort.level=max。
	SupportsMaxEffort bool `json:"supports_max_effort"`
	SupportsFastMode  bool `json:"supports_fast_mode"` // Opus 4.6 独有 (Speed="fast")
	SupportsAutoMode  bool `json:"supports_auto_mode"` // Auto 模式 (模型自主选择工具/搜索策略)
	// (v0.16.0 移除 SupportsDeepThinking 死字段, 改用 SupportsMaxEffort; 见 § 12 版本记录)

	// 上下文与缓存
	Supports1MContext    bool `json:"supports_1m_context"`
	SupportsPromptCache  bool `json:"supports_prompt_cache"`
	SupportsCacheEditing bool `json:"supports_cache_editing"` // 通过 context-management beta 控制

	// 输出控制
	SupportsTokenEfficient bool `json:"supports_token_efficient"` // Claude 4 内置
	SupportsRedactThinking bool `json:"supports_redact_thinking"`

	// Token 上限 (冗余但便于查询)
	MaxInputTokens  int `json:"max_input_tokens"`
	MaxOutputTokens int `json:"max_output_tokens"`
}

// [RC-2] ModelUsage 已移除: /managed-models/usage 端点已迁移至 tk-dist

// ChatMessage 聊天消息 (简单文本格式, CrabClaw 使用)
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatContentBlock Anthropic 响应内容块 (v0.4.1)
type ChatContentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	Citations  interface{}     `json:"citations,omitempty"`
	Thinking   string          `json:"thinking,omitempty"`
	Signature  string          `json:"signature,omitempty"`
	Data       string          `json:"data,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	ServerName string          `json:"server_name,omitempty"`
	Caller     interface{}     `json:"caller,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	Content    interface{}     `json:"content,omitempty"`
	IsError    *bool           `json:"is_error,omitempty"`
}

// ChatUsage Anthropic 格式 token 用量 (v0.4.1)
type ChatUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	CacheCreationInput int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInput     int `json:"cache_read_input_tokens,omitempty"`
}

// ChatRequest 聊天请求
// 基础字段供 CrabClaw 使用, 扩展字段供 CrabCode 使用
// 所有新增字段零值不改变行为 (向后兼容)
type ChatRequest struct {
	// ── 基础字段 (CrabClaw 兼容) ──
	Messages  []ChatMessage `json:"messages"`
	Stream    bool          `json:"stream,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`

	// ── 完整请求控制 (CrabCode 扩展) ──
	// 以下字段全部 json:"-", 仅通过 buildChatRequest 序列化, 防止直接 json.Marshal 产生不完整输出
	RawMessages interface{}            `json:"-"` // 复杂消息体 (含 content blocks / 多模态), 非 nil 时优先于 Messages
	System      interface{}            `json:"-"` // string 或 []ContentBlock
	Tools       interface{}            `json:"-"` // []Tool 标准工具定义
	Temperature *float64               `json:"-"`
	Thinking    *ThinkingConfig        `json:"-"` // 思考配置
	Metadata    map[string]string      `json:"-"`
	Betas       []string               `json:"-"` // 显式 beta (SDK 自动合并)
	ServerTools []ServerTool           `json:"-"` // 服务端工具 (buildChatRequest 合入 tools 数组)
	Speed       string                 `json:"-"` // "" | "fast" (Fast Mode)
	Effort      *EffortConfig          `json:"-"` // 推理努力级别
	OutputConfig *OutputConfig         `json:"-"` // 结构化输出配置
	ExtraBody   map[string]interface{} `json:"-"` // 任意扩展字段 (buildChatRequest 合入请求体)

	// v0.13.0: OpenAI wire format 原生字段。AnthropicAdapter 忽略,
	// OpenAIAdapter 按 OpenAI 规范序列化。
	//
	// ParallelToolCalls 对应 OpenAI `parallel_tool_calls` 顶层字段,
	// 控制模型能否在单次响应中发多个 tool_call。nil = 不设置 (沿用上游默认 true)。
	ParallelToolCalls *bool `json:"-"`
}

// ---------- Chat 扩展类型 ----------

// ThinkingLevel 三档思考级别 (v0.9.0)
// 下游只需传 Level，SDK 自动组装 thinking + effort + maxTokens
const (
	ThinkingOff  = "off"  // 关闭: thinking=disabled, 不发 effort
	ThinkingHigh = "high" // 标准: thinking=adaptive, effort=high, maxTokens≥32K
	ThinkingMax  = "max"  // 深度: thinking=adaptive, effort=max, maxTokens=模型上限
)

// ThinkingHighMinMaxTokens 标准思考最低 maxTokens
// 依据: CrabCode 默认 MAX_OUTPUT_TOKENS_DEFAULT = 32_000
const ThinkingHighMinMaxTokens = 32_000

// ThinkingMaxFallbackMaxTokens 深度思考回退 maxTokens
// 当 caps.MaxOutputTokens 不可用时使用
// 依据: Opus 4.6 上限 128K
const ThinkingMaxFallbackMaxTokens = 128_000

// ThinkingConfig 控制模型思考行为
type ThinkingConfig struct {
	Type         string `json:"type"`                    // "enabled" | "disabled" | "adaptive"
	BudgetTokens int    `json:"budget_tokens,omitempty"` // 仅 type="enabled" 时 (旧模型回退)

	// Level 思考级别 (v0.9.0): "off" | "high" | "max"
	// 设置后 SDK 在 BuildRequestBody 中自动:
	//   1. 设置 thinking 类型 (adaptive 优先, 旧模型回退 enabled)
	//   2. 设置 effort 参数
	//   3. 必要时拉高 max_tokens
	Level string `json:"level,omitempty"`

	Display string `json:"display,omitempty"` // "none" | "summary" | "" (默认空=完整)
}

// NewThinkingConfig 根据三档 level 创建配置
// off → disabled; high/max → adaptive + Level (SDK 序列化时自动组装完整参数)
func NewThinkingConfig(level string) *ThinkingConfig {
	if level == "" || level == ThinkingOff {
		return &ThinkingConfig{Type: "disabled"}
	}
	return &ThinkingConfig{Type: "adaptive", Level: level}
}

// ServerTool 定义服务端执行的工具
// SDK 将这些工具 schema 合入 API 请求的 tools 数组
type ServerTool struct {
	Type   string                 `json:"type"`             // 工具类型标识
	Name   string                 `json:"name"`             // 工具名称
	Config map[string]interface{} `json:"config,omitempty"` // 工具特定配置
}

// Server Tool 类型常量
const (
	ServerToolTypeWebSearch = "web_search_20250305" // 联网搜索 (Brave Search)
)

// WebSearchConfig — ServerTool.Config 结构 (type=web_search_20250305)
// 使用方式: ServerTool{Type: ServerToolTypeWebSearch, Name: "web_search", Config: WebSearchConfigToMap(cfg)}
type WebSearchConfig struct {
	MaxUses        int      `json:"max_uses,omitempty"`         // 每请求最大搜索次数, 默认 8
	AllowedDomains []string `json:"allowed_domains,omitempty"`  // 域名白名单 (与 BlockedDomains 互斥)
	BlockedDomains []string `json:"blocked_domains,omitempty"`  // 域名黑名单 (与 AllowedDomains 互斥)
	UserLocation   *GeoLoc  `json:"user_location,omitempty"`    // 搜索地域偏好
}

// GeoLoc 地理位置
type GeoLoc struct {
	Country string `json:"country"` // ISO 3166-1 alpha-2
	City    string `json:"city,omitempty"`
}

// EffortConfig 控制推理努力级别
type EffortConfig struct {
	// Level 取值 "low" | "medium" | "high" | "max"。
	// "max" 是否被采用取决于 caps.SupportsMaxEffort: 不支持的模型 SDK 自动降到 "high"
	// (adapter_anthropic.go:182), 不会引发上游 400。
	Level string `json:"level"`
}

// OutputConfig 控制输出格式 (结构化输出)
type OutputConfig struct {
	Format string      `json:"format,omitempty"` // "json_schema" | ""
	Schema interface{} `json:"schema,omitempty"` // JSON Schema 定义
}

// NewWebSearchTool 创建搜索 Server Tool 的便捷方法
// AllowedDomains 与 BlockedDomains 互斥, 同时传入返回错误
func NewWebSearchTool(cfg *WebSearchConfig) (ServerTool, error) {
	st := ServerTool{
		Type: ServerToolTypeWebSearch,
		Name: "web_search",
	}
	if cfg != nil {
		if len(cfg.AllowedDomains) > 0 && len(cfg.BlockedDomains) > 0 {
			return st, fmt.Errorf("web search: allowed_domains and blocked_domains are mutually exclusive")
		}
		m := make(map[string]interface{})
		if cfg.MaxUses > 0 {
			m["max_uses"] = cfg.MaxUses
		}
		if len(cfg.AllowedDomains) > 0 {
			m["allowed_domains"] = cfg.AllowedDomains
		}
		if len(cfg.BlockedDomains) > 0 {
			m["blocked_domains"] = cfg.BlockedDomains
		}
		if cfg.UserLocation != nil {
			m["user_location"] = cfg.UserLocation
		}
		if len(m) > 0 {
			st.Config = m
		}
	}
	return st, nil
}

// ---------- Web Search Sources ----------

// WebSearchSource 联网搜索结果来源 (与后端 adk/stream_helpers.go SourceItem 对齐)
type WebSearchSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

// SourcesEvent 搜索来源事件 (从 SSE "sources" 事件解析)
// ADK 路径: data: {"type":"sources","sources":[...],"session_id":"..."}
// Gateway 路径: 由 Gateway 从上游响应中提取后注入
type SourcesEvent struct {
	Sources   []WebSearchSource `json:"sources"`
	SessionID string            `json:"session_id,omitempty"`
}

// ParseSourcesEvent 从 StreamEvent 中解析搜索来源
// 返回 nil 表示该事件不是 sources 类型
func ParseSourcesEvent(ev StreamEvent) *SourcesEvent {
	// 方式 1: event 字段为 "sources"
	// 方式 2: data JSON 中 type 字段为 "sources"
	var wrapper struct {
		Type    string            `json:"type"`
		Sources []WebSearchSource `json:"sources"`
		SID     string            `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(ev.Data), &wrapper); err != nil {
		return nil
	}
	if wrapper.Type != "sources" && ev.Event != "sources" {
		return nil
	}
	if len(wrapper.Sources) == 0 {
		return nil
	}
	return &SourcesEvent{Sources: wrapper.Sources, SessionID: wrapper.SID}
}

// ---------- OpenAI 兼容响应类型 (非 Anthropic 厂商) ----------

// OpenAIChatResponse — OpenAI /chat/completions 同步响应
type OpenAIChatResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"` // "chat.completion"
	Model   string           `json:"model"`
	Choices []OpenAIChatChoice `json:"choices"`
	Usage   OpenAIUsage      `json:"usage"`
}

// OpenAIChatChoice — OpenAI 同步响应中的单个选项
type OpenAIChatChoice struct {
	Index        int              `json:"index"`
	Message      OpenAIChatMessage `json:"message"`
	FinishReason string           `json:"finish_reason"` // "stop", "tool_calls", "length"
}

// OpenAIChatMessage — OpenAI 同步响应中的消息体
type OpenAIChatMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"` // GLM/DeepSeek thinking
}

// OpenAIToolCall — OpenAI tool_call 结构
type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function OpenAIFunctionCall `json:"function"`
}

// OpenAIFunctionCall — OpenAI function call 内容
type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// OpenAIUsage — OpenAI token 用量
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIStreamChunk — OpenAI SSE delta 格式
type OpenAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"` // "chat.completion.chunk"
	Choices []OpenAIStreamChoice `json:"choices"`
	Usage   *OpenAIUsage         `json:"usage,omitempty"`
}

// OpenAIStreamChoice — OpenAI 流式响应中的单个选项
type OpenAIStreamChoice struct {
	Index        int              `json:"index"`
	Delta        OpenAIStreamDelta `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
}

// OpenAIStreamDelta — OpenAI 流式增量内容
type OpenAIStreamDelta struct {
	Role             string                 `json:"role,omitempty"`
	Content          string                 `json:"content,omitempty"`
	ReasoningContent string                 `json:"reasoning_content,omitempty"`
	ToolCalls        []OpenAIStreamToolCall `json:"tool_calls,omitempty"`
}

// OpenAIStreamToolCall — OpenAI 流式 tool_call 增量
type OpenAIStreamToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function OpenAIFunctionCall `json:"function"`
}

// ChatResponse 同步聊天响应 (Anthropic format, v0.4.1)
type ChatResponse struct {
	ID         string             `json:"id"`
	Type       string             `json:"type"`
	Model      string             `json:"model"`
	Role       string             `json:"role"`
	Content    []ChatContentBlock `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      ChatUsage          `json:"usage"`
	// 结算后余额 (从响应 Header X-Token-Remaining / X-Call-Remaining 填充)
	// -1 表示服务端未返回
	TokenRemaining int64 `json:"-"`
	CallRemaining  int   `json:"-"`
	// V29 (v0.16.0+): 当前模型剩余 (从响应头 X-Token-Remaining-Model[-ETU] 填充)
	// adapter 初始化为 -1 = 未返回 (与 TokenRemaining 哨兵语义一致); ≥ 0 表示真实值
	ModelTokenRemaining    int64 `json:"-"`
	ModelTokenRemainingETU int64 `json:"-"`
}

// AnthropicResponse Anthropic 原生格式同步响应
// POST /managed-models/:id/anthropic 返回此格式 (无 response.Success 包装)
type AnthropicResponse struct {
	ID           string                   `json:"id"`
	Type         string                   `json:"type"`            // "message"
	Role         string                   `json:"role"`            // "assistant"
	Content      []AnthropicContentBlock  `json:"content"`
	Model        string                   `json:"model"`
	StopReason   string                   `json:"stop_reason"`
	StopSequence *string                  `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage           `json:"usage"`
}

// AnthropicContentBlock Anthropic 内容块
// 覆盖: text / thinking / redacted_thinking / tool_use / tool_result /
//       server_tool_use / mcp_tool_use / mcp_tool_result
type AnthropicContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ID       string          `json:"id,omitempty"`                // tool_use / server_tool_use / mcp_tool_use block ID
	Name     string          `json:"name,omitempty"`              // tool_use function name
	Input    json.RawMessage `json:"input,omitempty"`             // tool_use arguments
	Thinking string          `json:"thinking,omitempty"`          // thinking block content

	// text — web_search 搜索引用
	Citations interface{} `json:"citations,omitempty"`

	// thinking — Anthropic 签名 (后续请求必须回传)
	Signature string `json:"signature,omitempty"`

	// redacted_thinking — base64 编码的被审查思考内容
	Data string `json:"data,omitempty"`

	// server_tool_use / mcp_tool_use / mcp_tool_result — 服务端工具来源
	ServerName string `json:"server_name,omitempty"`

	// mcp_tool_use — MCP 调用者上下文
	Caller interface{} `json:"caller,omitempty"`

	// tool_result / mcp_tool_result — 工具执行结果
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	IsError   *bool       `json:"is_error,omitempty"`
}

// AnthropicUsage Anthropic token 用量
type AnthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// TextContent 提取所有 text 类型内容块的文本，拼接返回
func (r *AnthropicResponse) TextContent() string {
	var parts []string
	for _, b := range r.Content {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "")
}

// ThinkingContent 提取所有 thinking 类型内容块的文本，拼接返回
func (r *AnthropicResponse) ThinkingContent() string {
	var parts []string
	for _, b := range r.Content {
		if b.Type == "thinking" && b.Thinking != "" {
			parts = append(parts, b.Thinking)
		}
	}
	return strings.Join(parts, "")
}

// ToolUseBlocks 返回所有 tool_use 类型的内容块
func (r *AnthropicResponse) ToolUseBlocks() []AnthropicContentBlock {
	var blocks []AnthropicContentBlock
	for _, b := range r.Content {
		if b.Type == "tool_use" {
			blocks = append(blocks, b)
		}
	}
	return blocks
}

// StreamEvent SSE 流式事件
type StreamEvent struct {
	Event string `json:"event"`
	Data  string `json:"data"`

	// ── v0.11.0 新增: content block 元数据 (json:"-", 仅内存透传) ──
	// 零值等价 v0.10.0 行为, 未识别的事件 (message_start / message_delta /
	// message_stop / 非 Anthropic) 三字段全部零值。
	//
	// BlockIndex 对齐 Anthropic content_block_start/delta/stop 的 index 字段;
	// 其他事件为 0 (与合法 index=0 无法区分, 用 BlockType 非空判别)。
	BlockIndex int
	// BlockType 由 content_block_start 解析得到, delta/stop 从 index→type 映射查出。
	BlockType string
	// Ephemeral 为 true 表示网关标记此 block 下一轮不应回传。来源于
	// content_block_start.content_block.acosmi_ephemeral in-band 字段,
	// 由 SDK 解析后沿 blockTypeMap 传播给 delta/stop 事件。
	Ephemeral bool
}

// StreamSettlement 流式结算事件 (从 settled SSE 事件解析)
// 包含本次请求的 token 消耗及结算后的剩余余额
type StreamSettlement struct {
	RequestID      string `json:"requestId"`
	ConsumeStatus  string `json:"consumeStatus"`
	InputTokens    int    `json:"inputTokens"`
	OutputTokens   int    `json:"outputTokens"`
	TotalTokens    int    `json:"totalTokens"`
	TokenRemaining int64  `json:"tokenRemaining"` // 结算后剩余 token (-1 表示服务端未返回)
	CallRemaining  int    `json:"callRemaining"`  // 结算后剩余调用次数 (-1 表示服务端未返回)
}

// ParseSettlement 从 settled 类型的 StreamEvent 中解析结算信息
// 如果事件不是 settled 类型，返回 nil
func ParseSettlement(ev StreamEvent) *StreamSettlement {
	if ev.Event != "settled" && ev.Event != "pending_settle" {
		return nil
	}
	var s StreamSettlement
	s.TokenRemaining = -1
	s.CallRemaining = -1
	if err := json.Unmarshal([]byte(ev.Data), &s); err != nil {
		return nil
	}
	return &s
}

// ---------- Entitlements ----------

// EntitlementBalance 权益余额 (聚合)
type EntitlementBalance struct {
	TotalTokenQuota     int64 `json:"totalTokenQuota"`
	TotalTokenUsed      int64 `json:"totalTokenUsed"`
	TotalTokenRemaining int64 `json:"totalTokenRemaining"`
	TotalCallQuota      int   `json:"totalCallQuota"`
	TotalCallUsed       int   `json:"totalCallUsed"`
	TotalCallRemaining  int   `json:"totalCallRemaining"`
	ActiveEntitlements  int   `json:"activeEntitlements"`
}

// BalanceDetail 详细余额 (含每条权益明细)
type BalanceDetail struct {
	TotalTokenQuota     int64             `json:"totalTokenQuota"`
	TotalTokenUsed      int64             `json:"totalTokenUsed"`
	TotalTokenRemaining int64             `json:"totalTokenRemaining"`
	TotalCallQuota      int               `json:"totalCallQuota"`
	TotalCallUsed       int               `json:"totalCallUsed"`
	TotalCallRemaining  int               `json:"totalCallRemaining"`
	ActiveEntitlements  int               `json:"activeEntitlements"`
	Entitlements        []EntitlementItem  `json:"entitlements"`
}

// EntitlementItem 单条权益明细
type EntitlementItem struct {
	ID             string  `json:"id"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	TokenQuota     int64   `json:"tokenQuota"`
	TokenUsed      int64   `json:"tokenUsed"`
	TokenRemaining int64   `json:"tokenRemaining"`
	CallQuota      int     `json:"callQuota"`
	CallUsed       int     `json:"callUsed"`
	CallRemaining  int     `json:"callRemaining"`
	ExpiresAt      *string `json:"expiresAt,omitempty"`
	SourceID       string  `json:"sourceId,omitempty"`
	SourceType     string  `json:"sourceType,omitempty"`
	Remark         string  `json:"remark,omitempty"`
	CreatedAt      string  `json:"createdAt"`
}

// ConsumeRecord 核销记录
type ConsumeRecord struct {
	ID              string `json:"id"`
	EntitlementID   string `json:"entitlementId"`
	RequestID       string `json:"requestId"`
	ModelID         string `json:"modelId,omitempty"`
	TokensConsumed  int64  `json:"tokensConsumed"`
	Status          string `json:"status"`
	CreatedAt       string `json:"createdAt"`
}

// ---------- V29 Per-Model Bucket ----------

// ModelBucket 单桶视图 (用户多桶 hero / 模型切换提示用)
//
// 字段语义:
//   TokenQuota / TokenUsed / TokenRemaining 单位均为 ETU (折算后), 不是原始 token。
//   要展示原始 token 估算, 用 ListCoefficients 拿到的 OutputCoef 反向除。
//
// BucketClass:
//   "COMMERCIAL" — 套餐授予的精确桶 (model_id 精确匹配)
//   "GENERIC"    — 注册/邀请/月度免费的通配桶 (model_id="*", 限白名单模型)
type ModelBucket struct {
	BucketID           string  `json:"bucketId"`
	EntitlementID      string  `json:"entitlementId"`
	ModelID            string  `json:"modelId"`     // "*" = 通配
	BucketClass        string  `json:"bucketClass"` // COMMERCIAL / GENERIC
	TokenQuota         int64   `json:"tokenQuota"`
	TokenUsed          int64   `json:"tokenUsed"`
	TokenRemaining     int64   `json:"tokenRemaining"`
	CallQuota          int     `json:"callQuota"`
	CallUsed           int     `json:"callUsed"`
	CallRemaining      int     `json:"callRemaining"`
	CoefficientVersion int     `json:"coefficientVersion"`
	AllowedModelsJSON  string  `json:"allowedModelsJson,omitempty"`
}

// ModelByQuotaResponse GetByModel 响应; PrimaryBucket 在 BucketID 为空时表示无可用桶。
type ModelByQuotaResponse struct {
	ModelID           string      `json:"modelId"`
	ETURemaining      int64       `json:"etuRemaining"`      // 折算后剩余 (调度判定用)
	RawTokenRemaining int64       `json:"rawTokenRemaining"` // 反系数估算的原始 token (UI 展示用)
	HasQuota          bool        `json:"hasQuota"`
	PrimaryBucket     *ModelBucket `json:"primaryBucket,omitempty"`
}

// ModelCoefficient 单条模型系数 (SDK TTL 8s 缓存源)
type ModelCoefficient struct {
	ModelID           string  `json:"modelId"`
	TenantID          string  `json:"tenantId"`
	InputCoef         float64 `json:"inputCoef"`
	OutputCoef        float64 `json:"outputCoef"`
	CacheReadCoef     float64 `json:"cacheReadCoef"`
	CacheCreationCoef float64 `json:"cacheCreationCoef"`
	Version           int     `json:"version"`
	EffectiveAt       string  `json:"effectiveAt"`
}

// ConsumeRecordPage 核销记录分页响应
type ConsumeRecordPage struct {
	Records  []ConsumeRecord `json:"records"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
}

// ---------- Token Packages (商城) ----------

// TokenPackage 流量包商品
type TokenPackage struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	TokenQuota  int64       `json:"tokenQuota"`
	CallQuota   int         `json:"callQuota,omitempty"`
	Price       json.Number `json:"price"`
	ValidDays   int         `json:"validDays"`
	IsEnabled   bool        `json:"isEnabled"`
	SortOrder   int     `json:"sortOrder,omitempty"`
}

// Order 订单
type Order struct {
	ID          string      `json:"id"`
	PackageID   string      `json:"packageId"`
	PackageName string      `json:"packageName,omitempty"`
	Amount      json.Number `json:"amount"`
	Status      string      `json:"status"`
	PayURL      string      `json:"payUrl,omitempty"`
	CreatedAt   string      `json:"createdAt"`
}

// OrderStatus 订单状态
type OrderStatus struct {
	OrderID string `json:"orderId"`
	Status  string `json:"status"`
}

// [RC-12] OrderPage 已移除: 死代码, ListMyOrders 使用 []Order 直接返回

// PayPayload 下单请求
type PayPayload struct {
	PayMethod string `json:"payMethod,omitempty"`
}

// ---------- Wallet (钱包) ----------

// WalletStats 钱包统计
// 金额使用 json.Number 避免浮点精度丢失 (金融安全)
type WalletStats struct {
	Balance            json.Number `json:"balance"`
	MonthlyConsumption json.Number `json:"monthlyConsumption"`
	MonthlyRecharge    json.Number `json:"monthlyRecharge"`
	TransactionCount   int         `json:"transactionCount"`
}

// Transaction 交易记录
type Transaction struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Amount    json.Number `json:"amount"`
	Remark    string      `json:"remark,omitempty"`
	CreatedAt string      `json:"createdAt"`
}

// ---------- Skill Store ----------

// SkillStoreItem 技能商店中的技能
type SkillStoreItem struct {
	ID                  string  `json:"id"`
	PluginID            string  `json:"pluginId"`
	Key                 string  `json:"key"`
	Name                string  `json:"name"`
	Description         string  `json:"description"`
	Icon                string  `json:"icon"`
	Category            string  `json:"category"`
	InputSchema         string  `json:"inputSchema"`
	OutputSchema        string  `json:"outputSchema"`
	Timeout             int     `json:"timeout"`
	RetryCount          int     `json:"retryCount"`
	RetryDelay          int     `json:"retryDelay"`
	Version             string  `json:"version"`
	TotalCalls          int64   `json:"totalCalls"`
	AvgDurationMs       int64   `json:"avgDurationMs"`
	SuccessRate         float64 `json:"successRate"`
	IsEnabled           bool    `json:"isEnabled"`
	SecurityLevel       string  `json:"securityLevel"`
	SecurityScore       int     `json:"securityScore"`
	Scope               string  `json:"scope"`
	Status              string  `json:"status"`
	DownloadCount       int64   `json:"downloadCount"`
	Readme              string  `json:"readme"`
	Tags                []string `json:"tags"`
	Author              string  `json:"author"`
	PublisherID         string  `json:"publisherId"`
	IsPublished         bool    `json:"isPublished"`
	PluginName          string  `json:"pluginName"`
	PluginIcon          string  `json:"pluginIcon"`
	UpdatedAt           string  `json:"updatedAt"`
	Visibility          string  `json:"visibility,omitempty"`
	CertificationStatus string  `json:"certificationStatus,omitempty"`
	Source              string  `json:"source,omitempty"`
}

// SkillStoreQuery 技能商店搜索参数
type SkillStoreQuery struct {
	Category string
	Keyword  string
	Tag      string
}

// SkillSummary 技能统计概览
type SkillSummary struct {
	Installed      int64 `json:"installed"`
	Created        int64 `json:"created"`
	Total          int64 `json:"total"`
	StoreAvailable int64 `json:"storeAvailable"`
}

// SkillBrowseResponse 技能商店分页浏览响应
type SkillBrowseResponse struct {
	Items    []SkillStoreItem `json:"items"`
	Total    int64            `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"pageSize"`
}

// SkillStoreListItem 技能商店列表项（轻量，仅含浏览所需字段）
// 配合服务端 fields=minimal 参数使用，响应体积缩减 90%+
type SkillStoreListItem struct {
	ID                  string   `json:"id"`
	Key                 string   `json:"key"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	Icon                string   `json:"icon"`
	Category            string   `json:"category"`
	Version             string   `json:"version"`
	Author              string   `json:"author"`
	DownloadCount       int64    `json:"downloadCount"`
	Tags                []string `json:"tags"`
	CertificationStatus string   `json:"certificationStatus,omitempty"`
	Visibility          string   `json:"visibility,omitempty"`
	Source              string   `json:"source,omitempty"`
	UpdatedAt           string   `json:"updatedAt"`
}

// SkillBrowseListResponse 技能商店轻量浏览响应
type SkillBrowseListResponse struct {
	Items    []SkillStoreListItem `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"pageSize"`
}

// CertificationStatus 技能认证状态响应
type CertificationStatus struct {
	SkillID             string      `json:"skillId"`
	CertificationStatus string      `json:"certificationStatus"`
	CertifiedAt         *int64      `json:"certifiedAt,omitempty"`
	SecurityLevel       string      `json:"securityLevel,omitempty"`
	SecurityScore       int         `json:"securityScore"`
	Report              interface{} `json:"report,omitempty"`
}

// ---------- Skill Generator ----------

// GenerateSkillRequest 技能生成请求
type GenerateSkillRequest struct {
	Purpose     string   `json:"purpose"`
	Examples    []string `json:"examples,omitempty"`
	InputHints  string   `json:"inputHints,omitempty"`
	OutputHints string   `json:"outputHints,omitempty"`
	Category    string   `json:"category,omitempty"`
	Language    string   `json:"language,omitempty"`
}

// GenerateSkillResult 技能生成结果
type GenerateSkillResult struct {
	SkillName    string   `json:"skillName"`
	SkillKey     string   `json:"skillKey"`
	Description  string   `json:"description"`
	SkillMd      string   `json:"skillMd"`
	InputSchema  string   `json:"inputSchema"`
	OutputSchema string   `json:"outputSchema"`
	TestCases    []string `json:"testCases"`
	Readme       string   `json:"readme"`
	Category     string   `json:"category"`
	Tags         []string `json:"tags"`
	Timeout      int      `json:"timeout"`
}

// OptimizeSkillRequest 技能优化请求
type OptimizeSkillRequest struct {
	SkillName    string   `json:"skillName"`
	Description  string   `json:"description,omitempty"`
	InputSchema  string   `json:"inputSchema,omitempty"`
	OutputSchema string   `json:"outputSchema,omitempty"`
	Readme       string   `json:"readme,omitempty"`
	Aspects      []string `json:"aspects,omitempty"`
}

// OptimizeSkillResult 技能优化结果
type OptimizeSkillResult struct {
	OptimizedSkill GenerateSkillResult `json:"optimizedSkill"`
	Changes        []string            `json:"changes"`
	Score          int                 `json:"score"`
}

// ---------- Unified Tools ----------

// ToolView 统一工具视图
type ToolView struct {
	ID           string        `json:"id"`
	Key          string        `json:"key"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	Icon         string        `json:"icon"`
	Category     string        `json:"category"`
	InputSchema  string        `json:"inputSchema"`
	OutputSchema string        `json:"outputSchema"`
	Timeout      int           `json:"timeout"`
	IsEnabled    bool          `json:"isEnabled"`
	Provider     *ToolProvider `json:"provider,omitempty"`
}

// ToolProvider 工具的来源插件
type ToolProvider struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	SourceType  string `json:"sourceType"`
	MCPEndpoint string `json:"mcpEndpoint,omitempty"`
	IsEnabled   bool   `json:"isEnabled"`
}

// ToolListResponse 工具列表响应
type ToolListResponse struct {
	Skills []ToolView `json:"skills"`
	Total  int64      `json:"total"`
}

// ---------- Errors ----------

// RateLimitError 下载限流错误 (429)
type RateLimitError struct {
	Message    string
	RetryAfter string
	Raw        string
}

func (e *RateLimitError) Error() string { return e.Message }

// BusinessError API 业务层错误 (HTTP 200 但 code != 0)
// tk-dist 代理透传 yudao 响应, HTTP 状态码为 200, 业务错误在 JSON code 字段
// 调用方可用 errors.As 提取: var bizErr *acosmi.BusinessError
type BusinessError struct {
	Code    int
	Message string
}

func (e *BusinessError) Error() string {
	return fmt.Sprintf("API error (code=%d): %s", e.Code, e.Message)
}

// OrderTerminalError 订单到达非成功终态 (FAILED/CANCELLED/CLOSED/EXPIRED/REFUNDED)
// WaitForPayment 在订单终态为非成功时返回此错误
type OrderTerminalError struct {
	OrderID string
	Status  string
}

func (e *OrderTerminalError) Error() string {
	return fmt.Sprintf("order %s terminated: %s", e.OrderID, e.Status)
}

// ModelNotFoundError 模型缓存未命中且 ListModels 刷新后仍未找到。
//
// 历史上 getCachedModel miss 时硬返 ManagedModel{Provider:"anthropic"} 占位,
// 导致未预热场景下的 Chat 请求按 AnthropicAdapter 编码, 被发到错误端点。
// v0.13.x 改为 miss → ListModels 自动刷新一次; 仍 miss → 返回此错误。
//
// 调用方: 要么在 Login 后显式 ListModels 预热, 要么捕获本错误做重试/降级。
type ModelNotFoundError struct {
	ModelID string
}

func (e *ModelNotFoundError) Error() string {
	return fmt.Sprintf("managed model %q not found (list models to refresh cache, or verify model id)", e.ModelID)
}

// businessCodeChecker 内部接口, doJSON/doPublicJSON 解码后检查业务错误码
type businessCodeChecker interface {
	businessError() error
}

// ---------------------------------------------------------------------------
// L6 V2 P1 (2026-04-27, v0.15): 结构化 HTTP / 网络错误类型
//
// 老实现 parseHTTPError 返回 fmt.Errorf 字符串错误, 调用方无法 errors.As 出来分类.
// 网络错误 (timeout/EOF/connection refused) 直接 *net.OpError 透传, 无统一封装.
// L6 retry policy 必须基于结构化错误判断可重试 → 必须先做类型化 (V2 P1 前置).
//
// 文案兼容承诺: HTTPError.Error() 保留 "HTTP %d: %s" / "HTTP %d: [%s] %s" 格式;
// NetworkError.Error() 包含原始 op + url + cause.Error() — 老调用方字符串匹配 0 破坏.
// ---------------------------------------------------------------------------

// HTTPError 结构化 HTTP 非 2xx 错误.
//
// 用 errors.As 提取:
//
//	var he *acosmi.HTTPError
//	if errors.As(err, &he) {
//	    if he.StatusCode == 429 { ... } // 与 RateLimitError 不同, RateLimitError 仅下载链路用
//	}
type HTTPError struct {
	StatusCode int    // HTTP 状态码
	Type       string // anthropic.error.type / openai.error.type, 缺失为空
	Message    string // 错误消息
	RetryAfter int    // Retry-After 头解析的秒数, 0 表示未提供或解析失败
	Body       string // 原始响应体 (截断到 maxErrorBodySize)
}

func (e *HTTPError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("HTTP %d: [%s] %s", e.StatusCode, e.Type, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	if e.Body != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// NetworkError 结构化网络层错误 (传输失败, 区别于上游业务错误).
//
// 包装 c.http.Do 返回的 err — 含 timeout / EOF / connection refused / DNS 失败等.
// L6 retry policy: IsTimeout()/IsEOF() 任一为 true → 默认可重试 (与 SafeToRetry 配合).
type NetworkError struct {
	Op      string // 操作描述, e.g. "POST /v1/messages"
	URL     string // 请求 URL (脱敏后)
	Cause   error  // 原始 net 错误
	Timeout bool   // ctx.DeadlineExceeded / net.OpError.Timeout()
	EOF     bool   // io.EOF / "unexpected EOF" / "connection reset"
}

func (e *NetworkError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s %s: %s", e.Op, e.URL, e.Cause.Error())
	}
	return fmt.Sprintf("%s %s: network error", e.Op, e.URL)
}

func (e *NetworkError) Unwrap() error { return e.Cause }
func (e *NetworkError) IsTimeout() bool { return e.Timeout }
func (e *NetworkError) IsEOF() bool     { return e.EOF }

// ---------- API Response Wrapper ----------

// APIResponse nexus-v4 标准响应
// 兼容 yudao 格式 (msg) 和 nexus-v4 格式 (message)
type APIResponse[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Msg     string `json:"msg"`
	Data    T      `json:"data"`
}

// GetMessage 优先返回 message, 降级到 msg (兼容 yudao 透传)
func (r *APIResponse[T]) GetMessage() string {
	if r.Message != "" {
		return r.Message
	}
	return r.Msg
}

// businessError 检查业务层错误码 (实现 businessCodeChecker 接口)
// tk-dist 代理返回 HTTP 200 + {code: 500, msg: "余额不足"} 时,
// 此方法将其转换为 *BusinessError, 防止调用方收到零值数据+nil错误
func (r *APIResponse[T]) businessError() error {
	if r.Code != 0 {
		return &BusinessError{Code: r.Code, Message: r.GetMessage()}
	}
	return nil
}

// YudaoPageResult yudao 分页响应格式 (tk-dist 代理透传)
type YudaoPageResult[T any] struct {
	List  []T   `json:"list"`
	Total int64 `json:"total"`
}

// ---------- Notifications ----------

// Notification 单条通知
type Notification struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Type      string `json:"type"` // system | billing | security | task | commission | entitlement
	IsRead    bool   `json:"isRead"`
	CreatedAt string `json:"createdAt"`
}

// NotificationList 分页通知列表
type NotificationList struct {
	List        []Notification `json:"list"`
	UnreadCount int64          `json:"unreadCount"`
	Total       int64          `json:"total"`
	Page        int            `json:"page"`
	PageSize    int            `json:"pageSize"`
}

// NotificationUnreadCount 未读通知计数
type NotificationUnreadCount struct {
	UnreadCount int64 `json:"unreadCount"`
}

// NotificationPreference 通知偏好 (按类型+渠道)
type NotificationPreference struct {
	TypeCode     string `json:"typeCode"`
	ChannelInApp bool   `json:"channelInApp"`
	ChannelEmail bool   `json:"channelEmail"`
	ChannelSMS   bool   `json:"channelSms"`
	ChannelPush  bool   `json:"channelPush"`
}

// DeviceRegistration 推送设备注册
type DeviceRegistration struct {
	Platform   string `json:"platform"`   // android | ios | harmony
	Token      string `json:"token"`
	AppVersion string `json:"appVersion"`
}

// ParseNotificationEvent 从 WSEvent 中解析通知
// 返回 nil 表示该事件不是系统通知
func ParseNotificationEvent(ev WSEvent) *Notification {
	if ev.Type != "event" || ev.Topic != "system" {
		return nil
	}
	var n Notification
	if err := json.Unmarshal(ev.Data, &n); err != nil {
		return nil
	}
	if n.ID == "" {
		return nil
	}
	return &n
}
