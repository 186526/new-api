package controller

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

// newMistralConsoleBudgetServer answers the console trpc budget endpoint with one
// merged allowance used at percentage percent of limit dollars, which is the
// answer a console session reads.
func newMistralConsoleBudgetServer(t *testing.T, percentage float64, limit float64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/trpc/billing.budget" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w,
			`{"result":{"data":{"json":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}}}`,
			percentage, limit)
	}))
	t.Cleanup(server.Close)
	return server
}

// captureMistralServerLog redirects the server log to the returned buffer and
// resets the report of the missing channels, because common.SysLog writes through
// gin's writers and the report outlives a test. It also pins the pause between
// two accounts to zero, because common.RequestInterval is set for the process.
func captureMistralServerLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previousWriter, previousErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	previousInterval := common.RequestInterval
	gin.DefaultWriter, gin.DefaultErrorWriter = &output, &output
	common.RequestInterval = 0
	resetMistralMissingChannelLog()
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter = previousWriter, previousErrorWriter
		common.RequestInterval = previousInterval
		resetMistralMissingChannelLog()
	})
	return &output
}

func resetMistralMissingChannelLog() {
	mistralMissingChannelLog.mu.Lock()
	mistralMissingChannelLog.reported = ""
	mistralMissingChannelLog.mu.Unlock()
}

// TestMistralQuotaRefreshLogsAMissingChannelOnce requires two refresh ticks in a
// row to name the configured account without a channel once. The missing channel
// is a configuration state that lasts until the Secret changes or the channel
// comes back, so a line per tick pushed every other line out of the log.
func TestMistralQuotaRefreshLogsAMissingChannelOnce(t *testing.T) {
	setupMistralBalanceTest(t)
	captured := captureMistralServerLog(t)
	server := newMistralConsoleBudgetServer(t, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (gone)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","monthlyLimitUsd":150}]`)

	for tick := 1; tick <= 2; tick++ {
		result := refreshMistralQuotas()
		require.False(t, result.failed, "an account without a channel is a configuration state, not a failed refresh")
	}

	assert.Equal(t, 1, strings.Count(captured.String(),
		"no Mistral channel carries the name of the configured quota accounts Mistral (gone)"))
	assert.NotContains(t, captured.String(), "failed to refresh Mistral channel balance")
}

// TestMistralQuotaRefreshLogsAMissingChannelAgainWhenTheChannelsChange requires
// the line again after another Mistral channel appears, so a channel deleted by
// mistake is reported by the next refresh instead of staying hidden by the
// report of an earlier deletion.
func TestMistralQuotaRefreshLogsAMissingChannelAgainWhenTheChannelsChange(t *testing.T) {
	setupMistralBalanceTest(t)
	captured := captureMistralServerLog(t)
	server := newMistralConsoleBudgetServer(t, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (gone)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","monthlyLimitUsd":150}]`)

	require.False(t, refreshMistralQuotas().failed)
	require.False(t, refreshMistralQuotas().failed)
	other := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_other", Name: "Mistral (other)"}
	require.NoError(t, model.DB.Create(other).Error)
	captured.Reset()
	require.False(t, refreshMistralQuotas().failed)

	assert.Equal(t, 1, strings.Count(captured.String(),
		"no Mistral channel carries the name of the configured quota accounts Mistral (gone)"))
}

// TestMistralQuotaRefreshPublishesTheAllowanceOfAnAccountWithoutAChannel
// requires the missing channel to stay a balance-store problem: the quota metric
// is published for every configured account, with or without a channel. The
// metric is published inside the refresh, so the account is fetched again here
// only to read the window the refresh handed to it.
func TestMistralQuotaRefreshPublishesTheAllowanceOfAnAccountWithoutAChannel(t *testing.T) {
	setupMistralBalanceTest(t)
	captured := captureMistralServerLog(t)
	server := newMistralConsoleBudgetServer(t, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (gone)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","monthlyLimitUsd":150}]`)

	require.False(t, refreshMistralQuotas().failed)

	assert.Contains(t, captured.String(),
		"no Mistral channel carries the name of the configured quota accounts Mistral (gone)")
	quota, err := service.FetchMistralQuota(t.Context(), service.MistralQuotaAccount{
		Channel: "Mistral (gone)", Cookie: "ory_session_x=1", VibeKey: "mstrl_vibe", MonthlyLimitUSD: 150,
	})
	require.NoError(t, err)
	windows := mistralQuotaWindows(quota)
	require.Len(t, windows, 1, "Vibe and Studio share one allowance, so one window is published")
	assert.Equal(t, service.MistralBudgetScopeVibe, windows[0].Scope)
	assert.Equal(t, "monthly", windows[0].Window)
	assert.InDelta(t, 0.75, windows[0].RemainingRatio, 1e-9)
}

// TestMistralQuotaRefreshTakesTheConsoleAllowanceOverTheKeyBudget requires the
// refresh to publish what the console reports for a merged plan, not the figure
// the public billing endpoint reports to the key. The endpoint answered 10 USD
// at 100% on this plan, so a refresh that read it last would publish nothing left
// while the console still had 112.50 USD of 150.
func TestMistralQuotaRefreshTakesTheConsoleAllowanceOverTheKeyBudget(t *testing.T) {
	setupMistralBalanceTest(t)
	captureMistralServerLog(t)
	var keyBudgetHits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/trpc/billing.budget":
			_, _ = w.Write([]byte(`{"result":{"data":{"json":{"usage_percentage":25,"initial_budget":150,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}}}`))
		case "/v1/billing/budget":
			keyBudgetHits++
			_, _ = w.Write([]byte(`{"vibe_budget":{"usage_percentage":100,"initial_budget":10},"api_budget":{"usage_percentage":100,"initial_budget":10}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","studioKey":"mstrl_studio","monthlyLimitUsd":150}]`)
	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_studio", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	require.False(t, refreshMistralQuotas().failed)

	assert.Equal(t, 0, keyBudgetHits, "an account with a console session must never read the key budget endpoint")
	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.InDelta(t, 112.5, stored.Balance, 1e-9)
}
