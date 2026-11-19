package mistral

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestConvertOpenAIRequestAttributesRequestToVibeAllowance(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, vibeRelayInfo("mstrl_vibe_key"), &dto.GeneralOpenAIRequest{
		Model:    "deepseek-v4.1-flash",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() error: %v", err)
	}
	metadata := requestMetadata(t, converted)
	if got := metadata["call_source"]; got != "vibe_code" {
		t.Errorf("call_source = %v, want %q", got, "vibe_code")
	}
	if got := metadata["call_type"]; got != "main_call" {
		t.Errorf("call_type = %v, want %q", got, "main_call")
	}
}

func TestConvertOpenAIRequestKeepsClientMetadata(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, vibeRelayInfo("mstrl_vibe_key"), &dto.GeneralOpenAIRequest{
		Model:    "deepseek-v4.1-flash",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
		Metadata: json.RawMessage(`{"call_source":"api","trace":"abc"}`),
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() error: %v", err)
	}
	metadata := requestMetadata(t, converted)
	if got := metadata["call_source"]; got != "api" {
		t.Errorf("call_source = %v, want the client's %q", got, "api")
	}
	if got := metadata["trace"]; got != "abc" {
		t.Errorf("trace = %v, want %q", got, "abc")
	}
}

func TestConvertOpenAIRequestRejectsMalformedMetadata(t *testing.T) {
	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, vibeRelayInfo("mstrl_vibe_key"), &dto.GeneralOpenAIRequest{
		Model:    "deepseek-v4.1-flash",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
		Metadata: json.RawMessage(`"not an object"`),
	})
	if err == nil {
		t.Fatal("ConvertOpenAIRequest() accepted metadata that is not an object")
	}
}

func TestConvertOpenAIRequestLeavesOtherKeysUntagged(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, vibeRelayInfo("plainMistralKey"), &dto.GeneralOpenAIRequest{
		Model:    "mistral-large-latest",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
		Metadata: json.RawMessage(`{"trace":"abc"}`),
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() error: %v", err)
	}
	request := converted.(*dto.GeneralOpenAIRequest)
	if string(request.Metadata) != `{"trace":"abc"}` {
		t.Errorf("metadata = %s, want the client's own metadata untouched", request.Metadata)
	}
}

func vibeRelayInfo(apiKey string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: apiKey}}
}

func requestMetadata(t *testing.T, converted any) map[string]any {
	t.Helper()
	request, ok := converted.(*dto.GeneralOpenAIRequest)
	if !ok {
		t.Fatalf("converted request is %T, want *dto.GeneralOpenAIRequest", converted)
	}
	var metadata map[string]any
	if err := json.Unmarshal(request.Metadata, &metadata); err != nil {
		t.Fatalf("metadata %s is not a JSON object: %v", request.Metadata, err)
	}
	return metadata
}
