package antigravity

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/google/uuid"
)

const (
	envelopeUserAgent   = "antigravity"
	requestTypeAgent    = "agent"
	requestTypeImageGen = "image_gen"
)

// buildRequestBody 把 Gemini generateContent 请求套进 Antigravity 的信封：
// {"model":..,"project":..,"userAgent":"antigravity","requestType":..,"requestId":..,"request":{..}}
func buildRequestBody(modelName, projectID string, geminiRequest any) (map[string]any, error) {
	inner, err := common.Marshal(geminiRequest)
	if err != nil {
		return nil, err
	}
	var request map[string]any
	if err := common.Unmarshal(inner, &request); err != nil {
		return nil, err
	}
	if request == nil {
		return nil, errors.New("antigravity channel: empty gemini request")
	}
	delete(request, "model")
	delete(request, "safetySettings")
	delete(request, "stream")
	request["sessionId"] = sessionID(modelName, request)

	requestType := requestTypeAgent
	if strings.Contains(strings.ToLower(modelName), "image") {
		requestType = requestTypeImageGen
	}

	envelope := map[string]any{
		"model":       modelName,
		"userAgent":   envelopeUserAgent,
		"requestType": requestType,
		"requestId":   "agent-" + uuid.NewString(),
		"request":     request,
	}
	if projectID != "" {
		envelope["project"] = projectID
	}
	if requestType == requestTypeImageGen {
		envelope["requestId"] = fmt.Sprintf("image_gen/%d/%s/12", common.GetTimestamp(), uuid.NewString())
	}
	return envelope, nil
}

// sessionID 由模型名与首条用户文本派生，同一段对话在多次请求间保持稳定。
func sessionID(modelName string, request map[string]any) string {
	seed := modelName
	if contents, ok := request["contents"].([]any); ok {
		for _, content := range contents {
			entry, ok := content.(map[string]any)
			if !ok || entry["role"] != "user" {
				continue
			}
			parts, ok := entry["parts"].([]any)
			if !ok {
				continue
			}
			for _, part := range parts {
				partMap, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := partMap["text"].(string); ok && text != "" {
					seed += "|" + text
					break
				}
			}
			break
		}
	}
	sum := sha256.Sum256([]byte(seed))
	value := int64(binary.BigEndian.Uint64(sum[:8]))
	if value < 0 {
		value = -value
	}
	return fmt.Sprintf("-%d", value)
}

func geminiRequestFromValue(value any) (*dto.GeminiChatRequest, error) {
	request, ok := value.(*dto.GeminiChatRequest)
	if !ok {
		return nil, fmt.Errorf("antigravity channel: expected Gemini generateContent request, got %T", value)
	}
	return request, nil
}
