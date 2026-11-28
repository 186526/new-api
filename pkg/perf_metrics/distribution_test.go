package perfmetrics

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/stretchr/testify/require"
)

func TestObservationDistributionUsesControlledDimensions(t *testing.T) {
	rules, err := reasoning.ParseAutoEffortOption(`{}`)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		ToolExecution: &relaycommon.ToolExecutionMeta{Contract: "tools/fetch", Provider: "https://sensitive.example", Outcome: "sensitive_error"},
		AutoReasoning: &relaycommon.AutoReasoningMeta{Rules: rules, Scale: 5000, Reason: "applied", PolicyEffort: "medium", Effort: "medium"},
	}
	entries := observationDistribution(info.RequestObservation())
	require.Equal(t, []model.PerfMetricDistributionEntry{
		{PerfMetricDimension: model.PerfMetricDimension{Kind: "tool", Name: "tools/fetch", Variant: "unknown", Outcome: "unknown"}, Count: 1},
		{PerfMetricDimension: model.PerfMetricDimension{Kind: "reasoning", Name: "2", Variant: "medium", Outcome: "applied"}, Count: 1},
	}, entries)

	bucket := &atomicBucket{}
	bucket.add(Sample{distribution: entries, observationOnly: true})
	first := bucket.drain()
	bucket.add(Sample{distribution: entries, observationOnly: true})
	bucket.addCounters(first)
	second := bucket.drain()
	require.Zero(t, second.requestCount)
	for _, entry := range entries {
		require.Equal(t, int64(2), second.distribution[entry.PerfMetricDimension].Count)
	}
	require.Empty(t, bucket.drain().distribution)
}
