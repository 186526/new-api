package mistral

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// mistralVibeCallSource tags a request as Vibe Code traffic. Mistral decides
// which monthly allowance a request draws from by reading the request body: a
// key issued on chat.mistral.ai still consumes the API allowance, and is
// rejected with "API budget exhausted" once that allowance is used up, unless
// the body carries this tag. The tag is ordinary request metadata, so the server
// attributes the request to the Vibe allowance whatever key it was sent with.
const mistralVibeCallSource = "vibe_code"

// mistralVibeCallType labels the call a Vibe session makes. The Vibe CLI sends
// "main_call" for its top-level agent and "subagent" for the agents it spawns; a
// relayed request is always a top-level call.
const mistralVibeCallType = "main_call"

// ApplyVibeAttribution adds the metadata that makes the upstream attribute a
// request to the Vibe allowance. The server bills a request without the tag to
// the API allowance and rejects it once that allowance is spent, which is the
// state a Vibe Code plan reaches first. Vibe plan keys and API console keys share
// the mstrl_ prefix, so every plan key gets the tag; a key from another issuer
// keeps its body untouched. Fields the caller already set win, so a client that
// describes its own call keeps that description.
func ApplyVibeAttribution(request *dto.GeneralOpenAIRequest, apiKey string) (*dto.GeneralOpenAIRequest, error) {
	if !isMistralPlanAPIKey(apiKey) {
		return request, nil
	}
	if request == nil {
		return nil, nil
	}
	metadata := map[string]any{}
	if len(request.Metadata) > 0 {
		if err := common.Unmarshal(request.Metadata, &metadata); err != nil {
			return nil, err
		}
	}
	if _, exists := metadata["call_source"]; !exists {
		metadata["call_source"] = mistralVibeCallSource
	}
	if _, exists := metadata["call_type"]; !exists {
		metadata["call_type"] = mistralVibeCallType
	}
	encoded, err := common.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	request.Metadata = json.RawMessage(encoded)
	return request, nil
}

// isMistralPlanAPIKey reports whether the key is a Mistral plan key. Both the
// Vibe Code plan key and the API console key of a plan carry the same prefix.
func isMistralPlanAPIKey(apiKey string) bool {
	return strings.HasPrefix(strings.TrimSpace(apiKey), mistralVibeAPIKeyPrefix)
}
