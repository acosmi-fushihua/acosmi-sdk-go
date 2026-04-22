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
