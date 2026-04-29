// adapter_openai.go — OpenAI 兼容格式 adapter
//
// 用于所有非 Anthropic 厂商（DeepSeek、DashScope、Zhipu、Moonshot、VolcEngine 等）
// SDK 只做格式转换，厂商特定参数由 Nexus Gateway per-provider adapter 处理
//
// 关键区别：
//   - 不注入 Anthropic betas
//   - 端点后缀为 /chat（非 /anthropic）
//   - 流式使用 [DONE] 标记结束（非 message_stop）
//   - 响应为 OpenAI choices 格式

package acosmi

import (
	"encoding/json"
	"fmt"
)

// OpenAIAdapter 实现 ProviderAdapter，用于所有非 Anthropic 厂商
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) Format() ProviderFormat { return FormatOpenAI }
func (a *OpenAIAdapter) EndpointSuffix() string  { return "/chat" }

// resolveOpenAIReasoningEffort 把 Anthropic 心智模型的 Thinking/Effort
// 翻译成 OpenAI `reasoning_effort` 字段值。返回空串表示不设置。
func resolveOpenAIReasoningEffort(req *ChatRequest) string {
	// Effort 优先级最高, 因为它本身就是通用级别语义
	if req.Effort != nil && req.Effort.Level != "" {
		switch req.Effort.Level {
		case "low", "medium", "high":
			return req.Effort.Level
		case "max":
			// OpenAI 无 max 级别, 等价最深 = high
			return "high"
		}
	}
	// Thinking.Level 次之
	if req.Thinking != nil {
		switch req.Thinking.Level {
		case ThinkingHigh:
			return "high"
		case ThinkingMax:
			return "high"
		case ThinkingOff:
			return ""
		}
	}
	return ""
}

// resolveOpenAIResponseFormat 把 OutputConfig 翻译成 OpenAI response_format。
// 返回 nil 表示不设置。
func resolveOpenAIResponseFormat(req *ChatRequest) map[string]any {
	if req.OutputConfig == nil {
		return nil
	}
	switch req.OutputConfig.Format {
	case "json_schema":
		// OpenAI schema 形态: {type:"json_schema", json_schema:{schema:{...},strict:true}}
		js := map[string]any{}
		if req.OutputConfig.Schema != nil {
			js["schema"] = req.OutputConfig.Schema
		}
		// strict 默认开, 与 json_schema 模式语义一致
		js["strict"] = true
		return map[string]any{
			"type":        "json_schema",
			"json_schema": js,
		}
	case "json_object":
		return map[string]any{"type": "json_object"}
	case "":
		return nil
	default:
		// 未知 format, 原样透传, 交 Gateway 处理
		return map[string]any{"type": req.OutputConfig.Format}
	}
}

// BuildRequestBody 构建 OpenAI 兼容格式请求体
// 不注入 Anthropic betas，扩展字段（thinking/effort/speed）以通用 JSON 传递
func (a *OpenAIAdapter) BuildRequestBody(caps ModelCapabilities, req *ChatRequest) (map[string]any, error) {
	body := make(map[string]any)

	// ── 消息: 直接透传（Gateway 负责最终转换）──
	if req.RawMessages != nil {
		body["messages"] = req.RawMessages
	} else if len(req.Messages) > 0 {
		body["messages"] = req.Messages
	}

	body["stream"] = req.Stream
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	// ── System prompt: 透传给 Gateway ──
	if req.System != nil {
		body["system"] = req.System
	}

	// ── Temperature ──
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	// ── Tools: 透传原始格式，Gateway adapter 负责格式转换 ──
	if req.Tools != nil {
		body["tools"] = req.Tools
	}

	// ── 扩展字段 (v0.13.0: 按 OpenAI wire format 直接翻译) ──

	// Thinking / Effort → reasoning_effort
	// OpenAI 系只有顶层 `reasoning_effort: "low"|"medium"|"high"`, 无 thinking block;
	// 能接收到 reasoning_content (GLM/DeepSeek) 作为响应, 但请求侧只能控制级别。
	//
	// 翻译优先级:
	//   req.Effort.Level 非空  → 直接用 (低/中/高)
	//   req.Thinking.Level 非空 → ThinkingOff=不设; ThinkingHigh=high; ThinkingMax=high
	//                              (OpenAI 无 "max" 级别, 落到 high 等价最深)
	//   旧代码已手工设的 thinking 字段 → 保留 passthrough (ExtraBody 或调用方自设)
	if eff := resolveOpenAIReasoningEffort(req); eff != "" {
		body["reasoning_effort"] = eff
	}

	if req.Speed != "" {
		body["speed"] = req.Speed
	}

	// OutputConfig → response_format
	// Anthropic 心智模型通过 system prompt + prefill 实现 JSON 模式;
	// OpenAI 有顶层 response_format, SDK 直接翻译。
	if rf := resolveOpenAIResponseFormat(req); rf != nil {
		body["response_format"] = rf
	}

	if req.Metadata != nil {
		body["metadata"] = req.Metadata
	}

	// parallel_tool_calls 是 OpenAI 原生字段, 无歧义直接写。
	if req.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *req.ParallelToolCalls
	}

	// ── 不注入 Anthropic Betas ──
	// OpenAI 格式不使用 anthropic-beta header

	// ── 透传 ExtraBody ──
	for k, v := range req.ExtraBody {
		body[k] = v
	}

	// ── 流式选项 ──
	if req.Stream {
		body["stream_options"] = map[string]any{
			"include_usage": true,
		}
	}

	return body, nil
}

// ParseResponse 解析 OpenAI 格式同步响应为 ChatResponse
// 兼容 APIResponse 包装 {"code":0,"data":{...}} 和裸 OpenAI JSON 两种格式
func (a *OpenAIAdapter) ParseResponse(body []byte) (*ChatResponse, error) {
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

	var oaiResp OpenAIChatResponse
	if err := json.Unmarshal(raw, &oaiResp); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}

	return convertOpenAIToChatResponse(&oaiResp)
}

// convertOpenAIToChatResponse 将 OpenAI 同步响应转换为 ChatResponse
func convertOpenAIToChatResponse(oai *OpenAIChatResponse) (*ChatResponse, error) {
	resp := &ChatResponse{
		ID:             oai.ID,
		Type:           "message",
		Model:          oai.Model,
		Role:           "assistant",
		Usage: ChatUsage{
			InputTokens:  oai.Usage.PromptTokens,
			OutputTokens: oai.Usage.CompletionTokens,
		},
		TokenRemaining:         -1,
		CallRemaining:          -1,
		ModelTokenRemaining:    -1,
		ModelTokenRemainingETU: -1,
	}

	if len(oai.Choices) > 0 {
		choice := oai.Choices[0]

		// finish_reason 映射
		switch choice.FinishReason {
		case "stop":
			resp.StopReason = "end_turn"
		case "tool_calls":
			resp.StopReason = "tool_use"
		case "length":
			resp.StopReason = "max_tokens"
		default:
			resp.StopReason = choice.FinishReason
		}

		// thinking content → thinking block
		if choice.Message.ReasoningContent != "" {
			resp.Content = append(resp.Content, ChatContentBlock{
				Type:     "thinking",
				Thinking: choice.Message.ReasoningContent,
			})
		}

		// text content → text block
		if choice.Message.Content != "" {
			resp.Content = append(resp.Content, ChatContentBlock{
				Type: "text",
				Text: choice.Message.Content,
			})
		}

		// tool_calls → tool_use blocks
		for _, tc := range choice.Message.ToolCalls {
			resp.Content = append(resp.Content, ChatContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	return resp, nil
}

// ParseStreamLine 解析 OpenAI SSE 行
// [DONE] 标记流结束
func (a *OpenAIAdapter) ParseStreamLine(eventType, data string) (StreamEvent, bool, error) {
	if data == "[DONE]" {
		return StreamEvent{}, true, nil
	}

	var chunk OpenAIStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return StreamEvent{}, false, fmt.Errorf("parse openai stream chunk: %w", err)
	}

	return StreamEvent{Event: eventType, Data: data}, false, nil
}

// ---------------------------------------------------------------------------
// OpenAI → Anthropic 响应转换（供 ChatMessages 使用）
// ---------------------------------------------------------------------------

// parseOpenAIResponseToAnthropic 解析 OpenAI 格式响应并转换为 AnthropicResponse
// 用于 chatMessagesOpenAI 方法，使 Hub 层无需感知 provider 差异
func parseOpenAIResponseToAnthropic(raw json.RawMessage) (*AnthropicResponse, error) {
	// 尝试 APIResponse 包装
	var wrapper struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	data := []byte(raw)
	if json.Unmarshal(data, &wrapper) == nil && len(wrapper.Data) > 0 && string(wrapper.Data) != "null" {
		if wrapper.Code != 0 {
			return nil, &BusinessError{Code: wrapper.Code, Message: wrapper.Message}
		}
		data = wrapper.Data
	}

	var oaiResp OpenAIChatResponse
	if err := json.Unmarshal(data, &oaiResp); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}

	resp := &AnthropicResponse{
		ID:   oaiResp.ID,
		Type: "message",
		Role: "assistant",
		Model: oaiResp.Model,
		Usage: AnthropicUsage{
			InputTokens:  oaiResp.Usage.PromptTokens,
			OutputTokens: oaiResp.Usage.CompletionTokens,
		},
	}

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]

		switch choice.FinishReason {
		case "stop":
			resp.StopReason = "end_turn"
		case "tool_calls":
			resp.StopReason = "tool_use"
		case "length":
			resp.StopReason = "max_tokens"
		default:
			resp.StopReason = choice.FinishReason
		}

		// thinking → thinking block
		if choice.Message.ReasoningContent != "" {
			resp.Content = append(resp.Content, AnthropicContentBlock{
				Type:     "thinking",
				Thinking: choice.Message.ReasoningContent,
			})
		}

		// text → text block
		if choice.Message.Content != "" {
			resp.Content = append(resp.Content, AnthropicContentBlock{
				Type: "text",
				Text: choice.Message.Content,
			})
		}

		// tool_calls → tool_use blocks
		for _, tc := range choice.Message.ToolCalls {
			resp.Content = append(resp.Content, AnthropicContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	return resp, nil
}

// ---------------------------------------------------------------------------
// OpenAI SSE → Anthropic 事件转换器（供 chatMessagesStreamInternal 使用）
// ---------------------------------------------------------------------------

// openAIStreamConverter 将 OpenAI SSE chunks 转换为 Anthropic 兼容的 StreamEvent
// 有状态：跨 chunk 追踪 block 索引
type openAIStreamConverter struct {
	messageStarted  bool
	thinkingStarted bool
	thinkingStopped bool
	textStarted     bool
	toolBlockIndex  map[int]int // OpenAI tool_call index → Anthropic block index
	blockIndex      int
}

func newOpenAIStreamConverter() *openAIStreamConverter {
	return &openAIStreamConverter{
		toolBlockIndex: make(map[int]int),
	}
}

// Convert 将一行 OpenAI SSE data 转换为零或多个 Anthropic 格式 StreamEvent
// 返回 (events, done, error)
func (c *openAIStreamConverter) Convert(data string) ([]StreamEvent, bool, error) {
	if data == "[DONE]" {
		return nil, true, nil
	}

	var chunk OpenAIStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, false, fmt.Errorf("parse openai stream chunk: %w", err)
	}

	var events []StreamEvent

	if len(chunk.Choices) == 0 {
		return events, false, nil
	}

	choice := chunk.Choices[0]

	// 首个 chunk: 发送 message_start
	if !c.messageStarted {
		c.messageStarted = true
		msgJSON, _ := json.Marshal(map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":      chunk.ID,
				"type":    "message",
				"role":    "assistant",
				"content": []any{},
				"model":   "",
			},
		})
		events = append(events, StreamEvent{Event: "message_start", Data: string(msgJSON)})
	}

	// thinking delta (reasoning_content)
	if choice.Delta.ReasoningContent != "" {
		if !c.thinkingStarted {
			c.thinkingStarted = true
			blockJSON, _ := json.Marshal(map[string]any{
				"type":          "content_block_start",
				"index":         c.blockIndex,
				"content_block": map[string]any{"type": "thinking", "thinking": ""},
			})
			events = append(events, StreamEvent{Event: "content_block_start", Data: string(blockJSON)})
		}
		deltaJSON, _ := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": c.blockIndex,
			"delta": map[string]any{"type": "thinking_delta", "thinking": choice.Delta.ReasoningContent},
		})
		events = append(events, StreamEvent{Event: "content_block_delta", Data: string(deltaJSON)})
	}

	// text delta (content)
	if choice.Delta.Content != "" {
		// 关闭 thinking block（如果有）
		if c.thinkingStarted && !c.thinkingStopped {
			c.thinkingStopped = true
			stopJSON, _ := json.Marshal(map[string]any{
				"type": "content_block_stop", "index": c.blockIndex,
			})
			events = append(events, StreamEvent{Event: "content_block_stop", Data: string(stopJSON)})
			c.blockIndex++
		}
		if !c.textStarted {
			c.textStarted = true
			blockJSON, _ := json.Marshal(map[string]any{
				"type":          "content_block_start",
				"index":         c.blockIndex,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			events = append(events, StreamEvent{Event: "content_block_start", Data: string(blockJSON)})
		}
		deltaJSON, _ := json.Marshal(map[string]any{
			"type":  "content_block_delta",
			"index": c.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": choice.Delta.Content},
		})
		events = append(events, StreamEvent{Event: "content_block_delta", Data: string(deltaJSON)})
	}

	// tool_calls delta
	for _, tc := range choice.Delta.ToolCalls {
		if _, exists := c.toolBlockIndex[tc.Index]; !exists {
			// 关闭 text block（如果有）
			if c.textStarted {
				stopJSON, _ := json.Marshal(map[string]any{
					"type": "content_block_stop", "index": c.blockIndex,
				})
				events = append(events, StreamEvent{Event: "content_block_stop", Data: string(stopJSON)})
				c.blockIndex++
				c.textStarted = false
			}
			c.toolBlockIndex[tc.Index] = c.blockIndex
			blockJSON, _ := json.Marshal(map[string]any{
				"type":  "content_block_start",
				"index": c.blockIndex,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": map[string]any{},
				},
			})
			events = append(events, StreamEvent{Event: "content_block_start", Data: string(blockJSON)})
			c.blockIndex++ // 递增，为下一个 tool_call block 预留索引
		}
		if tc.Function.Arguments != "" {
			idx := c.toolBlockIndex[tc.Index]
			deltaJSON, _ := json.Marshal(map[string]any{
				"type":  "content_block_delta",
				"index": idx,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": tc.Function.Arguments,
				},
			})
			events = append(events, StreamEvent{Event: "content_block_delta", Data: string(deltaJSON)})
		}
	}

	// finish_reason: 关闭所有 block + message_delta + message_stop
	if choice.FinishReason != nil && *choice.FinishReason != "" {
		// 关闭可能仍打开的 block
		if c.textStarted {
			stopJSON, _ := json.Marshal(map[string]any{
				"type": "content_block_stop", "index": c.blockIndex,
			})
			events = append(events, StreamEvent{Event: "content_block_stop", Data: string(stopJSON)})
		} else if c.thinkingStarted && !c.thinkingStopped {
			stopJSON, _ := json.Marshal(map[string]any{
				"type": "content_block_stop", "index": c.blockIndex,
			})
			events = append(events, StreamEvent{Event: "content_block_stop", Data: string(stopJSON)})
		}
		// 关闭 tool blocks
		for _, idx := range c.toolBlockIndex {
			stopJSON, _ := json.Marshal(map[string]any{
				"type": "content_block_stop", "index": idx,
			})
			events = append(events, StreamEvent{Event: "content_block_stop", Data: string(stopJSON)})
		}

		// stop_reason 映射
		stopReason := "end_turn"
		switch *choice.FinishReason {
		case "tool_calls":
			stopReason = "tool_use"
		case "length":
			stopReason = "max_tokens"
		}

		deltaJSON, _ := json.Marshal(map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": stopReason},
		})
		events = append(events, StreamEvent{Event: "message_delta", Data: string(deltaJSON)})

		stopJSON, _ := json.Marshal(map[string]any{"type": "message_stop"})
		events = append(events, StreamEvent{Event: "message_stop", Data: string(stopJSON)})
	}

	return events, false, nil
}
