package mistral

import (
	"encoding/json"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

var mistralToolCallIdRegexp = regexp.MustCompile("^[a-zA-Z0-9]{9}$")

// mistralRole maps OpenAI's "developer" role onto Mistral's "system" role.
// Mistral rejects any other role with HTTP 422.
func mistralRole(role string) string {
	if role == "developer" {
		return "system"
	}
	return role
}

func requestOpenAI2Mistral(c *gin.Context, request *dto.GeneralOpenAIRequest) any {
	messages := make([]dto.Message, 0, len(request.Messages))
	idMap := make(map[string]string)
	for _, message := range request.Messages {
		// 1. tool_calls.id
		toolCalls := message.ParseToolCalls()
		if toolCalls != nil {
			for i := range toolCalls {
				if !mistralToolCallIdRegexp.MatchString(toolCalls[i].ID) {
					if newId, ok := idMap[toolCalls[i].ID]; ok {
						toolCalls[i].ID = newId
					} else {
						newId, err := common.GenerateRandomCharsKey(9)
						if err == nil {
							idMap[toolCalls[i].ID] = newId
							toolCalls[i].ID = newId
						}
					}
				}
			}
			message.SetToolCalls(toolCalls)
		}

		// 2. tool_call_id
		if message.ToolCallId != "" {
			if newId, ok := idMap[message.ToolCallId]; ok {
				message.ToolCallId = newId
			} else {
				if !mistralToolCallIdRegexp.MatchString(message.ToolCallId) {
					newId, err := common.GenerateRandomCharsKey(9)
					if err == nil {
						idMap[message.ToolCallId] = newId
						message.ToolCallId = newId
					}
				}
			}
		}

		// Mistral accepts only a string as a tool result, so a tool message keeps
		// its content as text. The empty result of a tool call is the common
		// case: turned into content parts it becomes [{"type":"text"}] and the
		// upstream rejects it with 422.
		if message.Role == "tool" {
			messages = append(messages, dto.Message{
				Role:       "tool",
				Content:    message.StringContent(),
				ToolCallId: message.ToolCallId,
			})
			continue
		}

		mediaMessages := message.ParseContent()
		if message.Role == "assistant" && message.ToolCalls != nil && message.StringContent() == "" {
			mediaMessages = []dto.MediaContent{}
		}
		for j, mediaMessage := range mediaMessages {
			if mediaMessage.Type == dto.ContentTypeImageURL {
				imageUrl := mediaMessage.GetImageMedia()
				mediaMessage.ImageUrl = imageUrl.Url
				mediaMessages[j] = mediaMessage
			}
		}
		message.SetMediaContent(mediaMessages)
		messages = append(messages, dto.Message{
			Role:       mistralRole(message.Role),
			Content:    message.Content,
			ToolCalls:  message.ToolCalls,
			ToolCallId: message.ToolCallId,
		})
	}
	out := &dto.GeneralOpenAIRequest{
		Model:       request.Model,
		Stream:      request.Stream,
		Messages:    messages,
		Temperature: request.Temperature,
		TopP:        request.TopP,
		Tools:       request.Tools,
		ToolChoice:  request.ToolChoice,
		// The metadata carries the allowance tag, so the rebuilt request keeps it.
		Metadata: request.Metadata,
	}
	if request.MaxTokens != nil || request.MaxCompletionTokens != nil {
		maxTokens := request.GetMaxTokens()
		out.MaxTokens = &maxTokens
	}
	return carryUnmodelledFields(c, request, out)
}

// carryUnmodelledFields copies every top-level member the conversion above
// does not rebuild, so a field the client sends but this struct does not
// declare (a sampler extension, for example) still reaches Mistral. Both are
// marshalled through their JSON tags, so the typed fields and the raw ones
// share one member list and a carried member never duplicates a typed one.
func carryUnmodelledFields(c *gin.Context, request *dto.GeneralOpenAIRequest, out *dto.GeneralOpenAIRequest) any {
	if c == nil || c.Request == nil {
		return out
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return out
	}
	raw, err := storage.Bytes()
	if err != nil {
		return out
	}
	var original map[string]json.RawMessage
	if err := common.Unmarshal(raw, &original); err != nil {
		return out
	}
	converted, err := common.Marshal(request)
	if err != nil {
		return out
	}
	var modelled map[string]json.RawMessage
	if err := common.Unmarshal(converted, &modelled); err != nil {
		return out
	}
	unmodelled := make(map[string]json.RawMessage)
	for name, value := range original {
		if _, ok := modelled[name]; !ok {
			unmodelled[name] = value
		}
	}
	if len(unmodelled) == 0 {
		return out
	}
	return &carriedRequest{GeneralOpenAIRequest: out, extra: unmodelled}
}

// carriedRequest marshals the converted request together with the members the
// conversion did not model.
type carriedRequest struct {
	*dto.GeneralOpenAIRequest
	extra map[string]json.RawMessage
}

func (r *carriedRequest) MarshalJSON() ([]byte, error) {
	modelled, err := common.Marshal(r.GeneralOpenAIRequest)
	if err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if err := common.Unmarshal(modelled, &members); err != nil {
		return nil, err
	}
	for name, value := range r.extra {
		if _, exists := members[name]; !exists {
			members[name] = value
		}
	}
	return common.Marshal(members)
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
