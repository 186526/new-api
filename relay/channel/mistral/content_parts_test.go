package mistral

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// A thinking frame measured on api.mistral.ai with reasoning_effort high: the
// answer's reasoning arrives as a content list, which the OpenAI shape cannot
// hold in delta.content.
const mistralThinkingFrame = `data: {"id":"3756cf05","object":"chat.completion.chunk","created":1795256033,"model":"deepseek-v4-1-flash-1017","choices":[{"index":0,"delta":{"index":0,"content":[{"type":"thinking","thinking":[{"type":"text","text":"We"}],"closed":true}]},"finish_reason":null}]}`

const mistralTextFieldFrame = `data: {"id":"3756cf05","object":"chat.completion.chunk","created":1795256033,"model":"deepseek-v4-1-flash-1017","choices":[{"index":0,"delta":{"index":0,"content":[{"type":"text","text":"4"}]},"finish_reason":null}]}`

func decodeDelta(t *testing.T, line []byte) map[string]json.RawMessage {
	t.Helper()
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte("data: ")) {
		t.Fatalf("line is not an SSE data line: %s", line)
	}
	var frame struct {
		Choices []struct {
			Delta map[string]json.RawMessage `json:"delta"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(trimmed[len("data: "):], &frame); err != nil {
		t.Fatalf("decode frame %s: %v", line, err)
	}
	if len(frame.Choices) != 1 {
		t.Fatalf("frame has %d choices, want 1: %s", len(frame.Choices), line)
	}
	return frame.Choices[0].Delta
}

// The reasoning of a thinking frame reaches the client as reasoning_content,
// and the content it replaces is a string, so the OpenAI handler parses it.
func TestStreamFrameCarriesThinkingAsReasoningContent(t *testing.T) {
	rewritten := rewriteStreamLine([]byte(mistralThinkingFrame + "\n"))
	delta := decodeDelta(t, rewritten)

	if got := string(delta["content"]); got != `""` {
		t.Errorf("content = %s, want an empty string", got)
	}
	if got := string(delta["reasoning_content"]); got != `"We"` {
		t.Errorf("reasoning_content = %s, want %q", got, "We")
	}
}

func TestStreamFrameCarriesTextAsContent(t *testing.T) {
	rewritten := rewriteStreamLine([]byte(mistralTextFieldFrame + "\n"))
	delta := decodeDelta(t, rewritten)

	if got := string(delta["content"]); got != `"4"` {
		t.Errorf("content = %s, want %q", got, "4")
	}
	if _, ok := delta["reasoning_content"]; ok {
		t.Errorf("a text-only frame carries reasoning_content: %s", rewritten)
	}
}

// A frame whose content is already a string is what the upstream sent, and the
// cache and the client both read the stream as it arrived.
func TestStreamFrameWithStringContentIsUntouched(t *testing.T) {
	line := `data: {"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n"
	if got := rewriteStreamLine([]byte(line)); string(got) != line {
		t.Errorf("line was rewritten: %s", got)
	}
}

func TestStreamDoneMarkerIsUntouched(t *testing.T) {
	line := "data: [DONE]\n"
	if got := rewriteStreamLine([]byte(line)); string(got) != line {
		t.Errorf("line was rewritten: %s", got)
	}
}

func TestStreamRewriterPassesEveryLine(t *testing.T) {
	source := strings.Join([]string{
		mistralThinkingFrame,
		"",
		mistralTextFieldFrame,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	out, err := io.ReadAll(rewriteContentPartsStream(io.NopCloser(strings.NewReader(source))))
	if err != nil {
		t.Fatalf("read rewritten stream: %v", err)
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) != 6 {
		t.Fatalf("rewritten stream has %d lines, want 6: %q", len(lines), out)
	}
	delta := decodeDelta(t, []byte(lines[0]))
	if got := string(delta["reasoning_content"]); got != `"We"` {
		t.Errorf("reasoning_content = %s, want %q", got, "We")
	}
	if got := string(lines[2]); got != mistralTextFieldFrame && !strings.Contains(got, `"content":"4"`) {
		t.Errorf("text frame = %s, want the answer text as a string", got)
	}
	if lines[4] != "data: [DONE]" {
		t.Errorf("last line = %q, want the done marker", lines[4])
	}
}

// The non-streaming answer measured on the same upstream carries the same list
// under message.content.
func TestResponseBodyCarriesParts(t *testing.T) {
	body := `{"id":"9b6cd74e","object":"chat.completion","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","tool_calls":null,"content":[{"type":"thinking","thinking":[{"type":"text","text":"We need answer:"}],"closed":true},{"type":"text","text":"4"}]},"response_error":{"format_error":null}}],"usage":{"prompt_tokens":34,"completion_tokens":28,"total_tokens":62}}`

	rewritten := rewriteResponseBody([]byte(body))

	var frame struct {
		Choices []struct {
			Message map[string]json.RawMessage `json:"message"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(rewritten, &frame); err != nil {
		t.Fatalf("decode rewritten body %s: %v", rewritten, err)
	}
	message := frame.Choices[0].Message
	if got := string(message["content"]); got != `"4"` {
		t.Errorf("content = %s, want %q", got, "4")
	}
	if got := string(message["reasoning_content"]); got != `"We need answer:"` {
		t.Errorf("reasoning_content = %s", got)
	}
	if _, ok := message["tool_calls"]; !ok {
		t.Errorf("the rewrite dropped tool_calls: %s", rewritten)
	}
}

func TestResponseBodyWithStringContentIsUntouched(t *testing.T) {
	body := `{"choices":[{"index":0,"message":{"role":"assistant","content":"4"}}]}`
	if got := rewriteResponseBody([]byte(body)); string(got) != body {
		t.Errorf("body was rewritten: %s", got)
	}
}
