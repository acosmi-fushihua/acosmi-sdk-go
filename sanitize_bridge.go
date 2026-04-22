// sanitize_bridge.go — 主包与 sanitize 子包的胶水层
//
// sanitize 子包定义了与 provider 无关的 block 处理工具, 不依赖 acosmi 主包;
// 本文件把 ChatRequest 这种主包类型归一化为 sanitize 能处理的 []any 形态,
// 并提供 Client 级别的可配置钩子 (SetDefensiveSanitize / SetAutoStripEphemeralHistory)。
//
// 调用时机: buildChatRequest 开头 (每次 Chat/ChatStream/ChatMessages*)。
// 未配置时零开销 (applyRequestSanitizers 首行 early-return)。

package acosmi

import (
	"encoding/json"
	"fmt"

	"github.com/acosmi/acosmi-sdk-go/sanitize"
)

// SetDefensiveSanitize 配置请求前的底线防御 (体积 / deny-list / 深度)。
// 传空值 MinimalSanitizeConfig{} 关闭 (所有字段为零 = 禁用)。
// 并发安全, 可在任意时间调用。
func (c *Client) SetDefensiveSanitize(cfg sanitize.MinimalSanitizeConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defensiveCfg = &cfg
}

// SetAutoStripEphemeralHistory 开启后, 每次请求前 SDK 会从 RawMessages
// 中剥除带 acosmi_ephemeral:true 标记的 block, 并联动剥除引用已剥
// tool_use 的 tool_result, 避免 provider 报 "tool_use_id 不存在"。
//
// 标记来源: 网关在响应 content_block_start 中 in-band 注入; 消费者
// 把上一轮 assistant 回复原样拼回 history 即可触发剥离。
func (c *Client) SetAutoStripEphemeralHistory(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.autoStripEphemeral = on
}

// applyRequestSanitizers 在 buildChatRequest 开头调用, 把配置化的
// 防御策略应用到 req。未配置时立即返回 nil, 零开销。
func (c *Client) applyRequestSanitizers(req *ChatRequest) error {
	c.mu.RLock()
	cfg := c.defensiveCfg
	strip := c.autoStripEphemeral
	c.mu.RUnlock()

	if cfg == nil && !strip {
		return nil
	}

	// RawMessages 分支: 归一化 → sanitize → (可选) strip → 写回。
	if req.RawMessages != nil {
		msgs, err := normalizeRawMessages(req.RawMessages)
		if err != nil {
			return fmt.Errorf("sanitize: normalize raw messages: %w", err)
		}
		if cfg != nil {
			msgs, err = sanitize.Sanitize(msgs, *cfg)
			if err != nil {
				return err
			}
		}
		if strip {
			msgs = sanitize.StripEphemeral(msgs)
		}
		req.RawMessages = msgs
		return nil
	}

	// 纯 Messages 分支: block 级操作不适用, 只做深度校验。
	if cfg != nil && cfg.MaxMessagesTurns > 0 && len(req.Messages) > cfg.MaxMessagesTurns {
		return sanitize.ErrHistoryTooDeep
	}
	return nil
}

// normalizeRawMessages 把任意形态的 RawMessages (struct 切片 /
// map 切片 / []any / json.RawMessage) 归一为 []any, 供 sanitize 包处理。
//
// 已是 []any 时零拷贝直接返回; 其他走一次 json roundtrip。
// roundtrip 会丢失具体 Go 类型, 但 JSON 序列化结果等价, 不影响
// 最终发到网关的 body; 还顺带把 json.RawMessage 之类展开成结构化形式。
func normalizeRawMessages(rm any) ([]any, error) {
	if s, ok := rm.([]any); ok {
		return s, nil
	}
	b, err := json.Marshal(rm)
	if err != nil {
		return nil, err
	}
	var s []any
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("raw messages must be a JSON array: %w", err)
	}
	return s, nil
}
