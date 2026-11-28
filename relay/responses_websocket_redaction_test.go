package relay

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/gorilla/websocket"
)

func TestResponsesWSRestoresOnlyCurrentTurnClientText(t *testing.T) {
	config := redaction.DefaultConfig()
	config.RestorePII = true
	policy, err := redaction.Compile(config, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	newTurn := func() (*redaction.Session, string) {
		t.Helper()
		session, err := policy.NewSession(nil)
		if err != nil {
			t.Fatal(err)
		}
		placeholder, err := session.Redact("alice@example.invalid")
		if err != nil || !strings.HasPrefix(placeholder, "<EMAIL_") {
			t.Fatalf("could not make email placeholder: %v", err)
		}
		return session, placeholder
	}
	session, placeholder := newTurn()
	restorer := session.NewResponseRestorer(redaction.Responses)
	const delta = "response.output_text.delta"
	frame := func(kind int, eventType, text string) responsesWSMessage {
		return responsesWSMessage{kind: kind, body: []byte(fmt.Sprintf(`{"type":%q,"item_id":"msg_1","output_index":0,"content_index":0,"delta":%q}`, eventType, text))}
	}
	prefix := frame(websocket.TextMessage, delta, "hello "+placeholder[:6])
	if got := restoreResponsesWSClientFrame(restorer, prefix, delta); !strings.Contains(string(got.body), `"delta":"hello "`) {
		t.Fatalf("ordinary text was not delivered immediately: %s", got.body)
	}
	for _, raw := range []responsesWSMessage{
		frame(websocket.BinaryMessage, delta, placeholder[6:]),
		frame(websocket.TextMessage, "response.function_call_arguments.delta", placeholder),
		frame(websocket.TextMessage, "response.reasoning_summary_text.delta", placeholder),
	} {
		var envelope struct{ Type string }
		if err := json.Unmarshal(raw.body, &envelope); err != nil {
			t.Fatal(err)
		}
		if got := restoreResponsesWSClientFrame(restorer, raw, envelope.Type); string(got.body) != string(raw.body) {
			t.Fatalf("modified an ineligible %s frame", envelope.Type)
		}
	}
	suffix := frame(websocket.TextMessage, delta, placeholder[6:])
	if got := restoreResponsesWSClientFrame(restorer, suffix, delta); !strings.Contains(string(got.body), "alice@example.invalid") || strings.Contains(string(suffix.body), "alice@example.invalid") {
		t.Fatal("restoration did not affect only the outgoing copy")
	}
	terminal := responsesWSMessage{kind: websocket.TextMessage, body: []byte(fmt.Sprintf(`{"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]},{"type":"function_call","arguments":%q}]}}`, placeholder, placeholder))}
	if got := restoreResponsesWSClientFrame(restorer, terminal, "response.completed"); strings.Count(string(got.body), "alice@example.invalid") != 1 {
		t.Fatal("terminal restoration must only affect assistant text")
	}
	other, _ := newTurn()
	old := frame(websocket.TextMessage, delta, placeholder)
	if got := restoreResponsesWSClientFrame(other.NewResponseRestorer(redaction.Responses), old, delta); string(got.body) != string(old.body) {
		t.Fatal("restored a placeholder from another turn")
	}
}

func TestProtectedResponsesWSCancelRequiresCurrentIDsAndKnownFields(t *testing.T) {
	policy, err := redaction.Compile(redaction.DefaultConfig(), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"type":"response.cancel","event_id":"cancel.1","stream_id":"active"}`,
		`{"type":"response.cancel","response_id":"resp_active"}`,
	} {
		if _, err := protectedResponsesWSCancel([]byte(body), "active", "resp_active", session.CheckControlIdentifiers); err != nil {
			t.Errorf("rejected correlated cancel %s: %v", body, err)
		}
	}
	for _, body := range []string{
		`{"type":"response.cancel"}`,
		`{"type":"response.cancel","stream_id":"active","response_id":"resp_other"}`,
		`{"type":"response.cancel","stream_id":"other"}`,
		`{"type":"response.cancel","response_id":"resp_active","extra":"uninspected"}`,
		`{"type":"response.cancel","stream_id":"active","event_id":"user@example.invalid"}`,
	} {
		if _, err := protectedResponsesWSCancel([]byte(body), "active", "resp_active", session.CheckControlIdentifiers); err == nil {
			t.Errorf("accepted invalid protected cancel %s", body)
		}
	}
}

func TestObserveResponsesWSReasoningReadsFinalEnvelope(t *testing.T) {
	meta := &relaycommon.AutoReasoningMeta{Reason: "policy", PolicyEffort: "low"}
	payload, err := buildResponsesWSCreateEvent([]byte(`{"model":"test-model","reasoning":{"effort":"high","max_tokens":3072,"other":"ignored"}}`), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	observeResponsesWSReasoning(meta, payload)
	if len(meta.OutboundFields) != 2 || meta.OutboundFields["reasoning.effort"] != "high" || meta.OutboundFields["reasoning.max_tokens"] != float64(3072) || meta.PolicyEffort != "low" {
		t.Fatalf("unexpected reasoning fields: %#v", meta.OutboundFields)
	}
}
