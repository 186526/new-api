package controller

import (
	"bytes"
	"context"
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
	appmetrics "github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/service"
)

// newMistralBillingServer answers the billing endpoint of api.mistral.ai with a
// Vibe allowance used at vibePercentage percent of vibeLimit.
func newMistralBillingServer(t *testing.T, vibePercentage float64, vibeLimit float64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/budget" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"usage_percentage":0,"initial_budget":0,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","api_budget":{"usage_percentage":0,"initial_budget":0,"currency":"USD","reset_at":"2026-12-01T00:00:00Z"},"vibe_budget":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z"}}`,
			vibePercentage, vibeLimit)
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
	server := newMistralBillingServer(t, 25, 150)
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
	server := newMistralBillingServer(t, 25, 150)
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
// only to read the windows the refresh handed to it.
func TestMistralQuotaRefreshPublishesTheAllowanceOfAnAccountWithoutAChannel(t *testing.T) {
	setupMistralBalanceTest(t)
	captured := captureMistralServerLog(t)
	server := newMistralBillingServer(t, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (gone)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","monthlyLimitUsd":150}]`)

	require.False(t, refreshMistralQuotas().failed)

	assert.Contains(t, captured.String(),
		"no Mistral channel carries the name of the configured quota accounts Mistral (gone)")
	quota, err := service.FetchMistralQuota(context.Background(), service.MistralQuotaAccount{Channel: "Mistral (gone)", VibeKey: "mstrl_vibe"})
	require.NoError(t, err)
	windows := mistralQuotaWindows(quota)
	require.Len(t, windows, 2)
	vibe := windows[1]
	assert.Equal(t, service.MistralBudgetScopeVibe, vibe.Scope)
	assert.InDelta(t, 0.75, vibe.RemainingRatio, 1e-9)
}

// upstreamQuotaLabels returns the channel_configured value of every published
// series of one metric whose channel label is the given name, keyed by
// scope|window (empty for a metric without those labels).
func upstreamQuotaLabels(t *testing.T, metricName, channel string) map[string]bool {
	t.Helper()
	gathered, err := appmetrics.Registry().Gather()
	require.NoError(t, err)
	labels := make(map[string]bool)
	for _, family := range gathered {
		if family.GetName() != metricName {
			continue
		}
		for _, sample := range family.GetMetric() {
			channelName, configured, scope, window := "", "", "", ""
			for _, label := range sample.GetLabel() {
				switch label.GetName() {
				case "channel":
					channelName = label.GetValue()
				case "channel_configured":
					configured = label.GetValue()
				case "scope":
					scope = label.GetValue()
				case "window":
					window = label.GetValue()
				}
			}
			if channelName != channel {
				continue
			}
			labels[scope+"|"+window] = configured == "true"
		}
	}
	return labels
}

// TestMistralQuotaRefreshLabelsAConfiguredAccountWithoutAChannel requires the
// refresh to tell the two cases apart on the metric itself: an account whose
// channel exists publishes channel_configured="true" and an account that no
// channel carries publishes "false". The orphan keeps its series, so the figure
// stays readable, and only the label separates it from a live channel.
func TestMistralQuotaRefreshLabelsAConfiguredAccountWithoutAChannel(t *testing.T) {
	setupMistralBalanceTest(t)
	captureMistralServerLog(t)
	server := newMistralBillingServer(t, 25, 150)
	live := "Mistral (label live)"
	orphan := "Mistral (label orphan)"
	setMistralQuotaEnv(t, server.URL, `[`+
		`{"channel":"`+live+`","cookie":"ory_session_x=1","vibeKey":"mstrl_live","monthlyLimitUsd":150},`+
		`{"channel":"`+orphan+`","cookie":"ory_session_y=1","vibeKey":"mstrl_orphan","monthlyLimitUsd":150}]`)
	liveChannel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_live", Name: live}
	require.NoError(t, model.DB.Create(liveChannel).Error)

	require.False(t, refreshMistralQuotas().failed)

	ratios := upstreamQuotaLabels(t, "newapi_upstream_quota_remaining_ratio", live)
	require.Contains(t, ratios, "vibe|monthly")
	assert.True(t, ratios["vibe|monthly"], "a channel that carries the account name must publish channel_configured=true")

	orphanRatios := upstreamQuotaLabels(t, "newapi_upstream_quota_remaining_ratio", orphan)
	require.Contains(t, orphanRatios, "vibe|monthly", "the orphan keeps its series so the figure stays readable")
	assert.False(t, orphanRatios["vibe|monthly"], "an account no channel carries must publish channel_configured=false")

	orphanRefreshes := upstreamQuotaLabels(t, "newapi_upstream_quota_refresh_timestamp_seconds", orphan)
	assert.False(t, orphanRefreshes["|"], "the refresh timestamp of an orphan carries the same label")
	liveRefreshes := upstreamQuotaLabels(t, "newapi_upstream_quota_refresh_timestamp_seconds", live)
	assert.True(t, liveRefreshes["|"])

	balances := upstreamQuotaLabels(t, "newapi_channel_balance_usd", live)
	assert.True(t, balances["|"], "a balance series belongs to a channel and stays configured")
}

// TestMistralQuotaRefreshKeepsALiveChannelWhenTheTableCannotBeRead requires an
// unreadable channel table to leave every account labelled as configured. The
// table says nothing about a channel being gone, so hiding a live channel
// behind an orphan label would be the worse failure, and naming every account in
// the missing-channel line would be worse still.
func TestMistralQuotaRefreshKeepsALiveChannelWhenTheTableCannotBeRead(t *testing.T) {
	setupMistralBalanceTest(t)
	captured := captureMistralServerLog(t)
	server := newMistralBillingServer(t, 25, 150)
	account := "Mistral (unreadable)"
	setMistralQuotaEnv(t, server.URL, `[{"channel":"`+account+`","cookie":"ory_session_x=1","vibeKey":"mstrl_unreadable","monthlyLimitUsd":150}]`)
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	require.False(t, refreshMistralQuotas().failed)

	labels := upstreamQuotaLabels(t, "newapi_upstream_quota_remaining_ratio", account)
	require.Contains(t, labels, "vibe|monthly")
	assert.True(t, labels["vibe|monthly"], "an unreadable channel table must not label a live account as an orphan")
	assert.NotContains(t, captured.String(),
		"no Mistral channel carries the name of the configured quota accounts "+account)
}
