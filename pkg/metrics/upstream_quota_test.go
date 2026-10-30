package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gaugesWithPrefix returns the gauge values whose label set contains prefix.
func gaugesWithPrefix(t *testing.T, name, prefix string) map[string]float64 {
	t.Helper()
	metrics, err := registry.Gather()
	require.NoError(t, err)
	values := make(map[string]float64)
	for _, metric := range metrics {
		if metric.GetName() != name {
			continue
		}
		for _, sample := range metric.GetMetric() {
			key := ""
			for _, label := range sample.GetLabel() {
				key += label.GetName() + "=" + label.GetValue() + ","
			}
			if strings.HasPrefix(key, prefix) {
				values[key] = sample.GetGauge().GetValue()
			}
		}
	}
	return values
}

func TestObserveUpstreamQuotaPublishesWindowsAndRefreshTime(t *testing.T) {
	channel := "Google Antigravity window test"
	reset := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	refresh := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	ObserveUpstreamQuota(UpstreamQuota{
		Channel: channel,
		Windows: []QuotaWindow{
			{Scope: "Gemini Models", Window: "5h", RemainingRatio: 1, ResetAt: reset},
			{Scope: "Gemini Models", Window: "weekly", RemainingRatio: 0.9681, ResetAt: reset},
		},
		RefreshedAt: refresh,
	})

	ratios := gaugesWithPrefix(t, "newapi_upstream_quota_remaining_ratio", "channel="+channel+",")
	require.Len(t, ratios, 2)
	assert.Equal(t, 1.0, ratios["channel="+channel+",scope=Gemini Models,window=5h,"])
	assert.InDelta(t, 0.9681, ratios["channel="+channel+",scope=Gemini Models,window=weekly,"], 1e-9)

	resets := gaugesWithPrefix(t, "newapi_upstream_quota_reset_timestamp_seconds", "channel="+channel+",")
	assert.Equal(t, float64(reset.Unix()), resets["channel="+channel+",scope=Gemini Models,window=5h,"])

	refreshes := gaugesWithPrefix(t, "newapi_upstream_quota_refresh_timestamp_seconds", "channel="+channel+",")
	assert.Equal(t, float64(refresh.Unix()), refreshes["channel="+channel+","])
}

func TestObserveUpstreamQuotaKeepsTheWorstBucketPerWindow(t *testing.T) {
	channel := "Google Antigravity worst test"

	ObserveUpstreamQuota(UpstreamQuota{
		Channel: channel,
		Windows: []QuotaWindow{
			{Scope: "gemini-4-argon", Window: "default", RemainingRatio: 0.8},
			{Scope: "gemini-4-argon", Window: "default", RemainingRatio: 0.2},
			{Scope: "claude-opus-5-5", Window: "default", RemainingRatio: 0.6},
		},
	})

	ratios := gaugesWithPrefix(t, "newapi_upstream_quota_remaining_ratio", "channel="+channel+",")
	require.Len(t, ratios, 2)
	assert.InDelta(t, 0.2, ratios["channel="+channel+",scope=gemini-4-argon,window=default,"], 1e-9)
	assert.InDelta(t, 0.6, ratios["channel="+channel+",scope=claude-opus-5-5,window=default,"], 1e-9)
}

func TestObserveUpstreamQuotaSkipsIncompleteWindows(t *testing.T) {
	channel := "Google Antigravity incomplete test"

	ObserveUpstreamQuota(UpstreamQuota{
		Channel: channel,
		Windows: []QuotaWindow{{Scope: "", Window: "5h", RemainingRatio: 1}},
	})
	ObserveUpstreamQuota(UpstreamQuota{
		Windows: []QuotaWindow{{Scope: "x", Window: "5h", RemainingRatio: 1}},
	})

	assert.Empty(t, gaugesWithPrefix(t, "newapi_upstream_quota_remaining_ratio", "channel="+channel+","))
	assert.Empty(t, gaugesWithPrefix(t, "newapi_upstream_quota_refresh_timestamp_seconds", "channel="+channel+","))
}
