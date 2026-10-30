package controller

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/service"
)

// AntigravityQuotaRefreshInterval is how often Antigravity channels publish
// their account quota to the metrics endpoint.
const AntigravityQuotaRefreshInterval = 5 * time.Minute

func refreshAntigravityChannelQuotas() {
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		common.SysError(common.LogText("failed to list channels for the Antigravity quota refresh: %s", err.Error()))
		return
	}
	for _, channel := range channels {
		if channel.Type != constant.ChannelTypeAntigravity {
			continue
		}
		usage, err := service.FetchAntigravityChannelUsage(context.Background(), channel)
		if err != nil {
			common.SysLog(common.LogText("failed to refresh Antigravity quota: channel_id=%d, error=%v", channel.Id, err))
			continue
		}
		appmetrics.ObserveUpstreamQuota(antigravityQuota(channel, usage))
		time.Sleep(common.RequestInterval)
	}
}

// StartAntigravityQuotaRefresh polls the Antigravity account quota in the
// background so the metrics endpoint always carries a recent figure. The first
// lookup runs at startup, which keeps a restarted process from reporting an
// empty dashboard until the first tick.
func StartAntigravityQuotaRefresh(interval time.Duration) {
	if interval <= 0 {
		return
	}
	refreshAntigravityChannelQuotas()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		refreshAntigravityChannelQuotas()
	}
}

// antigravityQuota turns the usage groups into quota windows. A group without
// a display name falls back to the bucket name, so the per-model fallback of
// the quota lookup still publishes one series per model.
func antigravityQuota(channel *model.Channel, usage *service.AntigravityUsage) appmetrics.UpstreamQuota {
	quota := appmetrics.UpstreamQuota{
		Channel:     channel.Name,
		RefreshedAt: time.Unix(usage.FetchedAt, 0),
	}
	for _, group := range usage.Groups {
		for _, bucket := range group.Buckets {
			quota.Windows = append(quota.Windows, appmetrics.QuotaWindow{
				Scope:          fallbackLabel(group.DisplayName, bucket.DisplayName),
				Window:         fallbackLabel(bucket.Window, "default"),
				RemainingRatio: bucket.RemainingFraction,
				ResetAt:        parseQuotaResetTime(bucket.ResetTime),
			})
		}
	}
	return quota
}

func fallbackLabel(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "default"
}

func parseQuotaResetTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	resetAt, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return resetAt
}
