package antigravity

import (
	"testing"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestRequestEffortPrefersRelayInfo(t *testing.T) {
	request := &dto.GeminiChatRequest{
		GenerationConfig: dto.GeminiChatGenerationConfig{
			ThinkingConfig: &dto.GeminiThinkingConfig{ThinkingLevel: "high"},
		},
	}

	if got := requestEffort(nil, request); got != "high" {
		t.Errorf("requestEffort without RelayInfo = %q, want high", got)
	}

	info := &common.RelayInfo{ReasoningEffort: "low"}
	if got := requestEffort(info, request); got != "low" {
		t.Errorf("requestEffort = %q, want the RelayInfo value low", got)
	}

	if got := requestEffort(&common.RelayInfo{}, &dto.GeminiChatRequest{}); got != "" {
		t.Errorf("requestEffort = %q, want empty", got)
	}
}
