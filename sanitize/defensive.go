package sanitize

import (
	"encoding/base64"
	"strings"
)

// Sanitize 对已经解析为 []any 的 messages 做底线防御: 深度校验 +
// 体积校验 + deny-list 剥除 + tool_use_id 联动剥除 tool_result。
//
// 入参形态: messages 的每个元素预期是 map[string]any (或 JSON 解码后的
// 等价结构), 含 role / content; content 为 string (plain text) 或
// []any (block 数组)。形态异常的元素原样透传, 不 panic。
//
// 返回 (messages, nil) 表示通过 (可能 block 数组被缩减); 返回
// (nil, err) 表示早失败, 调用方应放弃本次请求。
func Sanitize(messages []any, cfg MinimalSanitizeConfig) ([]any, error) {
	if cfg.MaxMessagesTurns > 0 && len(messages) > cfg.MaxMessagesTurns {
		return nil, ErrHistoryTooDeep
	}

	// 体积校验: 先扫一遍, 任何违规直接早失败 (不修改 messages)。
	if cfg.MaxImageBytes > 0 || cfg.MaxVideoBytes > 0 || cfg.MaxPDFBytes > 0 {
		if err := checkMediaSizes(messages, cfg); err != nil {
			return nil, err
		}
	}

	// deny-list 剥除 + tool_use_id 联动。
	if len(cfg.PermanentDenyBlocks) > 0 {
		denySet := make(map[string]bool, len(cfg.PermanentDenyBlocks))
		for _, bt := range cfg.PermanentDenyBlocks {
			denySet[string(bt)] = true
		}
		messages = DropBlocks(messages, func(b map[string]any) bool {
			t, _ := b["type"].(string)
			return denySet[t]
		})
	}

	return messages, nil
}

// checkMediaSizes 遍历所有 block, 对 base64 内联 image/video/document
// 类型校验解码后字节数。URL 版无法本地量体积, 跳过 (交网关把关)。
func checkMediaSizes(messages []any, cfg MinimalSanitizeConfig) error {
	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := m["content"].([]any)
		if !ok {
			continue
		}
		for _, raw := range content {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			bt, _ := block["type"].(string)

			var limit int64
			switch BlockType(bt) {
			case BlockImage:
				limit = cfg.MaxImageBytes
			case BlockVideo:
				limit = cfg.MaxVideoBytes
			case BlockDocument:
				limit = cfg.MaxPDFBytes
			default:
				continue
			}
			if limit <= 0 {
				continue
			}

			data := extractBase64Data(block)
			if data == "" {
				continue // URL 版或形态异常, 跳过
			}
			actual := int64(base64.StdEncoding.DecodedLen(len(data)))
			if actual > limit {
				return &SizeError{
					BlockType: BlockType(bt),
					Actual:    actual,
					Limit:     limit,
				}
			}
		}
	}
	return nil
}

// extractBase64Data 从 Anthropic block 结构中抽 base64 data 字段。
//
// 形态:
//   image:     {source:{type:"base64", data:"..."}}
//   video:     {source:{type:"base64", data:"..."}}
//   document:  {source:{type:"base64", data:"..."}}
//
// 若是 URL 版 (source.type="url") 或缺字段, 返回 ""。
func extractBase64Data(block map[string]any) string {
	src, ok := block["source"].(map[string]any)
	if !ok {
		return ""
	}
	srcType, _ := src["type"].(string)
	if srcType != "base64" {
		return ""
	}
	data, _ := src["data"].(string)
	// 防御性: 某些上游把 "data:image/jpeg;base64,..." 整串塞进 data,
	// 虽非标准但兜底去掉前缀, 保证字节数估算正确。
	if i := strings.Index(data, "base64,"); i >= 0 {
		data = data[i+len("base64,"):]
	}
	return data
}
