package mistral

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

func TestRequestOpenAI2MistralMapsDeveloperRoleToSystem(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "deepseek-v4.1-flash",
		Messages: []dto.Message{
			{Role: "developer", Content: "answer briefly"},
			{Role: "user", Content: "hello"},
		},
	}

	converted := conversionForTest(t, request)

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

	converted := conversionForTest(t, &dto.GeneralOpenAIRequest{Model: "m", Messages: messages})

	for i, want := range roles {
		if got := converted.Messages[i].Role; got != want {
			t.Errorf("message %d role = %q, want %q", i, got, want)
		}
	}
}

// conversionForTest runs the chat conversion without a stored request body, so
// it covers only the typed fields.
func conversionForTest(t *testing.T, request *dto.GeneralOpenAIRequest) *dto.GeneralOpenAIRequest {
	t.Helper()
	converted, ok := requestOpenAI2Mistral(&gin.Context{}, request).(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("conversion returned %T, want the typed request", requestOpenAI2Mistral(&gin.Context{}, request))
	}
	return converted
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

	converted := conversionForTest(t, request)

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

	converted := conversionForTest(t, request)

	if got := converted.Messages[0].Content; got != "line one" {
		t.Errorf("tool result content = %#v, want %q", got, "line one")
	}
}

// A client field the request struct does not declare used to be dropped on the
// way to Mistral, so a sampler extension never reached the upstream.
func TestRequestOpenAI2MistralCarriesFieldsItDoesNotModel(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"top_k":5,"chat_template_kwargs":{"thinking":true}}`)
	storage, err := common.CreateBodyStorage(body)
	if err != nil {
		t.Fatalf("create body storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.KeyBodyStorage, storage)

	request := &dto.GeneralOpenAIRequest{Model: "m", Messages: []dto.Message{{Role: "user", Content: "hi"}}}
	encoded, err := common.Marshal(requestOpenAI2Mistral(c, request))
	if err != nil {
		t.Fatalf("marshal converted request: %v", err)
	}
	var members map[string]json.RawMessage
	if err := common.Unmarshal(encoded, &members); err != nil {
		t.Fatalf("unmarshal converted request: %v", err)
	}
	if string(members["top_k"]) != "5" {
		t.Errorf("top_k = %s, want 5", members["top_k"])
	}
	if string(members["chat_template_kwargs"]) != `{"thinking":true}` {
		t.Errorf("chat_template_kwargs = %s, want the client value", members["chat_template_kwargs"])
	}
	if string(members["model"]) != `"m"` {
		t.Errorf("model = %s, want the converted value to win", members["model"])
	}
}
