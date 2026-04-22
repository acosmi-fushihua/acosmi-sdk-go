package sanitize

import "errors"

// MinimalSanitizeConfig 为 SDK 层防御性配置。只做所有下游 provider
// 都不可能接受的底线剥除; 具体 provider 适配由网关承担。
//
// 零值 = 不校验 / 不剥除, 等价于未启用。
type MinimalSanitizeConfig struct {
	// 体积上限 (字节, 仅对 base64 内联媒体生效; URL 版交网关把关)。
	// 0 = 不校验。超限直接返回 *SizeError, 不上传。
	MaxImageBytes int64
	MaxVideoBytes int64
	MaxPDFBytes   int64

	// MaxMessagesTurns 为 history 轮次硬上限 (防止内存爆炸 / 上行带宽)。
	// 0 = 不校验。超限返回 ErrHistoryTooDeep。
	MaxMessagesTurns int

	// PermanentDenyBlocks 为公共黑名单 (所有 provider 均拒绝的 block 类型)。
	// 默认空; 只有发现某类型全网 provider 都不认才在这里加 (例如 container_upload)。
	PermanentDenyBlocks []BlockType
}

// 错误类型
var (
	ErrHistoryTooDeep = errors.New("sanitize: messages history exceeds configured depth")
	ErrBlockDenied    = errors.New("sanitize: block type permanently denied")
)

// SizeError 为体积超限错误, 携带实际/上限字节数以便上游展示。
type SizeError struct {
	BlockType BlockType
	Actual    int64
	Limit     int64
}

func (e *SizeError) Error() string {
	return "sanitize: " + string(e.BlockType) + " base64 size " +
		itoa(e.Actual) + " exceeds limit " + itoa(e.Limit)
}

// itoa 内联实现, 避免引入 strconv 仅为错误字符串 (减少依赖面)。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
