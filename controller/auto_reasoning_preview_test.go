package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAutoReasoningPreviewUsesDraftAndChannelOverride(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	policy, err := hostreasoning.ParseAutoEffortOption(`{"enabled":true,"scale_metric":"input_chars"}`)
	require.NoError(t, err)
	const sample = "试算样本"
	draft := autoReasoningDraft{Protocol: "responses", Model: "alias", Group: "default", Text: new(sample)}
	request, format, mode, path, err := draft.request()
	require.NoError(t, err)
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, ModelMapping: new(`{"alias":"gpt-5"}`), ParamOverride: new(`{"reasoning":{"effort":"high"}}`)}
	before := hostreasoning.CurrentAutoEffort()
	info, fields, err := previewReasoningConversion(ctx, draft, policy, channel, request, format, mode, path)
	require.NoError(t, err)
	require.Equal(t, 4, info.AutoReasoning.Scale)
	require.Equal(t, "applied", info.AutoReasoning.Reason)
	require.Equal(t, "low", info.AutoReasoning.Effort)
	require.Equal(t, map[string]any{"reasoning.effort": "high"}, fields)
	require.Same(t, before, hostreasoning.CurrentAutoEffort())
}

func TestAutoReasoningPreviewFiltersNonReasoningData(t *testing.T) {
	fields := previewReasoningFields([]byte(`{"reasoning":{"effort":"not an effort"},"thinking":{"type":"enabled","budget_tokens":128},"input":"Sample text"}`))
	require.Equal(t, map[string]any{"thinking.type": "enabled", "thinking.budget_tokens": float64(128)}, fields)
}
