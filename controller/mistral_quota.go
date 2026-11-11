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

// MistralQuotaRefreshInterval is how often the configured Mistral accounts
// publish their monthly allowances to the metrics endpoint. The console sits
// behind a Cloudflare managed challenge, so the interval stays coarse and each
// tick adds jitter instead of polling on an exact schedule.
const MistralQuotaRefreshInterval = 30 * time.Minute

const mistralQuotaMaxBackoff = time.Hour

type mistralQuotaRefreshResult struct {
	authFailed bool
	failed     bool
}

func refreshMistralQuotas() mistralQuotaRefreshResult {
	accounts, err := service.LoadMistralQuotaAccounts()
	if err != nil {
		common.SysError(common.LogText("failed to load Mistral quota accounts: %s", err.Error()))
		return mistralQuotaRefreshResult{failed: true}
	}
	if len(accounts) == 0 {
		return mistralQuotaRefreshResult{}
	}

	client := service.NewMistralQuotaClient()
	result := mistralQuotaRefreshResult{}
	for _, account := range accounts {
		quota, err := client.Fetch(context.Background(), account)
		if err != nil {
			result.failed = true
			var authErr *service.MistralQuotaAuthError
			if errors.As(err, &authErr) {
				result.authFailed = true
			}
			common.SysLog(common.LogText("failed to refresh Mistral quota: channel=%s, error=%v", account.Channel, err))
			continue
		}
		appmetrics.ObserveUpstreamQuota(appmetrics.UpstreamQuota{
			Channel:     quota.Channel,
			Windows:     mistralQuotaWindows(quota),
			RefreshedAt: quota.FetchedAt,
		})
		// The channel balance shows the allowance the channel key consumes, and
		// the balance sweep skips a disabled channel, so it is refreshed here for
		// every configured channel instead.
		if err := refreshMistralChannelBalance(account, quota); err != nil {
			common.SysLog(common.LogText("failed to refresh Mistral channel balance: channel=%s, error=%v", account.Channel, err))
		}
		time.Sleep(common.RequestInterval)
	}
	return result
}

// mistralQuotaWindows lists every allowance the console reported, so the
// dashboard can separate the API/Studio budget from the Vibe Code budget.
func mistralQuotaWindows(quota *service.MistralQuota) []appmetrics.QuotaWindow {
	windows := make([]appmetrics.QuotaWindow, 0, 2)
	for _, budget := range []*service.MistralBudget{quota.API, quota.Vibe} {
		if budget == nil {
			continue
		}
		windows = append(windows, appmetrics.QuotaWindow{
			Scope:          budget.Scope,
			Window:         "monthly",
			RemainingRatio: clampMistralQuotaRatio(1 - budget.UsagePercentage/100),
			ResetAt:        budget.ResetAt,
		})
	}
	return windows
}

// StartMistralQuotaRefresh polls the monthly allowances in the background. The
// first lookup runs at startup so a restarted process does not report an empty
// quota until the first tick. A rejected session backs off for up to an hour
// instead of retrying a dead cookie at the normal cadence.
func StartMistralQuotaRefresh(interval time.Duration) {
	if interval <= 0 {
		return
	}
	wait := time.Duration(0)
	for {
		if wait > 0 {
			time.Sleep(wait)
		}
		result := refreshMistralQuotas()
		switch {
		case result.authFailed:
			wait = min(6*interval, mistralQuotaMaxBackoff)
		case result.failed:
			wait = min(2*interval, mistralQuotaMaxBackoff)
		default:
			wait = jitterMistralQuotaInterval(interval)
		}
	}
}

func jitterMistralQuotaInterval(interval time.Duration) time.Duration {
	jitter := interval / 5
	if jitter <= 0 {
		return interval
	}
	return interval + rand.N(jitter)
}

func clampMistralQuotaRatio(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
