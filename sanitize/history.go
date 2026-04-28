package sanitize

// BlockPredicate 判定一个 block 是否应被剥除。
type BlockPredicate func(block map[string]any) bool

// DropBlocks 按 pred 从 messages 中剥除 block, 并联动剥除
// 对应的 tool_result (user 轮) —— 若被剥的 block 是 tool_use / server_tool_use /
// mcp_tool_use, 收集其 id, 然后扫所有 user 轮的 tool_result, 凡 tool_use_id
// 匹配任一收集到的 id 则一并剥除。
//
// 此函数不修改入参 messages 中任何 map 的 key, 只重构切片。
// 产出新 messages 切片; 各 message 的 content 若被修改, 则重新构建 []any;
// 未被修改的 message 保持原引用 (零拷贝优化)。
//
// 收敛两步: 先扫收集 droppedToolUseIDs, 再整体剥; 这样顺序不影响正确性
// (即使 tool_result 在 tool_use 之前出现也能捕获)。
func DropBlocks(messages []any, pred BlockPredicate) []any {
	droppedToolUseIDs := collectDroppedToolUseIDs(messages, pred)

	// 第二步: 剥除符合 pred 的 block + 联动剥 tool_result。
	out := make([]any, 0, len(messages))
	for _, msg := range messages {
		m, ok := msg.(map[string]any)
		if !ok {
			out = append(out, msg)
			continue
		}
		content, ok := m["content"].([]any)
		if !ok {
			out = append(out, msg)
			continue
		}

		kept, changed := filterBlocks(content, pred, droppedToolUseIDs)
		if !changed {
			out = append(out, msg)
			continue
		}

		// content 空了的消息整条丢弃 (assistant 本轮全是 ephemeral 块;
		// 或 user 轮全是联动剥的 tool_result)。避免产生空消息让 provider 报错。
		if len(kept) == 0 {
			continue
		}

		// 浅拷贝 msg map, 只改 content, 避免污染调用方数据。
		newMsg := make(map[string]any, len(m))
		for k, v := range m {
			newMsg[k] = v
		}
		newMsg["content"] = kept
		out = append(out, newMsg)
	}
	return out
}

// collectDroppedToolUseIDs 第一遍扫描, 仅为了收集 "本次被 pred 命中的
// tool_use 类 block 的 id", 以便联动剥 tool_result。
func collectDroppedToolUseIDs(messages []any, pred BlockPredicate) map[string]bool {
	ids := map[string]bool{}
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
			if !pred(block) {
				continue
			}
			t, _ := block["type"].(string)
			switch BlockType(t) {
			case BlockToolUse, BlockServerToolUse, BlockMCPToolUse:
				if id, _ := block["id"].(string); id != "" {
					ids[id] = true
				}
			}
		}
	}
	return ids
}

// filterBlocks 对单条消息的 content 数组剥除 pred 命中的 block, 以及
// tool_use_id 在 droppedToolUseIDs 中的 tool_result。返回 (新数组, 是否变更)。
func filterBlocks(content []any, pred BlockPredicate, droppedToolUseIDs map[string]bool) ([]any, bool) {
	kept := make([]any, 0, len(content))
	changed := false
	for _, raw := range content {
		block, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		if pred(block) {
			changed = true
			continue
		}
		// 联动剥 tool_result / mcp_tool_result (仅当 tool_use_id 命中)。
		if len(droppedToolUseIDs) > 0 {
			t, _ := block["type"].(string)
			switch BlockType(t) {
			case BlockToolResult, BlockMCPToolResult:
				if id, _ := block["tool_use_id"].(string); id != "" && droppedToolUseIDs[id] {
					changed = true
					continue
				}
			}
		}
		kept = append(kept, raw)
	}
	return kept, changed
}

// StripEphemeral 从 messages 中剥除带 acosmi_ephemeral:true 标记的 block,
// 以及对应的 tool_result。这是 DropBlocks 的 in-band 特化版。
//
// 硬豁免: thinking / redacted_thinking 块永不剥, 即使携带 ephemeral 标记。
// 理由: Anthropic extended thinking + tool_use 续轮场景下, 上游强制要求
// assistant 历史中保留原始 thinking 块, 否则返回:
//
//	"The content[].thinking in the thinking mode must be passed back to the API."
//
// 历史污染防御: 即使旧版网关 / 历史响应里 thinking 块带了 ephemeral=true,
// 客户端在调用 StripEphemeral 时也不应剥除, 避免下一轮发出残缺请求被上游拒。
// 网关侧 anthropic preset 已停止注入此标记 (commit 55fe8090), 本豁免兜底
// 历史会话与第三方调用方两类已污染场景。
func StripEphemeral(messages []any) []any {
	return DropBlocks(messages, func(b map[string]any) bool {
		t, _ := b["type"].(string)
		switch BlockType(t) {
		case BlockThinking, BlockRedactedThinking:
			return false // 硬豁免, 不可被 ephemeral 标记覆盖
		}
		v, ok := b[EphemeralMarkerField].(bool)
		return ok && v
	})
}
