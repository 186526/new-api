package mistral

import (
	"encoding/json"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestConvertOpenAIResponsesRequestForwardsTheRequest(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4-1-flash-1017"}}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, info, dto.OpenAIResponsesRequest{
		Model:        "deepseek-v4.1-flash",
		Instructions: json.RawMessage(`"answer briefly"`),
		Input:        json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]`),
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIResponsesRequest() error: %v", err)
	}
	responsesRequest, ok := converted.(dto.OpenAIResponsesRequest)
	if !ok {
		t.Fatalf("converted request has type %T, want dto.OpenAIResponsesRequest", converted)
	}
	if responsesRequest.Model != "deepseek-v4-1-flash-1017" {
		t.Errorf("model = %q, want the mapped upstream model", responsesRequest.Model)
	}
	if string(responsesRequest.Instructions) != `"answer briefly"` {
		t.Errorf("instructions = %s, want them forwarded unchanged", responsesRequest.Instructions)
	}
	if string(responsesRequest.Input) != `[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]` {
		t.Errorf("input = %s, want the user item forwarded unchanged", responsesRequest.Input)
	}
}

func TestConvertOpenAIResponsesRequestMapsDeveloperRoleToSystem(t *testing.T) {
	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, &relaycommon.RelayInfo{}, dto.OpenAIResponsesRequest{
		Model: "deepseek-v4.1-flash",
		Input: json.RawMessage(`[{"role":"developer","content":[{"type":"input_text","text":"rules"}]}]`),
	})
	if err != nil {
		t.Fatalf("ConvertOpenAIResponsesRequest() error: %v", err)
	}
	responsesRequest := converted.(dto.OpenAIResponsesRequest)

	var items []struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(responsesRequest.Input, &items); err != nil {
		t.Fatalf("unmarshal rewritten input: %v", err)
	}
	if len(items) != 1 || items[0].Role != "system" {
		t.Errorf("input = %s, want the developer role rewritten to system", responsesRequest.Input)
	}
}

func TestMistralResponsesInputLeavesStringInputAlone(t *testing.T) {
	input := json.RawMessage(`"hello"`)

	if got := mistralResponsesInput(input); string(got) != string(input) {
		t.Errorf("input = %s, want %s", got, input)
	}
}
