package metrics

import (
	"sort"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// samplesForChannel returns the gauge values of one metric whose channel label
// is the given name, keyed by "scope|window" (empty for a metric without those
// labels).
func samplesForChannel(t *testing.T, metricName, channel string) map[string]float64 {
	t.Helper()
	gathered, err := registry.Gather()
	require.NoError(t, err)
	samples := make(map[string]float64)
	for _, family := range gathered {
		if family.GetName() != metricName {
			continue
		}
		for _, sample := range family.GetMetric() {
			var name, scope, window string
			for _, label := range sample.GetLabel() {
				switch label.GetName() {
				case "channel":
					name = label.GetValue()
				case "scope":
					scope = label.GetValue()
				case "window":
					window = label.GetValue()
				}
			}
			if name == channel {
				samples[scope+"|"+window] = sample.GetGauge().GetValue()
			}
		}
	}
	return samples
}

// configuredLabel returns the channel_configured value of the one series of a
// metric whose channel label is the given name and whose scope and window match,
// so a test can assert both the label and the untouched figure.
func configuredLabel(t *testing.T, metricName, channel, scope, window string) (string, float64, bool) {
	t.Helper()
	gathered, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range gathered {
		if family.GetName() != metricName {
			continue
		}
		for _, sample := range family.GetMetric() {
			var name, sampleScope, sampleWindow, configured string
			for _, label := range sample.GetLabel() {
				switch label.GetName() {
				case "channel":
					name = label.GetValue()
				case "scope":
					sampleScope = label.GetValue()
				case "window":
					sampleWindow = label.GetValue()
				case "channel_configured":
					configured = label.GetValue()
				}
			}
			if name == channel && sampleScope == scope && sampleWindow == window {
				return configured, sample.GetGauge().GetValue(), true
			}
		}
	}
	return "", 0, false
}

// labelNames returns the label names of the first sample of one metric, sorted.
// The mission requires the existing label set to stay unchanged apart from the
// new label, so the test names the whole set rather than only the new member.
func labelNames(t *testing.T, name string) []string {
	t.Helper()
	gathered, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range gathered {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.GetMetric() {
			names := make([]string, 0, len(sample.GetLabel()))
			for _, label := range sample.GetLabel() {
				names = append(names, label.GetName())
			}
			sort.Strings(names)
			return names
		}
	}
	return nil
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

	ratios := samplesForChannel(t, "newapi_upstream_quota_remaining_ratio", channel)
	require.Len(t, ratios, 2)
	assert.Equal(t, 1.0, ratios["Gemini Models|5h"])
	assert.InDelta(t, 0.9681, ratios["Gemini Models|weekly"], 1e-9)

	resets := samplesForChannel(t, "newapi_upstream_quota_reset_timestamp_seconds", channel)
	assert.Equal(t, float64(reset.Unix()), resets["Gemini Models|5h"])

	refreshes := samplesForChannel(t, "newapi_upstream_quota_refresh_timestamp_seconds", channel)
	assert.Equal(t, float64(refresh.Unix()), refreshes["|"])
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

	ratios := samplesForChannel(t, "newapi_upstream_quota_remaining_ratio", channel)
	require.Len(t, ratios, 2)
	assert.InDelta(t, 0.2, ratios["gemini-4-argon|default"], 1e-9)
	assert.InDelta(t, 0.6, ratios["claude-opus-5-5|default"], 1e-9)
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

	assert.Empty(t, samplesForChannel(t, "newapi_upstream_quota_remaining_ratio", channel))
	assert.Empty(t, samplesForChannel(t, "newapi_upstream_quota_refresh_timestamp_seconds", channel))
}

// TestObserveUpstreamQuotaLabelsTheAllowanceOfAnAccountWithoutAChannel requires
// the allowance of a configured account that no channel carries to publish
// channel_configured="false" and the allowance of a live channel to publish
// "true", so a dashboard can keep the orphan series out of the channel rows.
func TestObserveUpstreamQuotaLabelsTheAllowanceOfAnAccountWithoutAChannel(t *testing.T) {
	orphan := "Mistral Vibe (orphan account test)"
	live := "Mistral Vibe (live channel test)"
	refresh := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	windows := []QuotaWindow{
		{Scope: "api", Window: "monthly", RemainingRatio: 0.25, ResetAt: reset},
		{Scope: "vibe", Window: "monthly", RemainingRatio: 0.75, ResetAt: reset},
	}

	ObserveUpstreamQuota(UpstreamQuota{
		Channel:         orphan,
		Windows:         windows,
		RefreshedAt:     refresh,
		ChannelOrphaned: true,
	})
	ObserveUpstreamQuota(UpstreamQuota{Channel: live, Windows: windows, RefreshedAt: refresh})

	configured, ratio, ok := configuredLabel(t, "newapi_upstream_quota_remaining_ratio", orphan, "vibe", "monthly")
	require.True(t, ok, "the orphan keeps its series so the figure stays readable")
	assert.Equal(t, "false", configured)
	assert.InDelta(t, 0.75, ratio, 1e-9)

	configured, ratio, ok = configuredLabel(t, "newapi_upstream_quota_remaining_ratio", live, "vibe", "monthly")
	require.True(t, ok)
	assert.Equal(t, "true", configured)
	assert.InDelta(t, 0.75, ratio, 1e-9)

	configured, resetAt, ok := configuredLabel(t, "newapi_upstream_quota_reset_timestamp_seconds", orphan, "api", "monthly")
	require.True(t, ok)
	assert.Equal(t, "false", configured)
	assert.Equal(t, float64(reset.Unix()), resetAt)

	configured, refreshedAt, ok := configuredLabel(t, "newapi_upstream_quota_refresh_timestamp_seconds", orphan, "", "")
	require.True(t, ok)
	assert.Equal(t, "false", configured)
	assert.Equal(t, float64(refresh.Unix()), refreshedAt)

	configured, _, ok = configuredLabel(t, "newapi_upstream_quota_refresh_timestamp_seconds", live, "", "")
	require.True(t, ok)
	assert.Equal(t, "true", configured)
}

// TestUpstreamQuotaKeepsItsLabelsAndAddsOnlyTheConfiguredOne pins the label set
// of the three allowance metrics, so the numeric meaning of an existing series
// is the one it had before the label: the same channel, scope and window values
// key the same figure, and channel_configured is the only new member.
func TestUpstreamQuotaKeepsItsLabelsAndAddsOnlyTheConfiguredOne(t *testing.T) {
	ObserveUpstreamQuota(UpstreamQuota{
		Channel:     "label-set probe live",
		Windows:     []QuotaWindow{{Scope: "api", Window: "monthly", RemainingRatio: 0.5}},
		RefreshedAt: time.Unix(1_700_000_000, 0),
	})
	ObserveUpstreamQuota(UpstreamQuota{
		Channel:         "label-set probe orphan",
		Windows:         []QuotaWindow{{Scope: "api", Window: "monthly", RemainingRatio: 0.5}},
		RefreshedAt:     time.Unix(1_700_000_000, 0),
		ChannelOrphaned: true,
	})

	assert.Equal(t, []string{"channel", "channel_configured", "scope", "window"},
		labelNames(t, "newapi_upstream_quota_remaining_ratio"))
	assert.Equal(t, []string{"channel", "channel_configured", "scope", "window"},
		labelNames(t, "newapi_upstream_quota_reset_timestamp_seconds"))
	assert.Equal(t, []string{"channel", "channel_configured"},
		labelNames(t, "newapi_upstream_quota_refresh_timestamp_seconds"))
}

// TestObserveChannelBalanceKeepsItsLabelsAndAddsOnlyTheConfiguredOne pins the
// balance label set. Every balance series is published from a channel that
// exists, so the new label is always "true"; the assertion on the figure keeps
// the numeric meaning of the series unchanged.
func TestObserveChannelBalanceKeepsItsLabelsAndAddsOnlyTheConfiguredOne(t *testing.T) {
	ObserveChannelBalanceUSD("balance-label probe", 0, 12.5)

	assert.Equal(t, 12.5, testutil.ToFloat64(channelBalanceUSD.WithLabelValues("balance-label probe", channelConfiguredValue(true))))
	assert.Equal(t, []string{"channel", "channel_configured"}, labelNames(t, "newapi_channel_balance_usd"))
	configured, balance, ok := configuredLabel(t, "newapi_channel_balance_usd", "balance-label probe", "", "")
	require.True(t, ok)
	assert.Equal(t, "true", configured)
	assert.InDelta(t, 12.5, balance, 1e-9)
}
