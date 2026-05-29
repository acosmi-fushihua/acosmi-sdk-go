// adapter.go — Provider Adapter 接口与注册表
//
// SDK 层按上游托管模型元数据选路：
//   PreferredFormat == "anthropic" → AnthropicAdapter → POST /managed-models/:id/anthropic
//   PreferredFormat == "openai"    → OpenAIAdapter    → POST /managed-models/:id/chat
//
// 旧上游若未返回 PreferredFormat / SupportedFormats，SDK 才回落到 provider 名称：
//   provider == "anthropic" / "acosmi" → AnthropicAdapter
//   其他 provider                     → OpenAIAdapter
//
// SDK 只负责：格式路由 + 请求结构转换 + 响应结构转换
// 厂商特定协议差异（endpoint/auth/region/字段裁剪）由 Nexus Gateway Profile 处理

package acosmi

import "strings"

// ProviderFormat 标识请求格式
type ProviderFormat int

const (
	FormatAnthropic ProviderFormat = iota // Anthropic 原生格式
	FormatOpenAI                          // OpenAI 兼容格式
)

// ProviderAdapter 定义了将 ChatRequest 转换为特定格式的接口
type ProviderAdapter interface {
	// Format 返回此 adapter 使用的请求格式
	Format() ProviderFormat

	// EndpointSuffix 返回 API 路径后缀
	// Anthropic: "/anthropic", OpenAI: "/chat"
	EndpointSuffix() string

	// BuildRequestBody 将 ChatRequest 转换为 HTTP body (map → json.Marshal)
	// caps 用于条件化字段注入（如 betas）
	BuildRequestBody(caps ModelCapabilities, req *ChatRequest) (map[string]any, error)

	// ParseResponse 解析同步响应 body 为 ChatResponse
	ParseResponse(body []byte) (*ChatResponse, error)

	// ParseStreamLine 解析一行 SSE data 为 StreamEvent
	// 返回 (event, done, error)
	// done=true 表示流结束（遇到 [DONE] 或 message_stop）
	ParseStreamLine(eventType, data string) (StreamEvent, bool, error)
}

// adapterRegistry 按 provider 名称映射 adapter
var adapterRegistry = map[string]ProviderAdapter{
	"anthropic": &AnthropicAdapter{},
	"acosmi":    &AnthropicAdapter{}, // Acosmi 自有模型走 Anthropic 格式
}

// defaultOpenAIAdapter 是非 Anthropic 厂商的默认 adapter
var defaultOpenAIAdapter ProviderAdapter = &OpenAIAdapter{}

// getAdapter 根据 provider 返回对应的 adapter (v0.5.0 遗留 API, 向后兼容)
// 新代码应使用 getAdapterForModel, 以便读取 ManagedModel 的格式能力字段
func getAdapter(provider string) ProviderAdapter {
	if a, ok := adapterRegistry[strings.ToLower(provider)]; ok {
		return a
	}
	return defaultOpenAIAdapter
}

// getAdapterForModel 按 ManagedModel 的 PreferredFormat / SupportedFormats 选择 adapter
//
// 决策顺序:
//  1. PreferredFormat 非空 **且** 该格式在 SupportedFormats 内 (或 SupportedFormats 未声明)
//     → 按其值返回 (anthropic | openai)
//  2. SupportedFormats 含 "anthropic" → AnthropicAdapter
//  3. SupportedFormats 含 "openai" → OpenAIAdapter
//  4. 两字段均空 (旧上游) → 回落 provider 名硬编码 (原 getAdapter 行为)
//
// 这使得 dashscope / zhipu / deepseek 等 provider 的模型如果上游启用了
// Anthropic 兼容端点 (providerAnthropicEndpoints 命中), 也能走 /anthropic 路径,
// 不再被 provider 字符串硬编码到 /chat 导致 tool_reference 400.
//
// [格式一致性护栏 2026-05-29] PreferredFormat 仅在确被 SupportedFormats 收录时才采信:
// 防止上游元数据漂移 (preferred_format=anthropic 但 supported_formats=[openai]) 把 SDK
// 路由到模型并不支持的格式端点 (撞 /anthropic "未绑定 Anthropic" 4xx)。这是网关侧
// "同 model_id 双 profile 选行" 根因修复在 SDK 侧的同构护栏。
func getAdapterForModel(m ManagedModel) ProviderAdapter {
	hasAnthropic := false
	hasOpenAI := false
	for _, f := range m.SupportedFormats {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "anthropic":
			hasAnthropic = true
		case "openai":
			hasOpenAI = true
		}
	}
	declared := hasAnthropic || hasOpenAI

	switch strings.ToLower(strings.TrimSpace(m.PreferredFormat)) {
	case "anthropic":
		if !declared || hasAnthropic {
			return &AnthropicAdapter{}
		}
	case "openai":
		if !declared || hasOpenAI {
			return &OpenAIAdapter{}
		}
	}

	if hasAnthropic {
		return &AnthropicAdapter{}
	}
	if hasOpenAI {
		return &OpenAIAdapter{}
	}

	// 旧上游未填字段: 回落到 provider 名硬编码 (向后兼容)
	return getAdapter(strings.ToLower(m.Provider))
}
