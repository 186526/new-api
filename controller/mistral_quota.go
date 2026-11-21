package controller

import (
	"context"
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/service"
)

// MistralQuotaRefreshInterval is how often the configured Mistral accounts
// publish their monthly allowance to the metrics endpoint. The console is the
// primary source for every account that has a session, so this interval is what
// the console is polled at: each tick reads every account once, which the
// deployed one-minute value turns into five requests a minute for the five
// configured accounts (about 300 an hour), with no pause between them because
// POLLING_INTERVAL is unset. A failed refresh stretches the wait between ticks:
// a rejected session backs off to six intervals, any other failed source to
// two, and a tick in which every account answers re-jitters the interval. Each
// tick still adds jitter so the accounts do not poll on an exact schedule.
const MistralQuotaRefreshInterval = time.Minute

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
	channels := loadMistralChannelIndex()
	if channels.readErr != nil {
		common.SysError(common.LogText("failed to list channels for the Mistral quota refresh: %s", channels.readErr.Error()))
	}
	missing := newMistralMissingChannels(accounts)
	missing.recordChannels(channels)
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
		// The channel is resolved before the allowance is published, so the
		// series of an account whose channel is gone is labelled
		// channel_configured="false" instead of reading as a live channel.
		channel, orphaned := channels.resolve(account.Channel)
		appmetrics.ObserveUpstreamQuota(appmetrics.UpstreamQuota{
			Channel:         quota.Channel,
			Windows:         mistralQuotaWindows(quota),
			RefreshedAt:     quota.FetchedAt,
			ChannelOrphaned: orphaned,
		})
		if orphaned {
			missing.recordAbsent(account.Channel)
		}
		// The channel balance shows the account's merged allowance, and the
		// balance sweep visits a disabled channel only when it disabled it
		// itself, so it is refreshed here for every configured channel instead.
		// An account without a channel has no balance to store.
		if err := storeMistralChannelBalance(channel, quota); err != nil {
			common.SysLog(common.LogText("failed to refresh Mistral channel balance: channel=%s, error=%v", account.Channel, err))
		}
		time.Sleep(common.RequestInterval)
	}
	missing.logOnce()
	return result
}

// mistralMissingChannels reports the configured accounts whose allowance has no
// channel behind it, so a channel deleted by mistake is named in the log instead
// of only losing its balance. The channels are read once per tick and every
// account of that tick is resolved against the same read.
type mistralMissingChannels struct {
	names    map[string]struct{}
	absent   []string
	channels []string
	loadErr  string
}

func newMistralMissingChannels(accounts []service.MistralQuotaAccount) *mistralMissingChannels {
	missing := &mistralMissingChannels{names: make(map[string]struct{}, len(accounts))}
	for _, account := range accounts {
		missing.names[account.Channel] = struct{}{}
	}
	return missing
}

// recordChannels keeps the Mistral channels of the tick and, when the table
// could not be read, the error that kept them from being resolved. An unreadable
// table turns no account into a missing one: the report would otherwise name
// every account as lost.
func (m *mistralMissingChannels) recordChannels(channels mistralChannelIndex) {
	if channels.readErr != nil {
		m.loadErr = channels.readErr.Error()
		return
	}
	m.channels = channels.names
}

// recordAbsent adds the account whose allowance was published without a channel
// behind it. The names come from the quota configuration, so only a configured
// account can reach the list.
func (m *mistralMissingChannels) recordAbsent(account string) {
	if _, ok := m.names[account]; !ok {
		return
	}
	m.absent = append(m.absent, account)
}

// logOnce writes one line for the accounts that have no channel. The line is
// written when a refresh first finds them and again only when the report
// changes: a channel deleted by mistake is reported again after the channel list
// is edited or read again, and a missing account does not hide the lines of the
// other accounts, because their balance line keeps its place in the log.
func (m *mistralMissingChannels) logOnce() {
	if len(m.absent) == 0 {
		return
	}
	slices.Sort(m.absent)
	slices.Sort(m.channels)
	absent := slices.Compact(slices.Clone(m.absent))
	report := strings.Join(absent, ", ") + "|" +
		strings.Join(slices.Compact(m.channels), ", ") + "|" + m.loadErr
	mistralMissingChannelLog.mu.Lock()
	defer mistralMissingChannelLog.mu.Unlock()
	if mistralMissingChannelLog.reported == report {
		return
	}
	mistralMissingChannelLog.reported = report
	common.SysLog(common.LogText(
		"no Mistral channel carries the name of the configured quota accounts %s, so their allowance is published without a channel balance",
		strings.Join(absent, ", ")))
}

// mistralMissingChannelLog holds the report of the last logged line, so the next
// refresh only logs a report that changed.
var mistralMissingChannelLog struct {
	mu       sync.Mutex
	reported string
}

// mistralQuotaWindows lists the merged monthly allowance the console reported.
// Vibe and Studio draw on one allowance, so the account publishes one window:
// a second scope would show a plan amount every plan has stopped carrying.
func mistralQuotaWindows(quota *service.MistralQuota) []appmetrics.QuotaWindow {
	budget := quota.Allowance
	if budget == nil {
		return nil
	}
	return []appmetrics.QuotaWindow{{
		Scope:          service.MistralBudgetScopeVibe,
		Window:         "monthly",
		RemainingRatio: clampMistralQuotaRatio(1 - budget.UsagePercentage/100),
		ResetAt:        budget.ResetAt,
	}}
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
