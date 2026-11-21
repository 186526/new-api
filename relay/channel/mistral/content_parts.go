package mistral

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// Mistral streams the answer of a reasoning model as content parts instead of
// a string: a thinking part carries the reasoning text under its own nested
// list, and a text part carries one piece of the answer. The OpenAI shape the
// relay parses holds a string in delta.content and the reasoning in
// delta.reasoning_content, so a frame that arrives as a list is unreadable
// there: the parse fails, the reasoning is dropped, and the client never sees
// it. Both the streaming and the non-streaming bodies are rewritten into the
// OpenAI shape before the OpenAI handlers read them.
//
//	{"content":[{"type":"thinking","thinking":[{"type":"text","text":"We"}]}]}
//	-> {"content":"","reasoning_content":"We"}
//	{"content":[{"type":"text","text":"4"}]} -> {"content":"4"}

const streamDataPrefix = "data: "

// contentPart is one member of a content list. A thinking part nests the
// reasoning text one level deeper than a text part.
type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking []struct {
		Text string `json:"text"`
	} `json:"thinking"`
}

// flattenContentArray turns a content list into the answer text and the
// reasoning text. ok is false for every shape that is not a list, which leaves
// the frame as the upstream wrote it.
func flattenContentArray(raw json.RawMessage) (text, thinking string, ok bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return "", "", false
	}
	var parts []contentPart
	if err := common.Unmarshal(raw, &parts); err != nil {
		return "", "", false
	}
	var answer, reasoning strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "text":
			answer.WriteString(part.Text)
		case "thinking":
			for _, inner := range part.Thinking {
				reasoning.WriteString(inner.Text)
			}
		}
	}
	return answer.String(), reasoning.String(), true
}

// rewriteContentMember replaces a content member that holds a list, and adds
// the reasoning as reasoning_content. It reports whether it changed anything.
func rewriteContentMember(members map[string]json.RawMessage) bool {
	text, thinking, ok := flattenContentArray(members["content"])
	if !ok {
		return false
	}
	encoded, err := common.Marshal(text)
	if err != nil {
		return false
	}
	members["content"] = encoded
	if thinking != "" {
		encoded, err = common.Marshal(thinking)
		if err != nil {
			return false
		}
		members["reasoning_content"] = encoded
	}
	return true
}

// rewriteStreamLine rewrites one SSE line of a chat completion stream. Every
// line that is not a content list is returned as it arrived, so the frames of
// a plain answer stay byte for byte what the upstream sent.
func rewriteStreamLine(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if !bytes.HasPrefix(trimmed, []byte(streamDataPrefix)) {
		return line
	}
	payload := trimmed[len(streamDataPrefix):]
	if !bytes.Contains(payload, []byte(`"content":[`)) {
		return line
	}
	var frame map[string]json.RawMessage
	if err := common.Unmarshal(payload, &frame); err != nil {
		return line
	}
	var choices []map[string]json.RawMessage
	if err := common.Unmarshal(frame["choices"], &choices); err != nil {
		return line
	}
	changed := false
	for i := range choices {
		var delta map[string]json.RawMessage
		if err := common.Unmarshal(choices[i]["delta"], &delta); err != nil {
			continue
		}
		if rewriteContentMember(delta) {
			encoded, err := common.Marshal(delta)
			if err != nil {
				continue
			}
			choices[i]["delta"] = encoded
			changed = true
		}
	}
	if !changed {
		return line
	}
	encodedChoices, err := common.Marshal(choices)
	if err != nil {
		return line
	}
	frame["choices"] = encodedChoices
	encoded, err := common.Marshal(frame)
	if err != nil {
		return line
	}
	return []byte(streamDataPrefix + string(encoded) + "\n")
}

// rewriteResponseBody rewrites the content list of a non-streaming answer.
func rewriteResponseBody(body []byte) []byte {
	if !bytes.Contains(body, []byte(`"content":[`)) {
		return body
	}
	var frame map[string]json.RawMessage
	if err := common.Unmarshal(body, &frame); err != nil {
		return body
	}
	var choices []map[string]json.RawMessage
	if err := common.Unmarshal(frame["choices"], &choices); err != nil {
		return body
	}
	changed := false
	for i := range choices {
		var message map[string]json.RawMessage
		if err := common.Unmarshal(choices[i]["message"], &message); err != nil {
			continue
		}
		if rewriteContentMember(message) {
			encoded, err := common.Marshal(message)
			if err != nil {
				continue
			}
			choices[i]["message"] = encoded
			changed = true
		}
	}
	if !changed {
		return body
	}
	encodedChoices, err := common.Marshal(choices)
	if err != nil {
		return body
	}
	frame["choices"] = encodedChoices
	encoded, err := common.Marshal(frame)
	if err != nil {
		return body
	}
	return encoded
}

// streamRewriter rewrites the content lists of a stream line by line. Bytes of
// a line that needs no rewrite are handed on as they were read.
type streamRewriter struct {
	source io.ReadCloser
	reader *bufio.Reader
	out    []byte
	err    error
}

func rewriteContentPartsStream(source io.ReadCloser) io.ReadCloser {
	return &streamRewriter{source: source, reader: bufio.NewReaderSize(source, 64*1024)}
}

func (w *streamRewriter) Read(p []byte) (int, error) {
	for len(w.out) == 0 {
		line, err := w.reader.ReadBytes('\n')
		if len(line) > 0 {
			w.out = rewriteStreamLine(line)
		}
		if err != nil {
			w.err = err
		}
		if len(w.out) > 0 {
			break
		}
		if w.err != nil {
			return 0, w.err
		}
	}
	n := copy(p, w.out)
	w.out = w.out[n:]
	return n, nil
}

func (w *streamRewriter) Close() error {
	return w.source.Close()
}
