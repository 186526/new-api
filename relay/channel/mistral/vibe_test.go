package mistral

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestConvertOpenAIRequestAttributesRequestToVibeAllowance(t *testing.T) {
	metadata := requestMetadata(t, convertedWithSelectedKey(t, vibeRelayInfo("mstrl_vibe_key")))
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
	members := bodyMembers(t, converted)
	if string(members["metadata"]) != `{"trace":"abc"}` {
		t.Errorf("metadata = %s, want the client's own metadata untouched", members["metadata"])
	}
}

func vibeRelayInfo(apiKey string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ApiKey: apiKey}}
}

func TestConvertOpenAIRequestSkipsAttributionWhenTheChannelDisablesIt(t *testing.T) {
	off := false
	info := vibeRelayInfo("mstrl_vibe_key")
	info.ChannelSetting.MistralVibeAttribution = &off

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, &dto.GeneralOpenAIRequest{
		Model:    "deepseek-v4.1-flash",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() error: %v", err)
	}
	members := bodyMembers(t, converted)
	if metadata, ok := members["metadata"]; ok {
		t.Errorf("metadata = %s, want none for a channel that keeps the API allowance", metadata)
	}
}

// selectedKeyBody carries the body a client sends alongside its key, so the
// attribution is asserted on the body the conversion forwards.
const selectedKeyBody = `{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hello"}],"prompt_cache_key":"a1b2c3","seed":42}`

func convertedWithSelectedKey(t *testing.T, info *relaycommon.RelayInfo) []byte {
	t.Helper()
	converted, err := (&Adaptor{}).ConvertOpenAIRequest(clientBodyForTest(t, selectedKeyBody), info, &dto.GeneralOpenAIRequest{
		Model:    "deepseek-v4.1-flash",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest() error: %v", err)
	}
	body, ok := converted.([]byte)
	if !ok {
		t.Fatalf("converted request is %T, want the forwarded body", converted)
	}
	return body
}

func bodyMembers(t *testing.T, converted any) map[string]json.RawMessage {
	t.Helper()
	body, ok := converted.([]byte)
	if !ok {
		t.Fatalf("converted request is %T, want the forwarded body", converted)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("converted body %s is not a JSON object: %v", body, err)
	}
	return members
}

func requestMetadata(t *testing.T, converted any) map[string]any {
	t.Helper()
	members := bodyMembers(t, converted)
	var metadata map[string]any
	if err := json.Unmarshal(members["metadata"], &metadata); err != nil {
		t.Fatalf("metadata %s is not a JSON object: %v", members["metadata"], err)
	}
	return metadata
}
