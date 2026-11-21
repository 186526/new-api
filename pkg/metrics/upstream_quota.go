package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The label names of the allowance series. The vector declarations and the
// deletion that keeps a label flip from leaving residue read them from the same
// place, so a rename cannot leave the two out of step.
const (
	labelChannel           = "channel"
	labelChannelConfigured = "channel_configured"
	labelScope             = "scope"
	labelWindow            = "window"

	channelConfiguredTrue  = "true"
	channelConfiguredFalse = "false"
)

// channelConfiguredValue is the value the channel_configured label carries on
// the upstream allowance series: "true" for a series that belongs to a channel
// New API still has, "false" for the allowance a configured account keeps
// reporting after its channel was removed. A dashboard filters on it to keep
// those orphan series out of the channel rows.
func channelConfiguredValue(configured bool) string {
	if configured {
		return channelConfiguredTrue
	}
	return channelConfiguredFalse
}

// oppositeChannelConfigured returns the value a flipped publish replaces, so the
// series left under the previous value can be deleted.
func oppositeChannelConfigured(configured string) string {
	if configured == channelConfiguredTrue {
		return channelConfiguredFalse
	}
	return channelConfiguredTrue
}

var (
	upstreamQuotaRemainingRatio = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_remaining_ratio",
		Help: "Remaining fraction of an upstream quota window, from 0 to 1.",
	}, []string{labelChannel, labelScope, labelWindow, labelChannelConfigured})

	upstreamQuotaResetTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_reset_timestamp_seconds",
		Help: "Unix time at which an upstream quota window resets.",
	}, []string{labelChannel, labelScope, labelWindow, labelChannelConfigured})

	upstreamQuotaRefreshTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_refresh_timestamp_seconds",
		Help: "Unix time of the last successful upstream quota lookup.",
	}, []string{labelChannel, labelChannelConfigured})
)

// QuotaWindow is one upstream quota window that reports a remaining fraction
// instead of a currency amount, such as the Antigravity account quota.
type QuotaWindow struct {
	Scope          string
	Window         string
	RemainingRatio float64
	ResetAt        time.Time
}

// UpstreamQuota groups the quota windows of one channel together with the time
// they were read.
type UpstreamQuota struct {
	Channel     string
	Windows     []QuotaWindow
	RefreshedAt time.Time
	// ChannelOrphaned marks the allowance of a configured account that no
	// channel carries any more. Those windows still publish, labelled
	// channel_configured="false", so a dashboard can keep them out of the channel
	// rows while the account stays configured. A caller that read the windows
	// from a channel it holds leaves it false, which publishes "true".
	ChannelOrphaned bool
}

// ObserveUpstreamQuota publishes a successful quota lookup. The refresh
// timestamp is published only here, so a channel whose lookup keeps failing
// shows an ageing timestamp instead of a fresh one.
func ObserveUpstreamQuota(quota UpstreamQuota) {
	if quota.Channel == "" {
		return
	}
	configured := channelConfiguredValue(!quota.ChannelOrphaned)
	worst := make(map[string]QuotaWindow, len(quota.Windows))
	for _, window := range quota.Windows {
		if window.Scope == "" || window.Window == "" {
			continue
		}
		key := window.Scope + "\x00" + window.Window
		if seen, exists := worst[key]; !exists || window.RemainingRatio < seen.RemainingRatio {
			worst[key] = window
		}
	}
	// The label belongs to the channel, not to a window, so the opposite value is
	// deleted for the whole channel before anything is written. Matching on the
	// window here would only reach the windows of this tick, and a tick that
	// publishes fewer windows than the last one — the console fallback reports
	// only the Vibe allowance when the subscription page omits api_budget — would
	// leave the dropped window reporting under the value it was last written with.
	// The deletions run only when this tick publishes something, because a channel
	// with no publishable window has nothing to replace and would otherwise lose
	// the last figure it reported.
	if len(worst) > 0 {
		channelLabels := prometheus.Labels{labelChannel: quota.Channel}
		dropFlippedConfiguredSeries(upstreamQuotaRemainingRatio, configured, channelLabels)
		dropFlippedConfiguredSeries(upstreamQuotaResetTimestamp, configured, channelLabels)
	}
	for _, window := range worst {
		upstreamQuotaRemainingRatio.WithLabelValues(quota.Channel, window.Scope, window.Window, configured).Set(window.RemainingRatio)
		if !window.ResetAt.IsZero() {
			upstreamQuotaResetTimestamp.WithLabelValues(quota.Channel, window.Scope, window.Window, configured).Set(float64(window.ResetAt.Unix()))
		}
	}
	if !quota.RefreshedAt.IsZero() {
		dropFlippedConfiguredSeries(upstreamQuotaRefreshTimestamp, configured, prometheus.Labels{
			labelChannel: quota.Channel,
		})
		upstreamQuotaRefreshTimestamp.WithLabelValues(quota.Channel, configured).Set(float64(quota.RefreshedAt.Unix()))
	}
}

// dropFlippedConfiguredSeries deletes the series a previous publish left under
// the other channel_configured value. A GaugeVec keeps every series it ever
// created, so without the deletion a label flip leaves both values behind
// forever: the orphan series would outlive the channel that came back, and a
// dashboard filtering on channel_configured would keep excluding it.
func dropFlippedConfiguredSeries(vec *prometheus.GaugeVec, configured string, series prometheus.Labels) {
	labels := prometheus.Labels{
		labelChannelConfigured: oppositeChannelConfigured(configured),
	}
	for name, value := range series {
		labels[name] = value
	}
	vec.DeletePartialMatch(labels)
}
