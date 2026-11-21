package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// channelConfiguredValue is the value the channel_configured label carries on
// the upstream allowance series of this package: "true" for a series that
// belongs to a channel New API still has, "false" for the allowance a configured
// account keeps reporting after its channel was removed. A dashboard filters on
// it to keep those orphan series out of the channel rows.
func channelConfiguredValue(configured bool) string {
	return strconv.FormatBool(configured)
}

var (
	upstreamQuotaRemainingRatio = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_remaining_ratio",
		Help: "Remaining fraction of an upstream quota window, from 0 to 1.",
	}, []string{"channel", "scope", "window", "channel_configured"})

	upstreamQuotaResetTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_reset_timestamp_seconds",
		Help: "Unix time at which an upstream quota window resets.",
	}, []string{"channel", "scope", "window", "channel_configured"})

	upstreamQuotaRefreshTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_refresh_timestamp_seconds",
		Help: "Unix time of the last successful upstream quota lookup.",
	}, []string{"channel", "channel_configured"})
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
	for _, window := range worst {
		upstreamQuotaRemainingRatio.WithLabelValues(quota.Channel, window.Scope, window.Window, configured).Set(window.RemainingRatio)
		if !window.ResetAt.IsZero() {
			upstreamQuotaResetTimestamp.WithLabelValues(quota.Channel, window.Scope, window.Window, configured).Set(float64(window.ResetAt.Unix()))
		}
	}
	if !quota.RefreshedAt.IsZero() {
		upstreamQuotaRefreshTimestamp.WithLabelValues(quota.Channel, configured).Set(float64(quota.RefreshedAt.Unix()))
	}
}
