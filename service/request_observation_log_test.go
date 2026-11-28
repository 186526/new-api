package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestObservationLog(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ToolExecution:    &relaycommon.ToolExecutionMeta{Contract: "tools/search", Provider: "codex", Outcome: "success", CacheHit: true, Refund: "requested"},
		ImageCompression: &relaycommon.ImageCompressionMeta{Processed: 1, Reason: "not-loggable"},
		AutoReasoning:    &relaycommon.AutoReasoningMeta{Reason: "applied", Effort: "high", Conflict: "capped", PolicyEffort: "high"},
		RequestRedaction: &relaycommon.RequestRedactionMeta{Matches: 3, Categories: 2},
	}
	other := model.NewLogOther()
	appendRequestObservation(info, other)
	fields := other.Snapshot()
	public, ok := fields["tool_execution"].(relaycommon.ToolExecutionMeta)
	require.True(t, ok)
	assert.Empty(t, public.Provider)
	assert.Equal(t, "requested", public.Refund)
	admin, ok := fields["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "codex", admin["tool_execution"].(relaycommon.ToolExecutionMeta).Provider)
	assert.NotContains(t, other.JSONString(), "not-loggable")
	assert.Contains(t, other.JSONString(), `"matches":3`)
	assert.Contains(t, other.JSONString(), `"conflict_resolution":"capped"`)
	assert.Contains(t, other.JSONString(), `"policy_effort":"high"`)
}
