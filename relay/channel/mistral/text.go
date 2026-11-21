package mistral

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

var mistralToolCallIdRegexp = regexp.MustCompile("^[a-zA-Z0-9]{9}$")

// requestOpenAI2Mistral forwards the body the client sent and rewrites only the
// members Mistral rejects. The upstream prompt cache is keyed on the request
// body, so rebuilding the request with this struct's member order and defaults
// misses the cache on every call, and an uncached prompt is billed at ten times
// the input rate.
//
// The rewritten members are: a tool_calls[].id or tool_call_id that is not nine
// alphanumerics, a role of "developer", which becomes "system", the content of a
// tool message, which becomes the string of its text parts, and the empty
// content of an assistant message that carries tool calls, which becomes null.
// Every other member keeps its value and its position in the body.
func requestOpenAI2Mistral(c *gin.Context, request *dto.GeneralOpenAIRequest) ([]byte, error) {
	raw, err := clientRequestBody(c, request)
	if err != nil {
		return nil, err
	}
	body, err := mistralRequestBody(raw, request)
	if err != nil {
		return nil, err
	}
	return common.Marshal(body)
}

// clientRequestBody is the body the client sent. A channel tried without a
// stored body falls back to the body the caller parsed.
func clientRequestBody(c *gin.Context, request *dto.GeneralOpenAIRequest) ([]byte, error) {
	if c != nil && c.Request != nil {
		storage, err := common.GetBodyStorage(c)
		if err == nil {
			return storage.Bytes()
		}
	}
	if request == nil {
		return nil, errors.New("the request body is neither stored nor parsed")
	}
	return common.Marshal(request)
}

// mistralRequestBody parses the client body and rewrites the members Mistral
// rejects. Each member stays a json.RawMessage, which keeps both its value and
// its order.
func mistralRequestBody(raw []byte, request *dto.GeneralOpenAIRequest) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := common.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	var messages []json.RawMessage
	if err := common.Unmarshal(body["messages"], &messages); err != nil {
		return nil, err
	}
	for i := range messages {
		rewritten, err := rewriteMistralMessage(messages[i])
		if err != nil {
			return nil, err
		}
		messages[i] = rewritten
	}
	encoded, err := common.Marshal(messages)
	if err != nil {
		return nil, err
	}
	body["messages"] = encoded
	// The model the client asked for is not the model the channel maps it to.
	// The relay layer resolves the mapping before the adaptor runs, so the
	// mapped name is the one the upstream accepts.
	if request != nil && request.Model != "" {
		model, err := common.Marshal(request.Model)
		if err != nil {
			return nil, err
		}
		body["model"] = model
	}
	// The metadata carries the allowance tag. It is absent from a body read
	// from a channel test or a retry, so the tag the adaptor applied survives.
	if _, ok := body["metadata"]; !ok && request != nil && len(request.Metadata) > 0 {
		body["metadata"] = request.Metadata
	}
	return common.Marshal(body)
}

// rewriteMistralMessage rewrites one message member of the messages array and
// leaves the other members where they are.
func rewriteMistralMessage(message json.RawMessage) (json.RawMessage, error) {
	var members map[string]json.RawMessage
	if err := common.Unmarshal(message, &members); err != nil {
		return nil, err
	}
	role := jsonString(members["role"])
	// Mistral rejects every member of a message it does not declare, so the
	// reasoning text the client stores on an assistant turn is dropped here:
	// the earlier rebuild of the request happened to drop it too.
	delete(members, "reasoning_content")
	if role == "developer" {
		members["role"] = json.RawMessage(`"system"`)
		role = "system"
	}
	switch role {
	case "tool":
		content, err := toolResultText(members["content"])
		if err != nil {
			return nil, err
		}
		members["content"] = content
	case "assistant":
		if hasToolCalls(members["tool_calls"]) && strings.TrimSpace(jsonString(members["content"])) == "" {
			members["content"] = json.RawMessage(`null`)
		}
	}
	if toolCallId, ok := members["tool_call_id"]; ok {
		newId, err := mistralToolCallId(jsonString(toolCallId))
		if err != nil {
			return nil, err
		}
		members["tool_call_id"] = jsonStringValue(newId)
	}
	if toolCalls, ok := members["tool_calls"]; ok {
		rewritten, err := rewriteToolCallIds(toolCalls)
		if err != nil {
			return nil, err
		}
		members["tool_calls"] = rewritten
	}
	return common.Marshal(members)
}

// rewriteToolCallIds rewrites the id of every tool call in place.
func rewriteToolCallIds(toolCalls json.RawMessage) (json.RawMessage, error) {
	var calls []json.RawMessage
	if err := common.Unmarshal(toolCalls, &calls); err != nil {
		return nil, err
	}
	for i := range calls {
		var call map[string]json.RawMessage
		if err := common.Unmarshal(calls[i], &call); err != nil {
			return nil, err
		}
		id, ok := call["id"]
		if !ok {
			continue
		}
		newId, err := mistralToolCallId(jsonString(id))
		if err != nil {
			return nil, err
		}
		call["id"] = jsonStringValue(newId)
		rewritten, err := common.Marshal(call)
		if err != nil {
			return nil, err
		}
		calls[i] = rewritten
	}
	return common.Marshal(calls)
}

// toolResultText is a tool message content Mistral accepts: a string. Mistral
// rejects anything else with 422 "Input should be a valid string", and the empty
// result of a tool call arrives as an empty array or as content parts.
func toolResultText(content json.RawMessage) (json.RawMessage, error) {
	var text string
	if err := common.Unmarshal(content, &text); err == nil {
		return content, nil
	}
	var parts []json.RawMessage
	if err := common.Unmarshal(content, &parts); err != nil {
		return nil, err
	}
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		var member map[string]json.RawMessage
		if err := common.Unmarshal(part, &member); err != nil {
			continue
		}
		if jsonString(member["type"]) == "text" {
			texts = append(texts, jsonString(member["text"]))
		}
	}
	return jsonStringValue(strings.Join(texts, "")), nil
}

func hasToolCalls(toolCalls json.RawMessage) bool {
	var calls []json.RawMessage
	return common.Unmarshal(toolCalls, &calls) == nil && len(calls) > 0
}

// mistralToolCallId is the tool call id Mistral accepts: an id that is already
// nine alphanumerics is kept, and any other id is replaced by nine characters
// derived from it. Deriving the replacement keeps the request body identical
// across calls, so the upstream prompt cache still matches the conversation.
func mistralToolCallId(id string) (string, error) {
	if mistralToolCallIdRegexp.MatchString(id) {
		return id, nil
	}
	digest := sha256.Sum256([]byte(id))
	return hex.EncodeToString(digest[:])[:9], nil
}

func jsonStringValue(value string) json.RawMessage {
	encoded, err := common.Marshal(value)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return encoded
}

// jsonString reads a JSON member as the text it holds. A member of another type
// reads as the empty string.
func jsonString(member json.RawMessage) string {
	var text string
	if err := common.Unmarshal(member, &text); err != nil {
		return ""
	}
	return text
}

// mistralResponsesInput rewrites the "developer" role in a Responses API input
// to "system". Mistral accepts only system, user, assistant and tool roles on
// both the Chat Completions and the Responses endpoint.
func mistralResponsesInput(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return input
	}
	var items []map[string]any
	if err := common.Unmarshal(input, &items); err != nil {
		return input
	}
	changed := false
	for _, item := range items {
		if role, ok := item["role"].(string); ok && role == "developer" {
			item["role"] = "system"
			changed = true
		}
	}
	if !changed {
		return input
	}
	rewritten, err := common.Marshal(items)
	if err != nil {
		return input
	}
	return rewritten
}
