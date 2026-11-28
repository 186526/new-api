package helper

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/stretchr/testify/require"
)

func TestAutoReasoningEvaluatesActualGroupForEachAttempt(t *testing.T) {
	rules, err := hostreasoning.ParseAutoEffortOption(`{"enabled":true,"groups":["agent"]}`)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		OriginModelName: "alias",
		UsingGroup:      "default",
		Request:         &dto.GeneralOpenAIRequest{Model: "alias"},
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-5"},
		AutoReasoning:   &relaycommon.AutoReasoningMeta{Rules: rules, Scale: 2400},
	}
	first := &dto.GeneralOpenAIRequest{}
	mustApplyReasoningModelSuffix(t, info, first)
	require.Equal(t, "group_excluded", info.AutoReasoning.Reason)
	require.Empty(t, first.ReasoningEffort)

	info.UsingGroup = "agent"
	second := &dto.GeneralOpenAIRequest{}
	mustApplyReasoningModelSuffix(t, info, second)
	require.Equal(t, "applied", info.AutoReasoning.Reason)
	require.Equal(t, "medium", second.ReasoningEffort)
	require.Equal(t, "auto_scale", info.ReasoningConversion.Source)
	require.Empty(t, info.Request.(*dto.GeneralOpenAIRequest).ReasoningEffort)
}

func TestAutoReasoningScaleCountsUnicodeAndRepeatedImages(t *testing.T) {
	meta := &types.TokenCountMeta{CombineText: "中文🧪"}
	require.Equal(t, 3, estimateAutoReasoningScale("input_chars", "gpt-5", meta, 2))
	withoutImages := estimateAutoReasoningScale("estimated_tokens", "gpt-5", meta, 0)
	require.Equal(t, withoutImages+1700, estimateAutoReasoningScale("estimated_tokens", "gpt-5", meta, 2))
}

func TestAutoReasoningDiagnosticsPreserveExplicitDisableAcrossProtocols(t *testing.T) {
	rules, err := hostreasoning.ParseAutoEffortOption(`{"enabled":true,"conflict_policy":"always_override"}`)
	require.NoError(t, err)
	for _, test := range []struct {
		format  types.RelayFormat
		request dto.Request
		wire    string
	}{
		{types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{}, `{"reasoning_effort":"none"}`},
		{types.RelayFormatOpenAIResponses, &dto.OpenAIResponsesRequest{}, `{"reasoning":{"effort":"none"}}`},
		{types.RelayFormatClaude, &dto.ClaudeRequest{}, `{"thinking":{"type":"disabled"}}`},
		{types.RelayFormatGemini, &dto.GeminiChatRequest{}, `{"generationConfig":{"thinkingConfig":{"thinkingBudget":0}}}`},
	} {
		t.Run(string(test.format), func(t *testing.T) {
			require.NoError(t, json.Unmarshal([]byte(test.wire), test.request))
			info := &relaycommon.RelayInfo{
				RelayFormat: test.format, Request: test.request, OriginModelName: "gpt-5",
				ChannelMeta:   &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-5"},
				AutoReasoning: &relaycommon.AutoReasoningMeta{Rules: rules, Scale: 2000},
			}
			mustApplyReasoningModelSuffix(t, info)
			require.Nil(t, info.ReasoningConversion)
			require.Equal(t, "explicit_disable", info.AutoReasoning.Reason)
			diagnostics := info.ConversionDiagnostics()
			require.Len(t, diagnostics, 1)
			require.Equal(t, "auto_reasoning_explicit_disable", diagnostics[0].Code)
			require.Equal(t, test.format, diagnostics[0].From)
			require.Equal(t, "scale=2000 tier=2 target_effort=medium resolved_effort=none", diagnostics[0].Message)
		})
	}
}

func TestAutoReasoningCapsOutboundChat(t *testing.T) {
	rules, err := hostreasoning.ParseAutoEffortOption(`{"enabled":true,"conflict_policy":"cap_to_auto"}`)
	require.NoError(t, err)
	request := &dto.GeneralOpenAIRequest{Model: "gpt-5", ReasoningEffort: "high"}
	outbound := *request
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI, Request: request, OriginModelName: "gpt-5",
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, UpstreamModelName: "gpt-5"},
		AutoReasoning: &relaycommon.AutoReasoningMeta{Rules: rules, Scale: 1500},
	}
	mustApplyReasoningModelSuffix(t, info, &outbound)
	require.Equal(t, "low", outbound.ReasoningEffort)
	require.Equal(t, "high", request.ReasoningEffort)
	diagnostics := info.ConversionDiagnostics()
	require.Len(t, diagnostics, 1)
	require.Equal(t, "auto_reasoning_capped", diagnostics[0].Code)
	require.Equal(t, "scale=1500 tier=1 target_effort=low resolved_effort=low", diagnostics[0].Message)
}
