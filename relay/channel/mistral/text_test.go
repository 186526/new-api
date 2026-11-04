package mistral

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestRequestOpenAI2MistralMapsDeveloperRoleToSystem(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "deepseek-v4.1-flash",
		Messages: []dto.Message{
			{Role: "developer", Content: "answer briefly"},
			{Role: "user", Content: "hello"},
		},
	}

	converted := requestOpenAI2Mistral(request)

	wantRoles := []string{"system", "user"}
	if len(converted.Messages) != len(wantRoles) {
		t.Fatalf("got %d messages, want %d", len(converted.Messages), len(wantRoles))
	}
	for i, want := range wantRoles {
		if got := converted.Messages[i].Role; got != want {
			t.Errorf("message %d role = %q, want %q", i, got, want)
		}
	}
	if got := messageText(t, converted.Messages[0]); got != "answer briefly" {
		t.Errorf("developer message text = %q, want %q", got, "answer briefly")
	}
}

func TestRequestOpenAI2MistralKeepsSupportedRoles(t *testing.T) {
	roles := []string{"system", "user", "assistant", "tool"}
	messages := make([]dto.Message, 0, len(roles))
	for _, role := range roles {
		messages = append(messages, dto.Message{Role: role, Content: role})
	}

	converted := requestOpenAI2Mistral(&dto.GeneralOpenAIRequest{Model: "m", Messages: messages})

	for i, want := range roles {
		if got := converted.Messages[i].Role; got != want {
			t.Errorf("message %d role = %q, want %q", i, got, want)
		}
	}
}

func messageText(t *testing.T, message dto.Message) string {
	t.Helper()
	parts := message.ParseContent()
	if len(parts) == 0 {
		return ""
	}
	return parts[0].Text
}

// Mistral rejects a tool result whose content is not a string with 422
// "Input should be a valid string", which is what an empty result becomes when
// it is turned into content parts.
func TestRequestOpenAI2MistralKeepsToolContentAsString(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "deepseek-v4.1-flash",
		Messages: []dto.Message{
			{Role: "user", Content: "run it"},
			{Role: "assistant", Content: "", ToolCalls: json.RawMessage(`[{"id":"c1","type":"function","function":{"name":"bash","arguments":"{}"}}]`)},
			{Role: "tool", Content: "", ToolCallId: "c1"},
			{Role: "tool", Content: "ok", ToolCallId: "c1"},
		},
	}

	converted := requestOpenAI2Mistral(request)

	for _, index := range []int{2, 3} {
		message := converted.Messages[index]
		if message.Role != "tool" {
			t.Fatalf("message %d role = %q, want tool", index, message.Role)
		}
		if _, ok := message.Content.(string); !ok {
			t.Errorf("message %d content = %#v, want a string", index, message.Content)
		}
	}
	if got := converted.Messages[2].Content; got != "" {
		t.Errorf("empty tool result content = %#v, want empty string", got)
	}
	if got := converted.Messages[3].Content; got != "ok" {
		t.Errorf("tool result content = %#v, want %q", got, "ok")
	}
	if converted.Messages[2].ToolCallId != converted.Messages[3].ToolCallId {
		t.Errorf("tool_call_id %q does not match the assistant tool call", converted.Messages[2].ToolCallId)
	}
}

func TestRequestOpenAI2MistralKeepsTextToolResultAsString(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "deepseek-v4.1-flash",
		Messages: []dto.Message{
			{Role: "tool", Content: []any{map[string]any{"type": "text", "text": "line one"}}, ToolCallId: "c1"},
		},
	}

	converted := requestOpenAI2Mistral(request)

	if got := converted.Messages[0].Content; got != "line one" {
		t.Errorf("tool result content = %#v, want %q", got, "line one")
	}
}
