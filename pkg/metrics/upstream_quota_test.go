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
// A family with no samples fails here instead of returning nil, because a nil
// slice compared against the expected set reads as a mismatch only by accident:
// the metric may simply not have been published yet.
func labelNames(t *testing.T, name string) []string {
	t.Helper()
	gathered, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range gathered {
		if family.GetName() != name {
			continue
		}
		require.NotEmpty(t, family.GetMetric(), "metric %s was not published, so its label set cannot be read", name)
		sample := family.GetMetric()[0]
		names := make([]string, 0, len(sample.GetLabel()))
		for _, label := range sample.GetLabel() {
			names = append(names, label.GetName())
		}
		require.NotEmpty(t, names, "metric %s was published without labels", name)
		sort.Strings(names)
		return names
	}
	require.FailNow(t, "metric family not found in the registry", "metric %s", name)
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

// TestObserveUpstreamQuotaKeepsTheLastFigureWhenATickPublishesNoWindow requires a
// tick with nothing publishable to leave the previous figure alone. The deletion
// runs before the writes, so without the guard a tick that reports no usable
// window would erase the series of the tick before it and leave the channel with
// no allowance at all, which reads as a channel that vanished rather than one
// whose lookup returned nothing this time.
func TestObserveUpstreamQuotaKeepsTheLastFigureWhenATickPublishesNoWindow(t *testing.T) {
	channel := "Mistral Vibe (nothing publishable test)"
	reset := time.Unix(1_700_300_000, 0)

	ObserveUpstreamQuota(UpstreamQuota{
		Channel: channel,
		Windows: []QuotaWindow{{Scope: "vibe", Window: "monthly", RemainingRatio: 0.4, ResetAt: reset}},
	})
	// A window without a scope is skipped, so this tick has nothing to publish.
	ObserveUpstreamQuota(UpstreamQuota{
		Channel:         channel,
		Windows:         []QuotaWindow{{Scope: "", Window: "monthly", RemainingRatio: 0.1}},
		ChannelOrphaned: true,
	})

	counts := seriesCountByConfigured(t, "newapi_upstream_quota_remaining_ratio", channel)
	assert.Equal(t, 1, counts[channelConfiguredTrue], "the last published figure must survive a tick that reports nothing")
	assert.Equal(t, 0, counts[channelConfiguredFalse])
	assert.InDelta(t, 0.4, samplesForChannel(t, "newapi_upstream_quota_remaining_ratio", channel)["vibe|monthly"], 1e-9)
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
// key the same figure, and channel_configured is the only new member. The
// windows carry a reset time because the reset gauge is published only for a
// window that has one, so the test must not depend on another test having
// published that family first.
func TestUpstreamQuotaKeepsItsLabelsAndAddsOnlyTheConfiguredOne(t *testing.T) {
	reset := time.Unix(1_700_100_000, 0)
	ObserveUpstreamQuota(UpstreamQuota{
		Channel: "label-set probe live",
		Windows: []QuotaWindow{{
			Scope: "api", Window: "monthly", RemainingRatio: 0.5, ResetAt: reset,
		}},
		RefreshedAt: time.Unix(1_700_000_000, 0),
	})
	ObserveUpstreamQuota(UpstreamQuota{
		Channel: "label-set probe orphan",
		Windows: []QuotaWindow{{
			Scope: "api", Window: "monthly", RemainingRatio: 0.5, ResetAt: reset,
		}},
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

// TestObserveUpstreamQuotaDropsTheSeriesOfTheFlippedConfiguredLabel requires a
// flip to leave only the new series behind. A GaugeVec never deletes a series,
// so without the deletion a channel that comes back keeps its orphan series
// forever, and a dashboard filtering on channel_configured would keep excluding
// a channel that exists.
func TestObserveUpstreamQuotaDropsTheSeriesOfTheFlippedConfiguredLabel(t *testing.T) {
	channel := "Mistral Vibe (flipped label test)"
	reset := time.Unix(1_700_200_000, 0)
	refresh := time.Unix(1_700_000_000, 0)
	window := func() []QuotaWindow {
		return []QuotaWindow{{Scope: "monthly", Window: "monthly", RemainingRatio: 0.5, ResetAt: reset}}
	}

	// The account goes orphan, then its channel comes back.
	ObserveUpstreamQuota(UpstreamQuota{
		Channel: channel, Windows: window(), RefreshedAt: refresh, ChannelOrphaned: true,
	})
	ObserveUpstreamQuota(UpstreamQuota{
		Channel: channel, Windows: window(), RefreshedAt: refresh,
	})

	counts := seriesCountByConfigured(t, "newapi_upstream_quota_remaining_ratio", channel)
	require.Equal(t, 1, counts[channelConfiguredTrue], "the live series must be the one left")
	assert.Equal(t, 0, counts[channelConfiguredFalse], "the orphan series of the earlier tick must be deleted")

	counts = seriesCountByConfigured(t, "newapi_upstream_quota_reset_timestamp_seconds", channel)
	assert.Equal(t, 1, counts[channelConfiguredTrue])
	assert.Equal(t, 0, counts[channelConfiguredFalse])

	counts = seriesCountByConfigured(t, "newapi_upstream_quota_refresh_timestamp_seconds", channel)
	assert.Equal(t, 1, counts[channelConfiguredTrue])
	assert.Equal(t, 0, counts[channelConfiguredFalse])

	// The flip has to work the other way round too, or the orphan series would
	// outlive a channel that is deleted again.
	ObserveUpstreamQuota(UpstreamQuota{
		Channel: channel, Windows: window(), RefreshedAt: refresh, ChannelOrphaned: true,
	})
	counts = seriesCountByConfigured(t, "newapi_upstream_quota_remaining_ratio", channel)
	assert.Equal(t, 1, counts[channelConfiguredFalse])
	assert.Equal(t, 0, counts[channelConfiguredTrue])

	// A tick can also publish fewer windows than the one before it: the console
	// fallback reports only the Vibe allowance when the subscription page has no
	// api_budget, so the api window disappears while the account flips to orphan.
	// The label belongs to the channel and not to a window, so the deletion has to
	// reach a window this tick no longer publishes; otherwise the dropped window
	// keeps reporting the value under which it was last written.
	shrinking := "Mistral Vibe (shrinking windows test)"
	bothWindows := func() []QuotaWindow {
		return []QuotaWindow{
			{Scope: "api", Window: "monthly", RemainingRatio: 0.05, ResetAt: reset},
			{Scope: "vibe", Window: "monthly", RemainingRatio: 0.5, ResetAt: reset},
		}
	}
	ObserveUpstreamQuota(UpstreamQuota{
		Channel: shrinking, Windows: bothWindows(), RefreshedAt: refresh,
	})
	ObserveUpstreamQuota(UpstreamQuota{
		Channel:         shrinking,
		Windows:         []QuotaWindow{{Scope: "vibe", Window: "monthly", RemainingRatio: 0.5, ResetAt: reset}},
		RefreshedAt:     refresh,
		ChannelOrphaned: true,
	})

	counts = seriesCountByConfigured(t, "newapi_upstream_quota_remaining_ratio", shrinking)
	assert.Equal(t, 0, counts[channelConfiguredTrue],
		"the api window dropped from the tick must not keep its live label")
	assert.Equal(t, 1, counts[channelConfiguredFalse])

	counts = seriesCountByConfigured(t, "newapi_upstream_quota_reset_timestamp_seconds", shrinking)
	assert.Equal(t, 0, counts[channelConfiguredTrue],
		"the reset series of the dropped api window must not keep its live label")
	assert.Equal(t, 1, counts[channelConfiguredFalse])
}

// seriesCountByConfigured counts the published series of one metric for a
// channel, keyed by channel_configured value.
func seriesCountByConfigured(t *testing.T, metricName, channel string) map[string]int {
	t.Helper()
	gathered, err := registry.Gather()
	require.NoError(t, err)
	counts := map[string]int{channelConfiguredTrue: 0, channelConfiguredFalse: 0}
	for _, family := range gathered {
		if family.GetName() != metricName {
			continue
		}
		for _, sample := range family.GetMetric() {
			name, configured := "", ""
			for _, label := range sample.GetLabel() {
				switch label.GetName() {
				case labelChannel:
					name = label.GetValue()
				case labelChannelConfigured:
					configured = label.GetValue()
				}
			}
			if name == channel {
				counts[configured]++
			}
		}
	}
	return counts
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
