package mistral

import (
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
