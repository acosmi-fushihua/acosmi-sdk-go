// adapter_anthropic.go — Anthropic 原生格式 adapter
//
// 重构自 buildChatRequest 现有逻辑，功能完全等同。
// 包含 buildBetas() 调用、ServerTools 合入、ExtraBody 透传。

package acosmi

import (
	"encoding/json"
	"fmt"
)

// AnthropicAdapter 实现 ProviderAdapter，用于 Anthropic 原生模型
type AnthropicAdapter struct{}

func (a *AnthropicAdapter) Format() ProviderFormat { return FormatAnthropic }
func (a *AnthropicAdapter) EndpointSuffix() string  { return "/anthropic" }

// BuildRequestBody 构建 Anthropic 格式请求体
// 逻辑等同于原 buildChatRequest，包含完整的 betas/tools/ServerTools/ExtraBody 处理
func (a *AnthropicAdapter) BuildRequestBody(caps ModelCapabilities, req *ChatRequest) (map[string]any, error) {
	body := make(map[string]any)

	// 消息: RawMessages 优先于 Messages
	if req.RawMessages != nil {
		body["messages"] = req.RawMessages
	} else if len(req.Messages) > 0 {
		body["messages"] = req.Messages
	}

	body["stream"] = req.Stream

	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.System != nil {
		body["system"] = req.System
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	// ── Thinking + Effort 组装 ──
	if req.Thinking != nil && req.Thinking.Level != "" {
		// Level 模式: SDK 接管 thinking + effort + maxTokens
		resolveThinkingLevel(body, req, caps)
	} else {
		// 兼容模式: 调用方自己拼 (保持 v0.8.0 行为)
		if req.Thinking != nil {
			body["thinking"] = req.Thinking
		}
		if req.Effort != nil {
			body["effort"] = req.Effort
		}
	}

	if req.Metadata != nil {
		body["metadata"] = req.Metadata
	}

	// ── 合入 Tools + ServerTools ──
	var allTools []any
	if req.Tools != nil {
		if toolsJSON, err := json.Marshal(req.Tools); err == nil {
			var parsed []any
			if json.Unmarshal(toolsJSON, &parsed) == nil {
				allTools = append(allTools, parsed...)
			}
		}
	}
	for _, st := range req.ServerTools {
		schema := map[string]any{
			"type": st.Type,
			"name": st.Name,
		}
		for k, v := range st.Config {
			schema[k] = v
		}
		allTools = append(allTools, schema)
	}
	if len(allTools) > 0 {
		body["tools"] = allTools
	}

	// ── 推理控制 (非 Level 模式时的透传) ──
	if req.Speed != "" {
		body["speed"] = req.Speed
	}
	if req.OutputConfig != nil {
		body["output_config"] = req.OutputConfig
	}

	// ── Beta 自动组装 ──
	betas := buildBetas(caps, req)
	if len(betas) > 0 {
		body["betas"] = betas
	}

	// ── 透传 ExtraBody ──
	// 注意: ExtraBody 在 resolveThinkingLevel 之后执行，
	// Level 模式下 thinking / effort / max_tokens / temperature 已由 SDK 管理，
	// ExtraBody 中不应包含这些 key，否则会覆盖 SDK 计算结果
	for k, v := range req.ExtraBody {
		body[k] = v
	}

	return body, nil
}

// resolveThinkingLevel 根据 ThinkingConfig.Level 自动组装请求参数
//
// off  → thinking=disabled, 不设 effort, 不动 maxTokens
// high → thinking=adaptive, effort=high, maxTokens 至少 32K
// max  → thinking=adaptive, effort=max, maxTokens 拉到模型上限
//
// 旧模型不支持 adaptive 时，回退到 enabled + budget_tokens = maxTokens - 1
// (旧模型上 effort 也不可用，仅靠 budget 控制深度)
func resolveThinkingLevel(body map[string]any, req *ChatRequest, caps ModelCapabilities) {
	level := req.Thinking.Level

	// ── off ──
	if level == ThinkingOff {
		body["thinking"] = map[string]any{"type": "disabled"}
		return
	}

	// ── 模型不支持任何形式的 thinking → 不动 maxTokens，直接返回 ──
	if !caps.SupportsAdaptiveThinking && !caps.SupportsThinking {
		return
	}

	// ── 确定 maxTokens ──
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = ThinkingHighMinMaxTokens
	}

	if level == ThinkingMax {
		// 深度: 拉到模型上限
		modelMax := caps.MaxOutputTokens
		if modelMax <= 0 {
			modelMax = ThinkingMaxFallbackMaxTokens
		}
		if maxTokens < modelMax {
			maxTokens = modelMax
		}
	} else {
		// high: 至少 32K
		if maxTokens < ThinkingHighMinMaxTokens {
			maxTokens = ThinkingHighMinMaxTokens
		}
	}
	body["max_tokens"] = maxTokens

	// ── thinking ──
	// adaptive 优先 (Claude 4.x); 旧模型回退 enabled + full budget
	if caps.SupportsAdaptiveThinking {
		thinking := map[string]any{"type": "adaptive"}
		if req.Thinking.Display != "" {
			thinking["display"] = req.Thinking.Display
		}
		body["thinking"] = thinking
	} else if caps.SupportsThinking {
		// 旧模型: enabled + budget = maxTokens - 1 (给满)
		budget := maxTokens - 1
		if budget < 1024 {
			budget = 1024
		}
		thinking := map[string]any{
			"type":          "enabled",
			"budget_tokens": budget,
		}
		if req.Thinking.Display != "" {
			thinking["display"] = req.Thinking.Display
		}
		body["thinking"] = thinking
	}

	// ── effort ──
	// 仅支持 effort 的模型发送此参数
	if caps.SupportsEffort {
		effortLevel := "high"
		if level == ThinkingMax && caps.SupportsMaxEffort {
			effortLevel = "max"
		}
		body["effort"] = map[string]any{"level": effortLevel}
	}

	// ── API 约束: thinking 与 temperature 互斥 ──
	delete(body, "temperature")
}

// ParseResponse 解析 Anthropic 格式同步响应
// 兼容 APIResponse 包装 {"code":0,"data":{...}} 和裸 Anthropic JSON 两种格式
func (a *AnthropicAdapter) ParseResponse(body []byte) (*ChatResponse, error) {
	// 尝试 APIResponse 包装
	var wrapper struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	raw := body
	if json.Unmarshal(body, &wrapper) == nil && len(wrapper.Data) > 0 && string(wrapper.Data) != "null" {
		if wrapper.Code != 0 {
			return nil, &BusinessError{Code: wrapper.Code, Message: wrapper.Message}
		}
		raw = wrapper.Data
	}

	var resp ChatResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	resp.TokenRemaining = -1
	resp.CallRemaining = -1
	resp.ModelTokenRemaining = -1
	resp.ModelTokenRemainingETU = -1
	return &resp, nil
}

// ParseStreamLine 解析 Anthropic SSE 行
// Anthropic 原生协议无 [DONE]（message_stop 后上游关闭连接），
// 但 Nexus Gateway 的 ChatStream 路径会追加 [DONE] 哨兵，此处一并处理。
func (a *AnthropicAdapter) ParseStreamLine(eventType, data string) (StreamEvent, bool, error) {
	if data == "[DONE]" {
		return StreamEvent{}, true, nil
	}
	return StreamEvent{Event: eventType, Data: data}, false, nil
}
