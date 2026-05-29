package acosmi

import "testing"

func TestGetAdapterForModel_PreferredFormat(t *testing.T) {
	cases := []struct {
		name   string
		model  ManagedModel
		want   ProviderFormat
	}{
		{
			name:  "preferred anthropic wins over provider hardcode",
			model: ManagedModel{Provider: "dashscope", PreferredFormat: "anthropic"},
			want:  FormatAnthropic,
		},
		{
			name:  "preferred openai wins even for anthropic provider",
			model: ManagedModel{Provider: "anthropic", PreferredFormat: "openai"},
			want:  FormatOpenAI,
		},
		{
			name:  "preferred is case-insensitive",
			model: ManagedModel{Provider: "dashscope", PreferredFormat: "Anthropic"},
			want:  FormatAnthropic,
		},
		{
			name:  "supported list picks anthropic when present",
			model: ManagedModel{Provider: "dashscope", SupportedFormats: []string{"openai", "anthropic"}},
			want:  FormatAnthropic,
		},
		{
			name:  "supported list with only openai",
			model: ManagedModel{Provider: "anthropic", SupportedFormats: []string{"openai"}},
			want:  FormatOpenAI,
		},
		{
			name:  "fallback to provider hardcode when fields empty — anthropic",
			model: ManagedModel{Provider: "anthropic"},
			want:  FormatAnthropic,
		},
		{
			name:  "fallback to provider hardcode when fields empty — acosmi",
			model: ManagedModel{Provider: "acosmi"},
			want:  FormatAnthropic,
		},
		{
			name:  "fallback to provider hardcode when fields empty — dashscope → openai (legacy)",
			model: ManagedModel{Provider: "dashscope"},
			want:  FormatOpenAI,
		},
		{
			// 护栏: preferred=anthropic 与 supported_formats 矛盾 → 不路由到不支持的格式
			name:  "preferred anthropic ignored when supported_formats lacks it",
			model: ManagedModel{Provider: "dashscope", PreferredFormat: "anthropic", SupportedFormats: []string{"openai"}},
			want:  FormatOpenAI,
		},
		{
			name:  "preferred openai ignored when supported_formats lacks it",
			model: ManagedModel{Provider: "dashscope", PreferredFormat: "openai", SupportedFormats: []string{"anthropic"}},
			want:  FormatAnthropic,
		},
		{
			// preferred 在 supported 内 → 仍采信 (与 supported 顺序优先级无关)
			name:  "preferred openai honored when in supported_formats",
			model: ManagedModel{Provider: "dashscope", PreferredFormat: "openai", SupportedFormats: []string{"anthropic", "openai"}},
			want:  FormatOpenAI,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := getAdapterForModel(tc.model).Format()
			if got != tc.want {
				t.Fatalf("Format() = %v, want %v", got, tc.want)
			}
		})
	}
}
