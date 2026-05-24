package acosmi

import (
	"strings"
	"testing"
)

func TestValidateEndUserID(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty allowed", "", false},
		{"alphanum allowed", "user_123-abc", false},
		{"all upper", "ABC", false},
		{"max length 512", strings.Repeat("a", 512), false},
		{"over length 513", strings.Repeat("a", 513), true},
		{"reject colon", "u:1", true},
		{"reject dot", "u.1", true},
		{"reject slash", "u/1", true},
		{"reject space", "u 1", true},
		{"reject chinese", "用户", true},
		{"reject leading null", "\x00abc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEndUserID(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateEndUserID(%q): err=%v wantErr=%v", tc.in, err, tc.wantErr)
			}
		})
	}
}

func TestIsSSECommentLine(t *testing.T) {
	if !isSSECommentLine(": keep-alive") {
		t.Error("expected ':keep-alive' to be a comment")
	}
	if !isSSECommentLine(":") {
		t.Error("expected ':' to be a comment")
	}
	if !isSSECommentLine(":ping") {
		t.Error("expected ':ping' to be a comment")
	}
	if isSSECommentLine("data: hi") {
		t.Error("data: line should not be comment")
	}
	if isSSECommentLine("event: foo") {
		t.Error("event: line should not be comment")
	}
	if isSSECommentLine("") {
		t.Error("empty line should not be comment (treat as separator)")
	}
}

func TestOpenAIAdapter_BuildRequestBody_EndUserID_TopLevel(t *testing.T) {
	a := &OpenAIAdapter{}
	req := &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		EndUserID: "user-abc-123",
	}
	body, err := a.BuildRequestBody(ModelCapabilities{}, req)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	got, ok := body["user_id"].(string)
	if !ok || got != "user-abc-123" {
		t.Fatalf("expected body[user_id]=user-abc-123, got %v", body["user_id"])
	}
}

func TestOpenAIAdapter_BuildRequestBody_EndUserID_OverridesExtraBody(t *testing.T) {
	a := &OpenAIAdapter{}
	req := &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		EndUserID: "winner",
		ExtraBody: map[string]any{"user_id": "loser"},
	}
	body, err := a.BuildRequestBody(ModelCapabilities{}, req)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	if got := body["user_id"]; got != "winner" {
		t.Fatalf("EndUserID must override ExtraBody[user_id]; got %v", got)
	}
}

func TestOpenAIAdapter_BuildRequestBody_EndUserID_Empty_NoInject(t *testing.T) {
	a := &OpenAIAdapter{}
	req := &ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
	body, err := a.BuildRequestBody(ModelCapabilities{}, req)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	if _, ok := body["user_id"]; ok {
		t.Fatalf("EndUserID empty must not inject body[user_id]")
	}
}

func TestAnthropicAdapter_BuildRequestBody_EndUserID_NestedInMetadata(t *testing.T) {
	a := &AnthropicAdapter{}
	req := &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		EndUserID: "user-xyz",
	}
	body, err := a.BuildRequestBody(ModelCapabilities{}, req)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	meta, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %T (%v)", body["metadata"], body["metadata"])
	}
	if meta["user_id"] != "user-xyz" {
		t.Fatalf("expected metadata.user_id=user-xyz, got %v", meta["user_id"])
	}
}

func TestAnthropicAdapter_BuildRequestBody_CallerMetadataUserIDWins(t *testing.T) {
	a := &AnthropicAdapter{}
	req := &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		EndUserID: "from-end-user-id",
		Metadata:  map[string]string{"user_id": "from-metadata"},
	}
	body, err := a.BuildRequestBody(ModelCapabilities{}, req)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	meta, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %T", body["metadata"])
	}
	if meta["user_id"] != "from-metadata" {
		t.Fatalf("caller Metadata[user_id] must win over EndUserID; got %v", meta["user_id"])
	}
}

func TestAnthropicAdapter_BuildRequestBody_PreservesOtherMetadataKeys(t *testing.T) {
	a := &AnthropicAdapter{}
	req := &ChatRequest{
		Messages:  []ChatMessage{{Role: "user", Content: "hi"}},
		EndUserID: "uid-1",
		Metadata:  map[string]string{"trace_id": "t-42"},
	}
	body, err := a.BuildRequestBody(ModelCapabilities{}, req)
	if err != nil {
		t.Fatalf("BuildRequestBody: %v", err)
	}
	meta, ok := body["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %T", body["metadata"])
	}
	if meta["trace_id"] != "t-42" {
		t.Fatalf("expected metadata.trace_id preserved; got %v", meta["trace_id"])
	}
	if meta["user_id"] != "uid-1" {
		t.Fatalf("expected metadata.user_id=uid-1; got %v", meta["user_id"])
	}
}
