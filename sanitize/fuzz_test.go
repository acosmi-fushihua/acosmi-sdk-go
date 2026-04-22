package sanitize

import (
	"encoding/json"
	"testing"
)

// FuzzSanitize 对 DefensiveSanitize / StripEphemeral 喂任意 JSON 数组,
// 确保永不 panic。任何 error 都可接受 —— 只要不崩溃。
func FuzzSanitize(f *testing.F) {
	// 种子: 真实形态样本
	seeds := []string{
		`[]`,
		`[{"role":"user","content":[{"type":"text","text":"hi"}]}]`,
		`[{"role":"assistant","content":[{"type":"thinking","thinking":"x","acosmi_ephemeral":true}]}]`,
		`[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]`,
		`[{"role":"assistant","content":[{"type":"server_tool_use","id":"s1","acosmi_ephemeral":true}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","content":"r"}]}]`,
		`[null, 42, "string", {"role":"user"}]`,
		`[{"role":"user","content":"plain"}]`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		var msgs []any
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return // 非法 JSON, 不测
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v\ninput: %s", r, raw)
			}
		}()

		cfg := MinimalSanitizeConfig{
			MaxImageBytes:       1 << 20,
			MaxVideoBytes:       10 << 20,
			MaxPDFBytes:         5 << 20,
			MaxMessagesTurns:    100,
			PermanentDenyBlocks: []BlockType{BlockContainerUpload},
		}
		_, _ = Sanitize(msgs, cfg)
		_ = StripEphemeral(msgs)
	})
}
