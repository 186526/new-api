package redaction

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResponseRestorerDrainsLiteralTailAtProtocolStop(t *testing.T) {
	config := DefaultConfig()
	config.RestorePII = true
	policy, err := Compile(config, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	placeholder, err := session.Redact("alice@example.invalid")
	if err != nil || len(placeholder) < 8 {
		t.Fatalf("could not generate placeholder: %v", err)
	}
	prefix := placeholder[:8]
	quoted, _ := json.Marshal(prefix)
	for _, tc := range []struct {
		format      Format
		frame, stop string
	}{
		{Chat, `{"choices":[{"index":0,"delta":{"content":%s}}]}`, `[DONE]`},
		{Claude, `{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":%s}}`, `{"type":"content_block_stop","index":1}`},
		{Gemini, `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":%s}]}}]}`, `{"candidates":[{"index":0,"finishReason":"STOP"}]}`},
	} {
		t.Run(string(tc.format), func(t *testing.T) {
			restorer := session.NewResponseRestorer(tc.format)
			frame := []byte(strings.Replace(tc.frame, "%s", string(quoted), 1))
			if strings.Contains(string(restorer.Frame(frame)), string(quoted)) {
				t.Fatal("known prefix was written before the stop")
			}
			tails := restorer.BeforeFrame([]byte(tc.stop))
			if len(tails) != 1 || !strings.Contains(string(tails[0]), string(quoted)) || len(restorer.Drain()) != 0 {
				t.Fatal("literal tail was not emitted exactly once before the stop")
			}
			restorer.Frame(frame)
			restorer.Discard()
			if len(restorer.Drain()) != 0 {
				t.Fatal("discarded tail was emitted")
			}
		})
	}
}

func TestResponseRestorerSeparatesTextItemsAndIgnoresTools(t *testing.T) {
	config := DefaultConfig()
	config.RestorePII = true
	policy, err := Compile(config, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	const email = "alice@example.invalid"
	token, err := session.Redact(email)
	if err != nil || len(token) > 32 {
		t.Fatalf("unexpected token length: %d", len(token))
	}
	restorer := session.NewResponseRestorer(Responses)
	frame := func(event string, index int, text string) []byte {
		body, _ := json.Marshal(map[string]any{"type": event, "item_id": "msg", "output_index": 0, "content_index": index, "delta": text})
		return restorer.Frame(body)
	}
	const delta = "response.output_text.delta"
	if result := frame(delta, 0, token[:6]); !strings.Contains(string(result), `"delta":""`) {
		t.Fatal("known prefix was not buffered")
	}
	if result := frame(delta, 1, token[6:]); strings.Contains(string(result), email) {
		t.Fatal("mixed output items")
	}
	if result := frame(delta, 0, token[6:]); !strings.Contains(string(result), email) {
		t.Fatal("visible text was not restored")
	}
	if result := frame("response.function_call_arguments.delta", 0, token); strings.Contains(string(result), email) {
		t.Fatal("restored function arguments")
	}
	if result := session.NewRestorer().Push("ordinary <word", false); result != "ordinary <word" {
		t.Fatal("ordinary text was buffered")
	}
}
