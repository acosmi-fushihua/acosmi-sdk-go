package acosmi

import "testing"

// ---------- S-4: BlockIndex / BlockType 映射 ----------

func TestExtractAnthropicBlockMeta_StartDeltaStop(t *testing.T) {
	m := map[int]blockMeta{}

	// content_block_start 携带 type, 应入 map
	startData := `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"x"}}`
	idx, bt, eph := extractAnthropicBlockMeta("content_block_start", startData, m)
	if idx != 1 || bt != "tool_use" || eph {
		t.Errorf("start: got (%d,%q,%v), want (1,tool_use,false)", idx, bt, eph)
	}
	if m[1].Type != "tool_use" {
		t.Errorf("blockTypeMap[1].Type = %q, want tool_use", m[1].Type)
	}

	// content_block_delta 查 map 得到 type
	deltaData := `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`
	idx, bt, eph = extractAnthropicBlockMeta("content_block_delta", deltaData, m)
	if idx != 1 || bt != "tool_use" || eph {
		t.Errorf("delta: got (%d,%q,%v), want (1,tool_use,false)", idx, bt, eph)
	}

	// content_block_stop 查表后删除
	stopData := `{"type":"content_block_stop","index":1}`
	idx, bt, _ = extractAnthropicBlockMeta("content_block_stop", stopData, m)
	if idx != 1 || bt != "tool_use" {
		t.Errorf("stop: got (%d,%q), want (1,tool_use)", idx, bt)
	}
	if _, exists := m[1]; exists {
		t.Errorf("map should have deleted index 1 after stop")
	}
}

// ---------- S-5: in-band ephemeral 标记透传 ----------

func TestExtractAnthropicBlockMeta_EphemeralInBand(t *testing.T) {
	m := map[int]blockMeta{}

	// 网关 in-band 注入 acosmi_ephemeral:true
	startData := `{"type":"content_block_start","index":2,"content_block":{"type":"thinking","acosmi_ephemeral":true}}`
	idx, bt, eph := extractAnthropicBlockMeta("content_block_start", startData, m)
	if idx != 2 || bt != "thinking" || !eph {
		t.Errorf("start w/ ephemeral: got (%d,%q,%v), want (2,thinking,true)", idx, bt, eph)
	}

	// delta 应继承 ephemeral 标记 (SDK 不该只在 start 上吐)
	deltaData := `{"type":"content_block_delta","index":2,"delta":{"type":"thinking_delta","thinking":"..."}}`
	idx, bt, eph = extractAnthropicBlockMeta("content_block_delta", deltaData, m)
	if !eph {
		t.Errorf("delta should inherit ephemeral=true from start, got %v", eph)
	}

	stopData := `{"type":"content_block_stop","index":2}`
	_, _, eph = extractAnthropicBlockMeta("content_block_stop", stopData, m)
	if !eph {
		t.Errorf("stop should inherit ephemeral=true from start, got %v", eph)
	}
}

// ---------- 非 content_block 事件: 返回空 type, 不污染 StreamEvent ----------

func TestExtractAnthropicBlockMeta_NonBlockEvents(t *testing.T) {
	m := map[int]blockMeta{}

	cases := []struct {
		event, data string
	}{
		{"message_start", `{"type":"message_start","message":{"id":"m_1"}}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`},
		{"message_stop", `{"type":"message_stop"}`},
		{"ping", `{"type":"ping"}`},
		{"", ``},
	}
	for _, tc := range cases {
		idx, bt, eph := extractAnthropicBlockMeta(tc.event, tc.data, m)
		if bt != "" {
			t.Errorf("event %q: expected empty BlockType, got (%d,%q,%v)", tc.event, idx, bt, eph)
		}
	}
	if len(m) != 0 {
		t.Errorf("non-block events should not touch map, got %d entries", len(m))
	}
}

// ---------- 解析错误: 不 panic, 返回空 type ----------

func TestExtractAnthropicBlockMeta_MalformedJSON(t *testing.T) {
	m := map[int]blockMeta{}
	idx, bt, eph := extractAnthropicBlockMeta("content_block_start", `not json`, m)
	if bt != "" {
		t.Errorf("malformed JSON should return empty type, got (%d,%q,%v)", idx, bt, eph)
	}
}

// ---------- 无 start 只有 delta: 防御性返回空 type (map 查不到) ----------

func TestExtractAnthropicBlockMeta_DeltaWithoutStart(t *testing.T) {
	m := map[int]blockMeta{}
	deltaData := `{"type":"content_block_delta","index":99,"delta":{"type":"text_delta","text":"x"}}`
	idx, bt, _ := extractAnthropicBlockMeta("content_block_delta", deltaData, m)
	if idx != 99 {
		t.Errorf("index should still be extracted: got %d, want 99", idx)
	}
	if bt != "" {
		t.Errorf("unknown index should yield empty BlockType, got %q", bt)
	}
}
