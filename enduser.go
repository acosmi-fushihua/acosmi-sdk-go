// enduser.go — v1.6.0 业务侧终端用户 id (EndUserID) 公共工具
//
// 网关 sanitizer 仍会做权限校验 + 派生兜底; 此处仅负责 SDK 侧的基本约束:
//   - 正则: [a-zA-Z0-9_-]+ (与上游官方文档一致, 不含 ":" / "." 等隐式破坏字符)
//   - 长度: ≤ 512
//   - 禁止隐私信息: SDK 无从机械判定, 仅在 guide.md 提示, 不在代码做拦截
//
// 该 helper 暴露 ValidateEndUserID 公开 API, 方便 caller 在赋值前自校验,
// 避免请求被网关 sanitizer 静默丢弃。

package acosmi

import (
	"fmt"
	"strings"
)

// maxEndUserIDLength 与上游 DeepSeek 官方文档对齐。
const maxEndUserIDLength = 512

// ValidateEndUserID 检查 EndUserID 是否符合规范 (字符集 + 长度)。
// 空串返回 nil (合法: 表示未设置)。
//
// 网关侧 sanitizer 在收到非空但校验失败的值时会 drop 并 metric, 不阻塞请求;
// 但 caller 在自有业务侧拿到来源 (如 DB 内部 id) 后, 推荐先调用本函数明确语义。
func ValidateEndUserID(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > maxEndUserIDLength {
		return fmt.Errorf("acosmi: EndUserID length %d > %d", len(s), maxEndUserIDLength)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '-'
		if !ok {
			return fmt.Errorf("acosmi: EndUserID contains invalid byte 0x%02x at offset %d (allowed: [a-zA-Z0-9_-])", c, i)
		}
	}
	return nil
}

// isSSECommentLine 识别 SSE 协议中的注释行 (":<text>"), 如 ": keep-alive"。
//
// 上游 (如 DeepSeek) 在开始推理前可能持续发送 SSE 注释作为保活;
// 注释行不构成事件, SDK 解析器必须显式跳过, 否则未来若 else-branch 把
// 未匹配行误送 JSON.Unmarshal 会回归出 parse error。
//
// 严格定义: 行的首个非空字节是 ":" 且不是 "::"。这里不放宽 (SSE 规范不允许行首空白)。
func isSSECommentLine(line string) bool {
	return strings.HasPrefix(line, ":")
}
