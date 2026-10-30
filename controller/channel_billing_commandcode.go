package controller

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
)

const (
	commandCodeCreditsPath    = "/alpha/billing/credits"
	commandCodeMaxBodyBytes   = 1 << 20
	commandCodeRequestTTL     = 15 * time.Second
	commandCodeWindowFiveHour = "5h"
	commandCodeWindowWeekly   = "weekly"

	// CommandCodeBalanceRefreshInterval is how often channels configured with
	// BalanceSourceCommandCode refresh their balance and publish it to the
	// metrics endpoint.
	CommandCodeBalanceRefreshInterval = 5 * time.Minute
)

// commandCodeCredits holds the fields read from Command Code's
// /alpha/billing/credits response. The route is not part of the documented
// Provider API, so every field stays optional and the shape is treated as
// unstable: a missing or renamed field reads as zero instead of failing the
// lookup.
type commandCodeCredits struct {
	Credits struct {
		MonthlyCredits   float64 `json:"monthlyCredits"`
		PurchasedCredits float64 `json:"purchasedCredits"`
		FreeCredits      float64 `json:"freeCredits"`
	} `json:"credits"`
	WindowLimits struct {
		FiveHour commandCodeCreditWindow `json:"fiveHour"`
		Weekly   commandCodeCreditWindow `json:"weekly"`
	} `json:"windowLimits"`
}

// commandCodeCreditWindow is one rolling usage window in US dollars. ResetAt
// is an epoch millisecond value, unlike the second-based reset a 429 body
// carries.
type commandCodeCreditWindow struct {
	Used    float64 `json:"used"`
	Cap     float64 `json:"cap"`
	ResetAt int64   `json:"resetAt"`
}

// remainingUSD is the credit the account can still spend. monthlyCredits
// counts down from the plan grant, so the three pools are summed rather than
// compared.
func (credits commandCodeCredits) remainingUSD() float64 {
	return credits.Credits.MonthlyCredits + credits.Credits.PurchasedCredits + credits.Credits.FreeCredits
}

// commandCodeCreditsURL derives the balance URL from the channel base URL. The
// balance route sits at the API host root while the channel calls completions
// under the /provider/v1 prefix, so only scheme and host survive.
func commandCodeCreditsURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", baseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid base URL %q", baseURL)
	}
	return parsed.Scheme + "://" + parsed.Host + commandCodeCreditsPath, nil
}

// commandCodeCredentials lists the keys the balance lookup authenticates with.
// A multi-key channel can pool keys issued to different accounts, so every one
// of them is queried and the results are added up.
func commandCodeCredentials(channel *model.Channel) []string {
	keys := make([]string, 0, len(channel.GetKeys()))
	for _, key := range channel.GetKeys() {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	return keys
}

func fetchCommandCodeCredits(channel *model.Channel, key string) (commandCodeCredits, error) {
	credits := commandCodeCredits{}
	requestURL, err := commandCodeCreditsURL(channel.GetBaseURL())
	if err != nil {
		return credits, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandCodeRequestTTL)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return credits, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")

	setting := channel.GetSetting()
	client, err := service.GetHttpClientWithProxySettings(setting.Proxy, setting)
	if err != nil {
		return credits, err
	}
	response, err := client.Do(request)
	if err != nil {
		return credits, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return credits, fmt.Errorf("status code: %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, commandCodeMaxBodyBytes))
	if err != nil {
		return credits, err
	}
	if err := common.Unmarshal(body, &credits); err != nil {
		return credits, fmt.Errorf("invalid credits JSON response: %w", err)
	}
	return credits, nil
}

// updateChannelCommandCodeBalance refreshes one channel's balance from Command
// Code and publishes the rolling windows to the metrics endpoint. Credits spent
// by other tools on the same account show up here too, because the upstream
// reports the account rather than the channel.
func updateChannelCommandCodeBalance(channel *model.Channel) (float64, error) {
	accounts, err := fetchCommandCodeBalances(channel)
	if err != nil {
		return 0, err
	}
	balance, windows := aggregateCommandCodeCredits(accounts)
	if math.IsNaN(balance) || math.IsInf(balance, 0) || balance < 0 {
		return 0, fmt.Errorf("invalid balance: %v", balance)
	}
	channel.UpdateBalance(balance)
	appmetrics.ObserveChannelBalance(appmetrics.ChannelBalance{
		Channel:      channel.Name,
		RemainingUSD: balance,
		Windows:      windows,
		RefreshedAt:  time.Now(),
	})
	return balance, nil
}

// fetchCommandCodeBalances queries every key of the channel. A key that fails is
// skipped so the remaining accounts still produce a usable total; when every
// key fails the first error is returned.
func fetchCommandCodeBalances(channel *model.Channel) ([]commandCodeCredits, error) {
	keys := commandCodeCredentials(channel)
	if len(keys) == 0 {
		return nil, fmt.Errorf("channel has no key")
	}
	accounts := make([]commandCodeCredits, 0, len(keys))
	var firstErr error
	for _, key := range keys {
		credits, err := fetchCommandCodeCredits(channel, key)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		accounts = append(accounts, credits)
	}
	if len(accounts) == 0 {
		return nil, firstErr
	}
	return accounts, nil
}

// aggregateCommandCodeCredits adds the accounts behind one channel. The window
// reset is the earliest one, because a window the upstream reports as nearly
// spent must never look healthier than it is.
func aggregateCommandCodeCredits(accounts []commandCodeCredits) (float64, []appmetrics.CreditsWindow) {
	remaining := 0.0
	fiveHour := appmetrics.CreditsWindow{Name: commandCodeWindowFiveHour}
	weekly := appmetrics.CreditsWindow{Name: commandCodeWindowWeekly}
	for _, account := range accounts {
		remaining += account.remainingUSD()
		fiveHour = addCommandCodeCreditsWindow(fiveHour, commandCodeCreditsWindow(commandCodeWindowFiveHour, account.WindowLimits.FiveHour))
		weekly = addCommandCodeCreditsWindow(weekly, commandCodeCreditsWindow(commandCodeWindowWeekly, account.WindowLimits.Weekly))
	}
	return remaining, []appmetrics.CreditsWindow{fiveHour, weekly}
}

func addCommandCodeCreditsWindow(sum, window appmetrics.CreditsWindow) appmetrics.CreditsWindow {
	sum.UsedUSD += window.UsedUSD
	sum.CapUSD += window.CapUSD
	if window.ResetAt.IsZero() {
		return sum
	}
	if sum.ResetAt.IsZero() || window.ResetAt.Before(sum.ResetAt) {
		sum.ResetAt = window.ResetAt
	}
	return sum
}

func commandCodeCreditsWindow(name string, window commandCodeCreditWindow) appmetrics.CreditsWindow {
	creditsWindow := appmetrics.CreditsWindow{Name: name, UsedUSD: window.Used, CapUSD: window.Cap}
	if window.ResetAt > 0 {
		creditsWindow.ResetAt = time.UnixMilli(window.ResetAt)
	}
	return creditsWindow
}

// isCommandCodeBalanceChannel reports whether the balance refresher owns this
// channel. Only enabled OpenAI channels that selected the Command Code balance
// source qualify, so a deployment without one never calls upstream.
func isCommandCodeBalanceChannel(channel *model.Channel) bool {
	if channel.Type != constant.ChannelTypeOpenAI || channel.Status != common.ChannelStatusEnabled {
		return false
	}
	return channel.GetSetting().BalanceSource == dto.BalanceSourceCommandCode
}

// refreshCommandCodeChannelBalances refreshes every Command Code balance
// channel once. A failing channel keeps its previous balance and timestamp.
func refreshCommandCodeChannelBalances() {
	channels, err := model.GetAllChannels(0, 0, true, false)
	if err != nil {
		common.SysError(common.LogText("failed to list channels for balance refresh: %s", err.Error()))
		return
	}
	for _, channel := range channels {
		if !isCommandCodeBalanceChannel(channel) {
			continue
		}
		if _, err := updateChannelCommandCodeBalance(channel); err != nil {
			common.SysLog(common.LogText("failed to refresh Command Code balance: channel_id=%d, error=%v", channel.Id, err))
		}
		time.Sleep(common.RequestInterval)
	}
}

// StartCommandCodeBalanceRefresh polls the Command Code balance in the
// background so the metrics endpoint always carries a recent figure. The first
// lookup runs at startup, which keeps a restarted process from reporting an
// empty dashboard until the first tick.
func StartCommandCodeBalanceRefresh(interval time.Duration) {
	if interval <= 0 {
		return
	}
	refreshCommandCodeChannelBalances()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		refreshCommandCodeChannelBalances()
	}
}
