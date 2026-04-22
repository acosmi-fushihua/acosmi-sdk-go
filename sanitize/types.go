// Package sanitize 提供与 provider 无关的请求/响应 content-block 防御性处理。
//
// 职责边界:
//   - SDK 层: 只做所有下游 provider 都不可能接受的底线剥除 + 早失败
//   - Gateway 层: 按 provider preset 精细剥除 (不在本包内)
//
// 本包不依赖 acosmi 主包, 避免循环依赖; 主包 acosmi.DefensiveSanitize /
// acosmi.StripEphemeralHistory 负责与 ChatRequest 的胶水。
package sanitize

// BlockType 枚举 Anthropic content block 类型 (请求 + 响应 + ephemeral)。
type BlockType string

const (
	BlockText                    BlockType = "text"
	BlockImage                   BlockType = "image"
	BlockVideo                   BlockType = "video"
	BlockDocument                BlockType = "document"
	BlockSearchResult            BlockType = "search_result"
	BlockThinking                BlockType = "thinking"
	BlockRedactedThinking        BlockType = "redacted_thinking"
	BlockToolUse                 BlockType = "tool_use"
	BlockToolResult              BlockType = "tool_result"
	BlockToolReference           BlockType = "tool_reference"
	BlockServerToolUse           BlockType = "server_tool_use"
	BlockWebSearchToolResult     BlockType = "web_search_tool_result"
	BlockCodeExecutionToolResult BlockType = "code_execution_tool_result"
	BlockMCPToolUse              BlockType = "mcp_tool_use"
	BlockMCPToolResult           BlockType = "mcp_tool_result"
	BlockContainerUpload         BlockType = "container_upload"
)

// DeltaType 流式响应 delta 类型 (与 BlockType 正交)。
type DeltaType string

const (
	DeltaText      DeltaType = "text_delta"
	DeltaInputJSON DeltaType = "input_json_delta" // tool_use 参数流式
	DeltaThinking  DeltaType = "thinking_delta"
	DeltaSignature DeltaType = "signature_delta" // thinking 签名
	DeltaCitations DeltaType = "citations_delta"
)

// EphemeralMarkerField 是网关在 block JSON 中注入的 in-band 标记字段名。
// 消费者与 SDK 都用此常量识别; 存在且值为 true 时代表该 block 不应回传下一轮。
//
// 选择 in-band 标记而非独立 SSE 事件 (如 event: acosmi_meta) 的理由:
//   - 零缓冲: 解析 content_block_start JSON 时顺手读出, 无需暂存等待关联事件
//   - 零顺序依赖: 不依赖 "meta 事件先于 block 到达" 等脆弱时序
//   - 零延迟: 标记随 block 同步流出
//   - history 剥离天然可做: 消费者 history 中的 block 自带标记, 无需 SDK 另外记忆
const EphemeralMarkerField = "acosmi_ephemeral"
