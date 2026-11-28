package redaction

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

type ResponseRestorer struct {
	format       Format
	session      *Session
	items        map[string]*Restorer
	chatEnvelope responseObject
	changed      bool
}

func (s *Session) NewResponseRestorer(format Format) *ResponseRestorer {
	if len(s.restore) == 0 {
		return nil
	}
	return &ResponseRestorer{format: format, session: s, items: make(map[string]*Restorer)}
}

type responseObject = map[string]any

func responseMap(value any) responseObject {
	object, _ := value.(responseObject)
	return object
}

func responseList(value any) []any {
	items, _ := value.([]any)
	return items
}

func responseString(value any) string {
	text, _ := value.(string)
	return text
}

// JSON restores a final client payload; Frame handles one decoded SSE data
// payload or Responses WebSocket text message. Original bytes feed billing.
func (r *ResponseRestorer) JSON(body []byte) []byte  { return r.rewrite(body, false) }
func (r *ResponseRestorer) Frame(body []byte) []byte { return r.rewrite(body, true) }

func FailedResponseFrame(body []byte) bool {
	var header struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &header) != nil {
		return false
	}
	switch header.Type {
	case "error", "response.failed", "response.cancelled", "response.canceled":
		return true
	}
	return len(header.Error) != 0 && string(header.Error) != "null"
}

// Discard drops incomplete pieces without writing any client data.
func (r *ResponseRestorer) Discard() {
	if r != nil {
		clear(r.items)
	}
}

// BeforeFrame emits literal tails that must precede a protocol stop event.
// The caller forwards these client-only frames before rewriting the original.
func (r *ResponseRestorer) BeforeFrame(body []byte) [][]byte {
	if r == nil || len(r.items) == 0 {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(body), []byte("[DONE]")) {
		return r.Drain()
	}
	switch r.format {
	case Gemini:
		return r.beforeGeminiFinish(body)
	case Claude:
	default:
		return nil
	}
	var header struct {
		Type  string      `json:"type"`
		Index json.Number `json:"index"`
	}
	if json.Unmarshal(body, &header) != nil {
		return nil
	}
	switch header.Type {
	case "content_block_stop":
		if tail := r.drainItem(string(header.Index)); tail != nil {
			return [][]byte{tail}
		}
	case "message_stop":
		return r.Drain()
	}
	return nil
}

func (r *ResponseRestorer) beforeGeminiFinish(body []byte) [][]byte {
	var frame struct {
		Candidates []struct {
			Index        json.Number `json:"index"`
			FinishReason string      `json:"finishReason"`
			Content      struct {
				Role  string `json:"role"`
				Parts []struct {
					Text             *string         `json:"text"`
					Thought          bool            `json:"thought"`
					FunctionCall     json.RawMessage `json:"functionCall"`
					FunctionResponse json.RawMessage `json:"functionResponse"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(body, &frame) != nil {
		return nil
	}
	var frames [][]byte
	for position, candidate := range frame.Candidates {
		if candidate.FinishReason == "" || (candidate.Content.Role != "" && candidate.Content.Role != "model") {
			continue
		}
		index := string(candidate.Index)
		if index == "" {
			index = strconv.Itoa(position)
		}
		var keys []string
		for key := range r.items {
			suffix, found := strings.CutPrefix(key, index+":")
			if !found {
				continue
			}
			partIndex, err := strconv.Atoi(suffix)
			if err != nil || partIndex < 0 {
				continue
			}
			if partIndex < len(candidate.Content.Parts) {
				part := candidate.Content.Parts[partIndex]
				if part.Text != nil && !part.Thought && len(part.FunctionCall) == 0 && len(part.FunctionResponse) == 0 {
					continue // Frame will consume the final text fragment.
				}
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if tail := r.drainItem(key); tail != nil {
				frames = append(frames, tail)
			}
		}
	}
	return frames
}

// Drain is only for confirmed orderly termination. It emits text deltas,
// never a fabricated success marker or a full output snapshot.
func (r *ResponseRestorer) Drain() [][]byte {
	if r == nil {
		return nil
	}
	keys := make([]string, 0, len(r.items))
	for key := range r.items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var frames [][]byte
	for _, key := range keys {
		if tail := r.drainItem(key); tail != nil {
			frames = append(frames, tail)
		}
	}
	return frames
}

func (r *ResponseRestorer) drainItem(key string) []byte {
	item := r.items[key]
	if item == nil {
		return nil
	}
	delete(r.items, key)
	text := item.Push("", true)
	if text == "" {
		return nil
	}
	var frame responseObject
	switch r.format {
	case Chat:
		frame = responseObject{"object": "chat.completion.chunk", "choices": []responseObject{{"index": json.Number(key), "delta": responseObject{"content": text}, "finish_reason": nil}}}
		for field, value := range r.chatEnvelope {
			frame[field] = value
		}
	case Claude:
		frame = responseObject{"type": "content_block_delta", "index": json.Number(key), "delta": responseObject{"type": "text_delta", "text": text}}
	case Gemini:
		index, position, _ := strings.Cut(key, ":")
		partIndex, err := strconv.Atoi(position)
		if err != nil || partIndex < 0 || partIndex >= maxMatches {
			return nil
		}
		parts := make([]responseObject, partIndex+1)
		for index := range parts {
			parts[index] = responseObject{"text": ""}
		}
		parts[partIndex]["text"] = text
		frame = responseObject{"candidates": []responseObject{{"index": json.Number(index), "content": responseObject{"role": "model", "parts": parts}}}}
	case Responses:
		// Responses requires a terminal snapshot; a missing terminal is not
		// a clean EOF, and a snapshot already carries any remaining text.
		return nil
	}
	encoded, _ := json.Marshal(frame)
	return encoded
}

func (r *ResponseRestorer) rewrite(body []byte, stream bool) []byte {
	if r == nil || !json.Valid(body) {
		return body
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var object responseObject
	if decoder.Decode(&object) != nil || object == nil || object["error"] != nil {
		return body
	}
	r.changed = false
	switch r.format {
	case Chat:
		r.chat(object, stream)
	case Claude:
		r.claude(object, stream)
	case Gemini:
		r.gemini(object, stream)
	case Responses:
		r.responses(object, stream)
	}
	if !r.changed {
		return body
	}
	rewritten, err := json.Marshal(object)
	if err != nil {
		return body
	}
	return rewritten
}

func (r *ResponseRestorer) text(object responseObject, field, key string, stream, final bool) {
	text, ok := object[field].(string)
	if !ok {
		return
	}
	var restorer *Restorer
	if stream {
		restorer = r.items[key]
		if restorer == nil && len(r.items) < maxMatches {
			restorer = r.session.NewRestorer()
			r.items[key] = restorer
		}
	} else {
		restorer = r.session.NewRestorer()
	}
	if restorer == nil {
		return
	}
	restored := restorer.Push(text, final)
	if final && stream {
		delete(r.items, key)
	}
	if restored != text {
		object[field] = restored
		r.changed = true
	}
}

func (r *ResponseRestorer) finish(object responseObject, field, key string) {
	if r.items[key] == nil {
		return
	}
	object[field] = ""
	r.changed = true
	r.text(object, field, key, true, true)
}

func (r *ResponseRestorer) chat(object responseObject, stream bool) {
	if stream {
		if r.chatEnvelope == nil {
			r.chatEnvelope = make(responseObject, 3)
		}
		for _, field := range []string{"id", "model", "created"} {
			switch value := object[field].(type) {
			case string:
				if len(value) <= 256 {
					r.chatEnvelope[field] = value
				}
			case json.Number:
				if len(value) <= 20 {
					r.chatEnvelope[field] = value
				}
			}
		}
	}
	for _, value := range responseList(object["choices"]) {
		choice := responseMap(value)
		if !stream {
			message := responseMap(choice["message"])
			if message["role"] == "assistant" {
				r.text(message, "content", "", false, true)
			}
			continue
		}
		index, ok := choice["index"].(json.Number)
		if !ok {
			continue
		}
		key := string(index)
		final := choice["finish_reason"] != nil
		delta := responseMap(choice["delta"])
		if delta == nil && final && r.items[key] != nil && r.items[key].pending != "" {
			delta = responseObject{}
			choice["delta"] = delta
		}
		if delta == nil || (delta["role"] != nil && delta["role"] != "assistant") {
			continue
		}
		if _, ok := delta["content"].(string); !ok && final {
			r.finish(delta, "content", key)
			continue
		}
		r.text(delta, "content", key, true, final)
	}
}

func (r *ResponseRestorer) claude(object responseObject, stream bool) {
	if !stream {
		if object["role"] != "assistant" {
			return
		}
		for _, value := range responseList(object["content"]) {
			block := responseMap(value)
			if block["type"] == "text" {
				r.text(block, "text", "", false, true)
			}
		}
		return
	}
	index, ok := object["index"].(json.Number)
	if !ok {
		return
	}
	key := string(index)
	switch object["type"] {
	case "content_block_start":
		block := responseMap(object["content_block"])
		if block["type"] == "text" {
			r.text(block, "text", key, true, false)
		}
	case "content_block_delta":
		delta := responseMap(object["delta"])
		if delta["type"] == "text_delta" {
			r.text(delta, "text", key, true, false)
		}
	}
}

func (r *ResponseRestorer) gemini(object responseObject, stream bool) {
	for position, value := range responseList(object["candidates"]) {
		candidate := responseMap(value)
		content := responseMap(candidate["content"])
		if content["role"] != nil && content["role"] != "model" {
			continue
		}
		index, ok := candidate["index"].(json.Number)
		if !ok {
			index = json.Number(strconv.Itoa(position))
		}
		for partIndex, value := range responseList(content["parts"]) {
			part := responseMap(value)
			if part["thought"] == true || part["functionCall"] != nil || part["functionResponse"] != nil {
				continue
			}
			key := string(index) + ":" + strconv.Itoa(partIndex)
			r.text(part, "text", key, stream, !stream || candidate["finishReason"] != nil)
		}
	}
}

func (r *ResponseRestorer) responsesOutput(items []any) {
	for _, value := range items {
		item := responseMap(value)
		if item["type"] != "message" || item["role"] != "assistant" {
			continue
		}
		for _, value := range responseList(item["content"]) {
			part := responseMap(value)
			if part["type"] == "output_text" {
				r.text(part, "text", "", false, true)
			}
		}
	}
}

func (r *ResponseRestorer) responses(object responseObject, stream bool) {
	if !stream {
		r.responsesOutput(responseList(object["output"]))
		return
	}
	switch object["type"] {
	case "response.output_text.delta", "response.output_text.done":
		output, outputOK := object["output_index"].(json.Number)
		content, contentOK := object["content_index"].(json.Number)
		item := responseString(object["item_id"])
		if !outputOK || !contentOK || len(item) > 256 {
			return
		}
		key := item + ":" + string(output) + ":" + string(content)
		if object["type"] == "response.output_text.delta" {
			r.text(object, "delta", key, true, false)
			return
		}
		delete(r.items, key)
		r.text(object, "text", "", false, true)
	case "response.content_part.done":
		part := responseMap(object["part"])
		if part["type"] == "output_text" {
			r.text(part, "text", "", false, true)
		}
	case "response.output_item.done":
		r.responsesOutput([]any{object["item"]})
	case "response.completed", "response.done", "response.incomplete":
		clear(r.items)
		r.responsesOutput(responseList(responseMap(object["response"])["output"]))
	case "response.failed", "response.cancelled", "response.canceled":
		clear(r.items)
	}
}
