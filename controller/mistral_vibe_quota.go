package controller

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/QuantumNous/new-api/common"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/service"
)

// MistralVibeQuotaRefreshInterval is how often the configured Vibe accounts
// publish their monthly allowance to the metrics endpoint. The web endpoint
// sits behind a Cloudflare managed challenge, so the interval stays coarse and
// each tick adds jitter instead of polling on an exact schedule.
const MistralVibeQuotaRefreshInterval = 30 * time.Minute

const mistralVibeMaxBackoff = time.Hour

type mistralVibeRefreshResult struct {
	authFailed bool
	failed     bool
}

func refreshMistralVibeQuotas() mistralVibeRefreshResult {
	accounts, err := service.LoadMistralVibeAccounts()
	if err != nil {
		common.SysError(common.LogText("failed to load Mistral Vibe accounts: %s", err.Error()))
		return mistralVibeRefreshResult{failed: true}
	}
	if len(accounts) == 0 {
		return mistralVibeRefreshResult{}
	}

	client := service.NewMistralVibeClient()
	result := mistralVibeRefreshResult{}
	for _, account := range accounts {
		quota, err := client.Fetch(context.Background(), account)
		if err != nil {
			result.failed = true
			var authErr *service.MistralVibeAuthError
			if errors.As(err, &authErr) {
				result.authFailed = true
			}
			common.SysLog(common.LogText("failed to refresh Mistral Vibe quota: channel=%s, error=%v", account.Channel, err))
			continue
		}
		appmetrics.ObserveUpstreamQuota(appmetrics.UpstreamQuota{
			Channel: quota.Channel,
			Windows: []appmetrics.QuotaWindow{{
				Scope:          "vibe",
				Window:         "monthly",
				RemainingRatio: clampMistralVibeRatio(1 - quota.UsagePercentage/100),
				ResetAt:        quota.ResetAt,
			}},
			RefreshedAt: quota.FetchedAt,
		})
		time.Sleep(common.RequestInterval)
	}
	return result
}

// StartMistralVibeQuotaRefresh polls the Vibe allowance in the background. The
// first lookup runs at startup so a restarted process does not report an empty
// quota until the first tick. A rejected session backs off for up to an hour
// instead of retrying a dead cookie at the normal cadence.
func StartMistralVibeQuotaRefresh(interval time.Duration) {
	if interval <= 0 {
		return
	}
	wait := time.Duration(0)
	for {
		if wait > 0 {
			time.Sleep(wait)
		}
		result := refreshMistralVibeQuotas()
		switch {
		case result.authFailed:
			wait = min(6*interval, mistralVibeMaxBackoff)
		case result.failed:
			wait = min(2*interval, mistralVibeMaxBackoff)
		default:
			wait = jitterMistralVibeInterval(interval)
		}
	}
}

func jitterMistralVibeInterval(interval time.Duration) time.Duration {
	jitter := interval / 5
	if jitter <= 0 {
		return interval
	}
	return interval + rand.N(jitter)
}

func clampMistralVibeRatio(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
