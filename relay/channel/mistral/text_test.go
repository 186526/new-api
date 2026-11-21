package mistral

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

// channelTestContextForTest builds the context the channel test in
// controller/channel-test.go builds: a gin test context whose request carries
// no body at all, so the storage the relay records for it holds nothing.
func channelTestContextForTest(t *testing.T) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// The channel test never writes a request body, yet the relay still records a
// storage for the request. This pins that shape, so a test below that reads an
// empty body keeps testing what the channel test produces.
func TestChannelTestRequestStoresAnEmptyBody(t *testing.T) {
	c := channelTestContextForTest(t)

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		t.Fatalf("get body storage: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if size := storage.Size(); size != 0 {
		t.Fatalf("stored body size = %d, want 0", size)
	}
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

// The relay layer resolves the channel model mapping before the adaptor runs,
// so the request carries the upstream name while the stored body still carries
// the name the client asked for. The body that reaches Mistral must name the
// upstream model, or the upstream rejects it as an unknown model.
func TestRequestOpenAI2MistralSendsTheMappedModel(t *testing.T) {
	c := clientBodyForTest(t, `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}]}`)
	request := &dto.GeneralOpenAIRequest{Model: "deepseek-v4-1-flash-1017"}

	encoded, err := requestOpenAI2Mistral(c, request)
	if err != nil {
		t.Fatalf("convert request: %v", err)
	}

	body, _ := decodedForTest(t, encoded)
	if got := jsonString(body["model"]); got != "deepseek-v4-1-flash-1017" {
		t.Errorf("model = %q, want the mapped upstream model %q", got, "deepseek-v4-1-flash-1017")
	}
}

// A channel test builds its context without a request body, so the stored body
// is empty. The conversion must read the request the caller parsed instead of
// parsing the empty body, which fails with "unexpected end of JSON input" and
// leaves the channel test reporting an unrelated conversion error.
func TestRequestOpenAI2MistralFallsBackToTheParsedRequestWhenTheStoredBodyIsEmpty(t *testing.T) {
	maxTokens := uint(16)
	request := &dto.GeneralOpenAIRequest{
		Model:     "deepseek-v4-1-flash-1017",
		MaxTokens: &maxTokens,
		Messages:  []dto.Message{{Role: "user", Content: "hi"}},
	}

	encoded, err := requestOpenAI2Mistral(channelTestContextForTest(t), request)
	if err != nil {
		t.Fatalf("convert a request whose stored body is empty: %v", err)
	}

	marshalled, err := common.Marshal(request)
	if err != nil {
		t.Fatalf("marshal the parsed request: %v", err)
	}
	want := conversionForTest(t, string(marshalled))
	if !bytes.Equal(encoded, want) {
		t.Errorf("converted body = %s, want the conversion of the parsed request %s", encoded, want)
	}
}

// A request with neither a stored body nor a parsed one is refused by name. An
// empty body must not surface as a JSON parse failure, and a nil request must
// not read as success.
func TestRequestOpenAI2MistralRefusesWithoutAStoredBodyOrAParsedRequest(t *testing.T) {
	_, err := requestOpenAI2Mistral(channelTestContextForTest(t), nil)

	if err == nil {
		t.Fatalf("convert a request with neither a stored body nor a parsed request: error = nil, want an explicit refusal")
	}
	if strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Errorf("error = %q, want a refusal that names the missing request rather than a JSON parse failure", err)
	}
	if !strings.Contains(err.Error(), "neither stored nor parsed") {
		t.Errorf("error = %q, want it to name the missing request body", err)
	}
}

// A stored body is what the client sent, so it is forwarded as it stands and
// the parsed request does not replace it: the upstream prompt cache is keyed on
// the request body, and a rebuilt body misses the cache on every call.
func TestRequestOpenAI2MistralForwardsAStoredBodyInsteadOfTheParsedRequest(t *testing.T) {
	body := `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hi"}],"temperature":0.25,"prompt_cache_key":"a1b2c3"}`
	temperature := 0.9
	request := &dto.GeneralOpenAIRequest{
		Model:       "deepseek-v4-1-flash-1017",
		Temperature: &temperature,
		Messages:    []dto.Message{{Role: "user", Content: "rebuilt"}},
	}

	encoded, err := requestOpenAI2Mistral(clientBodyForTest(t, body), request)
	if err != nil {
		t.Fatalf("convert a request with a stored body: %v", err)
	}

	converted, messages := decodedForTest(t, encoded)
	if got := string(converted["temperature"]); got != "0.25" {
		t.Errorf("temperature = %s, want the client's 0.25", got)
	}
	if got := string(converted["prompt_cache_key"]); got != `"a1b2c3"` {
		t.Errorf("prompt_cache_key = %s, want the client's %q", got, "a1b2c3")
	}
	if got := jsonString(messages[0]["content"]); got != "hi" {
		t.Errorf("message content = %q, want the client's %q", got, "hi")
	}
}

func TestRequestOpenAI2MistralDropsReasoningContent(t *testing.T) {
	encoded := conversionForTest(t, `{"model":"m","messages":[{"role":"assistant","content":"hi","reasoning_content":"why"}]}`)
	if strings.Contains(string(encoded), "reasoning_content") {
		t.Errorf("converted body still carries reasoning_content: %s", encoded)
	}
}
