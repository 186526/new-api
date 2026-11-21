package mistral

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// clientBodyForTest stores the body as the relay handler does, so a conversion
// reads exactly what the client sent.
func clientBodyForTest(t *testing.T, body string) *gin.Context {
	t.Helper()
	storage, err := common.CreateBodyStorage([]byte(body))
	if err != nil {
		t.Fatalf("create body storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.KeyBodyStorage, storage)
	return c
}

// conversionForTest runs the conversion on the client body. The typed request
// is left empty: the conversion reads the body, not its fields.
func conversionForTest(t *testing.T, body string) []byte {
	t.Helper()
	encoded, err := requestOpenAI2Mistral(clientBodyForTest(t, body), &dto.GeneralOpenAIRequest{})
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}
	return encoded
}

func decodedForTest(t *testing.T, encoded []byte) (map[string]json.RawMessage, []map[string]json.RawMessage) {
	t.Helper()
	var body map[string]json.RawMessage
	if err := common.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("unmarshal converted request %s: %v", encoded, err)
	}
	var messages []map[string]json.RawMessage
	if err := common.Unmarshal(body["messages"], &messages); err != nil {
		t.Fatalf("unmarshal converted messages %s: %v", body["messages"], err)
	}
	return body, messages
}

func TestRequestOpenAI2MistralMapsDeveloperRoleToSystem(t *testing.T) {
	encoded := conversionForTest(t, `{"model":"m","messages":[{"role":"developer","content":"answer briefly"},{"role":"user","content":"hello"}]}`)

	_, messages := decodedForTest(t, encoded)

	wantRoles := []string{"system", "user"}
	if len(messages) != len(wantRoles) {
		t.Fatalf("got %d messages, want %d", len(messages), len(wantRoles))
	}
	for i, want := range wantRoles {
		if got := jsonString(messages[i]["role"]); got != want {
			t.Errorf("message %d role = %q, want %q", i, got, want)
		}
	}
	if got := jsonString(messages[0]["content"]); got != "answer briefly" {
		t.Errorf("developer message text = %q, want %q", got, "answer briefly")
	}
}

func TestRequestOpenAI2MistralKeepsSupportedRoles(t *testing.T) {
	encoded := conversionForTest(t, `{"model":"m","messages":[{"role":"system","content":"system"},{"role":"user","content":"user"},{"role":"assistant","content":"assistant"},{"role":"tool","content":"tool","tool_call_id":"abc123456"}]}`)

	_, messages := decodedForTest(t, encoded)

	wantRoles := []string{"system", "user", "assistant", "tool"}
	for i, want := range wantRoles {
		if got := jsonString(messages[i]["role"]); got != want {
			t.Errorf("message %d role = %q, want %q", i, got, want)
		}
	}
}

// Mistral rejects a tool result whose content is not a string with 422
// "Input should be a valid string", which is what an empty result becomes when
// it stays an array of content parts.
func TestRequestOpenAI2MistralKeepsToolContentAsString(t *testing.T) {
	encoded := conversionForTest(t, `{"model":"m","messages":[
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{}"}}]},
		{"role":"tool","content":"","tool_call_id":"call_1"},
		{"role":"tool","content":"ok","tool_call_id":"call_1"},
		{"role":"tool","content":[],"tool_call_id":"call_1"}
	]}`)

	_, messages := decodedForTest(t, encoded)

	for index := 1; index < len(messages); index++ {
		var text string
		if err := common.Unmarshal(messages[index]["content"], &text); err != nil {
			t.Errorf("message %d content = %s, want a string", index, messages[index]["content"])
		}
	}
	if got := jsonString(messages[2]["content"]); got != "ok" {
		t.Errorf("tool result content = %q, want %q", got, "ok")
	}
	if messages[1]["content"] == nil || messages[3]["content"] == nil {
		t.Errorf("empty tool result content must stay a string, got %s and %s", messages[1]["content"], messages[3]["content"])
	}
	if string(messages[0]["content"]) != "null" {
		t.Errorf("empty assistant content with tool calls = %s, want null", messages[0]["content"])
	}
}

func TestRequestOpenAI2MistralKeepsTextToolResultAsString(t *testing.T) {
	encoded := conversionForTest(t, `{"model":"m","messages":[{"role":"tool","content":[{"type":"text","text":"line one"}],"tool_call_id":"abc123456"}]}`)

	_, messages := decodedForTest(t, encoded)

	if got := jsonString(messages[0]["content"]); got != "line one" {
		t.Errorf("tool result content = %q, want %q", got, "line one")
	}
}

// The upstream prompt cache is keyed on the request body, so a rewritten tool
// call id must read the same on every call.
func TestRequestOpenAI2MistralRewritesAnInvalidToolCallIdDeterministically(t *testing.T) {
	body := `{"model":"m","messages":[
		{"role":"assistant","content":null,"tool_calls":[{"id":"functions.bash:0","type":"function","function":{"name":"bash","arguments":"{}"}}]},
		{"role":"tool","content":"ok","tool_call_id":"functions.bash:0"}
	]}`

	first := conversionForTest(t, body)
	second := conversionForTest(t, body)
	if !bytes.Equal(first, second) {
		t.Fatalf("two conversions of one body differ:\n%s\n%s", first, second)
	}

	_, messages := decodedForTest(t, first)
	var calls []map[string]json.RawMessage
	if err := common.Unmarshal(messages[0]["tool_calls"], &calls); err != nil {
		t.Fatalf("unmarshal tool calls: %v", err)
	}
	callId := jsonString(calls[0]["id"])
	if !regexp.MustCompile(`^[a-zA-Z0-9]{9}$`).MatchString(callId) {
		t.Errorf("tool call id = %q, want nine alphanumerics", callId)
	}
	if got := jsonString(messages[1]["tool_call_id"]); got != callId {
		t.Errorf("tool_call_id = %q, want the rewritten tool call id %q", got, callId)
	}
}

// A client field the request struct does not declare, and the value of every
// member the conversion does not rewrite, must reach Mistral as the client sent
// it. JSON objects are read as maps, so the order is not compared here.
func TestRequestOpenAI2MistralForwardsTopLevelMembersUntouched(t *testing.T) {
	body := `{"model":"m","prompt_cache_key":"a1b2c3","messages":[{"role":"user","content":"hi"}],"seed":42,"temperature":0.2,"top_k":5,"metadata":{"trace":"abc"},"chat_template_kwargs":{"thinking":true},"max_tokens":16}`

	encoded := conversionForTest(t, body)

	var sent, converted map[string]json.RawMessage
	if err := common.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("unmarshal client body: %v", err)
	}
	if err := common.Unmarshal(encoded, &converted); err != nil {
		t.Fatalf("unmarshal converted body: %v", err)
	}
	if len(converted) != len(sent) {
		t.Fatalf("converted body has %d members, want the client's %d: %s", len(converted), len(sent), encoded)
	}
	for name, want := range sent {
		if name == "messages" {
			continue
		}
		if got, ok := converted[name]; !ok {
			t.Errorf("member %q is missing from the converted body", name)
		} else if !bytes.Equal(got, want) {
			t.Errorf("member %q = %s, want %s", name, got, want)
		}
	}
	for name := range converted {
		if _, ok := sent[name]; !ok {
			t.Errorf("the converted body has a member the client did not send: %q", name)
		}
	}
}
