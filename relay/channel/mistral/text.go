package mistral

import (
	"encoding/json"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
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

func requestOpenAI2Mistral(request *dto.GeneralOpenAIRequest) *dto.GeneralOpenAIRequest {
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

		mediaMessages := message.ParseContent()
		if message.Role == "assistant" && message.ToolCalls != nil && message.Content == "" {
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
	}
	if request.MaxTokens != nil || request.MaxCompletionTokens != nil {
		maxTokens := request.GetMaxTokens()
		out.MaxTokens = &maxTokens
	}
	return out
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
