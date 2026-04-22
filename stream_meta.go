// stream_meta.go — Anthropic SSE 事件的 content block 元数据解析
//
// 把 content_block_start 中的 index / type / acosmi_ephemeral 记入
// blockTypeMap, 供 delta/stop 查表回填 StreamEvent 的 BlockIndex / BlockType /
// Ephemeral 三字段。
//
// 设计要点:
//   - in-band 标记: ephemeral 随 content_block_start 的 JSON payload 到达,
//     无需独立 SSE 事件, 零额外缓冲, 零顺序依赖, 零延迟。
//   - 惰性解析: 只对 3 种 content_block_* 事件解 JSON, 其他事件 (message_start /
//     message_delta / message_stop / ping / error 等) 跳过, 不影响吞吐。
//   - 单流 map: map 由单 goroutine 拥有 (SSE 扫描循环), 无锁。stop 后删表项,
//     防长流累积。

package acosmi

import "encoding/json"

// blockMeta 是 SDK 为单个 content block 缓存的元数据。
type blockMeta struct {
	Type      string
	Ephemeral bool
}

// extractAnthropicBlockMeta 按 Anthropic SSE 事件类型解析 data, 更新
// blockTypeMap, 返回 (index, type, ephemeral)。
//
// 仅当事件为 content_block_start / content_block_delta / content_block_stop
// 时返回非零 type; 其他事件返回空 type, 调用方据此判别是否填充
// StreamEvent 元数据字段。
//
// start: 从 data.content_block.type / data.content_block.acosmi_ephemeral
//        读出, 写入 map; 返回该 block 的 meta。
// delta: 从 data.index 读出, 查 map 获取 type/ephemeral 传播给下游。
// stop:  同 delta, 但查表后删除该项 (释放内存)。
func extractAnthropicBlockMeta(eventType, data string, blockTypeMap map[int]blockMeta) (int, string, bool) {
	switch eventType {
	case "content_block_start":
		var payload struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type            string `json:"type"`
				AcosmiEphemeral bool   `json:"acosmi_ephemeral"`
			} `json:"content_block"`
		}
		if json.Unmarshal([]byte(data), &payload) != nil {
			return 0, "", false
		}
		meta := blockMeta{
			Type:      payload.ContentBlock.Type,
			Ephemeral: payload.ContentBlock.AcosmiEphemeral,
		}
		blockTypeMap[payload.Index] = meta
		return payload.Index, meta.Type, meta.Ephemeral
	case "content_block_delta":
		var payload struct {
			Index int `json:"index"`
		}
		if json.Unmarshal([]byte(data), &payload) != nil {
			return 0, "", false
		}
		meta := blockTypeMap[payload.Index]
		return payload.Index, meta.Type, meta.Ephemeral
	case "content_block_stop":
		var payload struct {
			Index int `json:"index"`
		}
		if json.Unmarshal([]byte(data), &payload) != nil {
			return 0, "", false
		}
		meta := blockTypeMap[payload.Index]
		delete(blockTypeMap, payload.Index)
		return payload.Index, meta.Type, meta.Ephemeral
	default:
		return 0, "", false
	}
}
