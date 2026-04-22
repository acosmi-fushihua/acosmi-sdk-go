package acosmi

import (
	"reflect"
	"testing"
)

// ---------- v0.13.0: reasoning_effort 翻译 ----------

func TestOpenAIAdapter_ReasoningEffort_FromEffort(t *testing.T) {
	cases := []struct {
		name  string
		level string
		want  string
	}{
		{"low", "low", "low"},
		{"medium", "medium", "medium"},
		{"high", "high", "high"},
		{"max → high", "max", "high"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &ChatRequest{
				Messages: []ChatMessage{{Role: "user", Content: "hi"}},
				Effort:   &EffortConfig{Level: tc.level},
			}
			body, err := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
			if err != nil {
				t.Fatal(err)
			}
			if got := body["reasoning_effort"]; got != tc.want {
				t.Errorf("reasoning_effort = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestOpenAIAdapter_ReasoningEffort_FromThinkingLevel(t *testing.T) {
	cases := []struct {
		level string
		want  string
	}{
		{ThinkingHigh, "high"},
		{ThinkingMax, "high"},
		{ThinkingOff, ""},
		{"", ""}, // 零值 level
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			req := &ChatRequest{
				Messages: []ChatMessage{{Role: "user", Content: "hi"}},
				Thinking: &ThinkingConfig{Type: "adaptive", Level: tc.level},
			}
			body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
			if tc.want == "" {
				if _, ok := body["reasoning_effort"]; ok {
					t.Errorf("expected no reasoning_effort, got %v", body["reasoning_effort"])
				}
			} else if got := body["reasoning_effort"]; got != tc.want {
				t.Errorf("reasoning_effort = %v, want %q", got, tc.want)
			}
		})
	}
}

// Effort 优先级高于 Thinking (同时设置时取 Effort)
func TestOpenAIAdapter_ReasoningEffort_EffortOverridesThinking(t *testing.T) {
	req := &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Effort:   &EffortConfig{Level: "low"},
		Thinking: &ThinkingConfig{Type: "adaptive", Level: ThinkingMax},
	}
	body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	if body["reasoning_effort"] != "low" {
		t.Errorf("effort should override thinking, got %v", body["reasoning_effort"])
	}
}

// ---------- v0.13.0: response_format 翻译 ----------

func TestOpenAIAdapter_ResponseFormat_JSONSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	req := &ChatRequest{
		Messages:     []ChatMessage{{Role: "user", Content: "hi"}},
		OutputConfig: &OutputConfig{Format: "json_schema", Schema: schema},
	}
	body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %T", body["response_format"])
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format.type = %v, want json_schema", rf["type"])
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema missing: %T", rf["json_schema"])
	}
	if !reflect.DeepEqual(js["schema"], schema) {
		t.Errorf("schema not propagated: got %v", js["schema"])
	}
	if js["strict"] != true {
		t.Errorf("strict should default to true, got %v", js["strict"])
	}
}

func TestOpenAIAdapter_ResponseFormat_JSONObject(t *testing.T) {
	req := &ChatRequest{
		Messages:     []ChatMessage{{Role: "user", Content: "hi"}},
		OutputConfig: &OutputConfig{Format: "json_object"},
	}
	body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing")
	}
	if rf["type"] != "json_object" {
		t.Errorf("response_format.type = %v, want json_object", rf["type"])
	}
}

func TestOpenAIAdapter_ResponseFormat_NilWhenNoOutputConfig(t *testing.T) {
	req := &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
	body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	if _, ok := body["response_format"]; ok {
		t.Errorf("response_format should not be set when OutputConfig nil")
	}
	// 遗留 output_config 字段也不应再出现 (v0.13 语义已改)
	if _, ok := body["output_config"]; ok {
		t.Errorf("output_config should not be emitted (v0.13 replaces with response_format)")
	}
}

// ---------- v0.13.0: parallel_tool_calls 字段 ----------

func TestOpenAIAdapter_ParallelToolCalls(t *testing.T) {
	tTrue, tFalse := true, false
	cases := []struct {
		name string
		ptc  *bool
		want any // nil = key absent
	}{
		{"nil → absent", nil, nil},
		{"true → true", &tTrue, true},
		{"false → false", &tFalse, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &ChatRequest{
				Messages:          []ChatMessage{{Role: "user", Content: "hi"}},
				ParallelToolCalls: tc.ptc,
			}
			body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
			got, ok := body["parallel_tool_calls"]
			if tc.want == nil {
				if ok {
					t.Errorf("expected absent, got %v", got)
				}
			} else {
				if got != tc.want {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// AnthropicAdapter 应忽略 ParallelToolCalls (该字段是 OpenAI 专属)
func TestAnthropicAdapter_IgnoresParallelToolCalls(t *testing.T) {
	tTrue := true
	req := &ChatRequest{
		Messages:          []ChatMessage{{Role: "user", Content: "hi"}},
		ParallelToolCalls: &tTrue,
	}
	body, _ := (&AnthropicAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	if _, ok := body["parallel_tool_calls"]; ok {
		t.Errorf("AnthropicAdapter should not emit parallel_tool_calls")
	}
}

// ---------- S-14: ExtraBody 透传 (两 adapter 均覆盖) ----------

func TestExtraBodyPassthrough_OpenAI(t *testing.T) {
	req := &ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		ExtraBody: map[string]any{
			"custom_key":  "custom_value",
			"nested":      map[string]any{"a": 1},
			"numeric":     42,
		},
	}
	body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	if body["custom_key"] != "custom_value" {
		t.Errorf("custom_key lost: %v", body["custom_key"])
	}
	nested, ok := body["nested"].(map[string]any)
	if !ok || nested["a"] != 1 {
		t.Errorf("nested ExtraBody lost: %v", body["nested"])
	}
	if body["numeric"] != 42 {
		t.Errorf("numeric ExtraBody lost: %v", body["numeric"])
	}
}

func TestExtraBodyPassthrough_Anthropic(t *testing.T) {
	req := &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		ExtraBody: map[string]any{"some_extension": "value"},
	}
	body, _ := (&AnthropicAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	if body["some_extension"] != "value" {
		t.Errorf("ExtraBody not propagated: %v", body)
	}
}

// ExtraBody key 与 SDK 显式字段同名时, ExtraBody 应覆盖 (当前语义)
func TestExtraBodyPassthrough_ShadowsSdkField(t *testing.T) {
	req := &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
		ExtraBody: map[string]any{"max_tokens": 999},
	}
	body, _ := (&OpenAIAdapter{}).BuildRequestBody(ModelCapabilities{}, req)
	// ExtraBody 在末尾写入, 覆盖 SDK 字段 —— 调用方自负其责
	if body["max_tokens"] != 999 {
		t.Errorf("ExtraBody should shadow SDK field, got %v", body["max_tokens"])
	}
}
