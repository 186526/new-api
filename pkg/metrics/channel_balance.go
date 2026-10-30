package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	channelBalanceUSD = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_channel_balance_usd",
		Help: "Remaining upstream credit of a channel, in US dollars.",
	}, []string{"channel"})

	upstreamCreditsUsedUSD = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_credits_used_usd",
		Help: "Upstream credit consumed in a rolling window, in US dollars.",
	}, []string{"channel", "window"})

	upstreamCreditsCapUSD = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_credits_cap_usd",
		Help: "Upstream credit cap of a rolling window, in US dollars.",
	}, []string{"channel", "window"})

	upstreamCreditsResetTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_credits_reset_timestamp_seconds",
		Help: "Unix time at which the upstream rolling window resets.",
	}, []string{"channel", "window"})

	upstreamCreditsRefreshTimestamp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_upstream_credits_refresh_timestamp_seconds",
		Help: "Unix time of the last successful upstream credit lookup.",
	}, []string{"channel"})
)

// CreditsWindow is one rolling usage window an upstream reports. A window
// without a cap carries no usable ratio, so it is not published.
type CreditsWindow struct {
	Name    string
	UsedUSD float64
	CapUSD  float64
	ResetAt time.Time
}

// ChannelBalance is one channel's remaining upstream credit together with the
// rolling windows the upstream reports for it.
type ChannelBalance struct {
	Channel      string
	RemainingUSD float64
	Windows      []CreditsWindow
	RefreshedAt  time.Time
}

func setChannelBalance(channel string, remainingUSD float64) {
	if channel == "" {
		return
	}
	channelBalanceUSD.WithLabelValues(channel).Set(remainingUSD)
}

// ObserveChannelBalanceUSD publishes the remaining balance of a channel whose
// upstream answers a balance query but reports no rolling windows, so every
// provider New API can query shows up next to the credit-reporting ones.
func ObserveChannelBalanceUSD(channelName string, channelID int, remainingUSD float64) {
	setChannelBalance(labelName(channelName, channelID), remainingUSD)
}

// ObserveChannelBalance publishes a successful upstream credit lookup. The
// refresh timestamp is published only here, so a channel whose lookup keeps
// failing shows an ageing timestamp instead of a fresh one.
func ObserveChannelBalance(balance ChannelBalance) {
	if balance.Channel == "" {
		return
	}
	setChannelBalance(balance.Channel, balance.RemainingUSD)
	for _, window := range balance.Windows {
		if window.Name == "" || window.CapUSD <= 0 {
			continue
		}
		upstreamCreditsUsedUSD.WithLabelValues(balance.Channel, window.Name).Set(window.UsedUSD)
		upstreamCreditsCapUSD.WithLabelValues(balance.Channel, window.Name).Set(window.CapUSD)
		if !window.ResetAt.IsZero() {
			upstreamCreditsResetTimestamp.WithLabelValues(balance.Channel, window.Name).Set(float64(window.ResetAt.Unix()))
		}
	}
	if !balance.RefreshedAt.IsZero() {
		upstreamCreditsRefreshTimestamp.WithLabelValues(balance.Channel).Set(float64(balance.RefreshedAt.Unix()))
	}
}
