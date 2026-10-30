package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	upstreamQuotaRemainingRatio = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_remaining_ratio",
		Help: "Remaining fraction of an upstream quota window, from 0 to 1.",
	}, []string{"channel", "scope", "window"})

	upstreamQuotaResetTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_reset_timestamp_seconds",
		Help: "Unix time at which an upstream quota window resets.",
	}, []string{"channel", "scope", "window"})

	upstreamQuotaRefreshTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_quota_refresh_timestamp_seconds",
		Help: "Unix time of the last successful upstream quota lookup.",
	}, []string{"channel"})
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
}

// ObserveUpstreamQuota publishes a successful quota lookup. The refresh
// timestamp is published only here, so a channel whose lookup keeps failing
// shows an ageing timestamp instead of a fresh one.
func ObserveUpstreamQuota(quota UpstreamQuota) {
	if quota.Channel == "" {
		return
	}
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
		upstreamQuotaRemainingRatio.WithLabelValues(quota.Channel, window.Scope, window.Window).Set(window.RemainingRatio)
		if !window.ResetAt.IsZero() {
			upstreamQuotaResetTimestamp.WithLabelValues(quota.Channel, window.Scope, window.Window).Set(float64(window.ResetAt.Unix()))
		}
	}
	if !quota.RefreshedAt.IsZero() {
		upstreamQuotaRefreshTimestamp.WithLabelValues(quota.Channel).Set(float64(quota.RefreshedAt.Unix()))
	}
}
