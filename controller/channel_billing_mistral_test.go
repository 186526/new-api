package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

func setupMistralBalanceTest(t *testing.T) {
	t.Helper()
	originalDB := model.DB
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Discard,
	})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.Channel{}))
	model.DB = database
	t.Cleanup(func() {
		model.DB = originalDB
	})
}

// newMistralConsoleServer answers both allowance sources with one merged
// allowance used at percentage percent of limit dollars: the public billing
// endpoint for the channel key, which the balance path reads first, and the
// console trpc endpoint for a cookie, which is the fallback for an entry that
// carries one and also what a key the billing endpoint refuses falls through to.
// The key endpoint is answered with its own two-sided shape, so a test drives the
// source the balance path actually reads.
func newMistralConsoleServer(t *testing.T, percentage float64, limit float64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/billing/budget":
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer mstrl_")
			assert.NotEmpty(t, r.Header.Get("Authorization"), "the key endpoint is read with a key, not a cookie")
			_, _ = fmt.Fprintf(w,
				`{"vibe_budget":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false},"api_budget":{"usage_percentage":%v,"initial_budget":15,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}`,
				percentage, limit, percentage)
		default:
			assert.Contains(t, r.Header.Get("Cookie"), "ory_session_")
			assert.Contains(t, r.Header.Get("User-Agent"), "Mozilla/5.0")
			_, _ = fmt.Fprintf(w,
				`{"result":{"data":{"json":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}}}`,
				percentage, limit)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func setMistralQuotaEnv(t *testing.T, serverURL string, accounts string) {
	t.Helper()
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_QUOTA_API_BASE_URL", serverURL)
	t.Setenv("MISTRAL_QUOTA_ADMIN_BASE_URL", serverURL)
	t.Setenv("MISTRAL_VIBE_QUOTA_BASE_URL", serverURL)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", accounts)
}

// TestMistralBalanceReadsTheChannelKeyEvenWhenTheFileKeyIsFree requires the
// balance path to read the allowance with the key the channel calls the upstream
// with and not with the key recorded in the accounts file. On 2026-11-21 the file
// named a key that answered the free plan's 100% of 10 USD while the key of
// channel 16 answered 1.238795712% of 150 USD, so the file key reported nothing
// left and the sweep disabled the channel; the channel key reports about 148.14
// USD, whatever key the file names.
func TestMistralBalanceReadsTheChannelKeyEvenWhenTheFileKeyIsFree(t *testing.T) {
	setupMistralBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/billing/budget" {
			t.Errorf("path = %q, want the key billing endpoint", r.URL.Path)
			http.Error(w, "the balance path reads only the key endpoint", http.StatusInternalServerError)
			return
		}
		if r.Header.Get("Authorization") == "Bearer mstrl_old" {
			_, _ = w.Write([]byte(`{"vibe_budget":{"usage_percentage":100,"initial_budget":10},"api_budget":{"usage_percentage":100,"initial_budget":10}}`))
			return
		}
		_, _ = w.Write([]byte(`{"vibe_budget":{"usage_percentage":1.238795712,"initial_budget":150},"api_budget":{"usage_percentage":0.00018766,"initial_budget":15}}`))
	}))
	t.Cleanup(server.Close)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral Vibe (xu.bo)","cookie":"ory_session_x=stale","vibeKey":"mstrl_old","monthlyLimitUsd":150}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_channel", Name: "Mistral Vibe (xu.bo)"}
	require.NoError(t, model.DB.Create(channel).Error)

	result, err := updateChannelBalance(channel)

	require.NoError(t, err)
	assert.InDelta(t, 148.14, result.Balance, 0.01, "the channel key reports the plan's own allowance")
	assert.InDelta(t, 148.14, channel.Balance, 0.01)
}

// TestMistralBalanceFallsBackToTheSessionWhenTheChannelKeyIsRejected requires a
// channel key the endpoint refuses to fall through to the cookie instead of
// writing a balance of zero, which would disable the channel for a credential
// that only needs replacing.
func TestMistralBalanceFallsBackToTheSessionWhenTheChannelKeyIsRejected(t *testing.T) {
	setupMistralBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/billing/budget" {
			http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		assert.Contains(t, r.Header.Get("Cookie"), "ory_session_")
		_, _ = w.Write([]byte(`{"result":{"data":{"json":{"usage_percentage":65.79,"initial_budget":150,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}}}`))
	}))
	t.Cleanup(server.Close)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_rotated", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	result, err := updateChannelBalance(channel)

	require.NoError(t, err)
	assert.InDelta(t, 51.315, result.Balance, 1e-9, "the console still reads the plan")
}

// TestMistralBalanceIsTheSameForEveryKeyOfTheAccount requires the channel
// balance to stop depending on which key the channel carries: Vibe and Studio
// draw on one monthly allowance, so a Studio key and a Vibe plan key of the same
// account report the same figure. 65.79% of 150 USD leaves 51.315 USD.
func TestMistralBalanceIsTheSameForEveryKeyOfTheAccount(t *testing.T) {
	setupMistralBalanceTest(t)
	server := newMistralConsoleServer(t, 65.79, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","studioKey":"mstrl_studio","monthlyLimitUsd":150}]`)

	for _, key := range []string{"mstrl_studio", "mstrl_vibe", "mstrl_unlisted"} {
		channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: key, Name: "Mistral (pro)"}
		require.NoError(t, model.DB.Create(channel).Error)
		result, err := updateChannelBalance(channel)
		require.NoError(t, err, "key %q", key)
		assert.InDelta(t, 51.315, result.Balance, 1e-9, "key %q", key)
		assert.InDelta(t, 51.315, channel.Balance, 1e-9, "key %q", key)
	}
}

func TestMistralBalanceRejectsAnAccountWithoutAPlanAmount(t *testing.T) {
	setupMistralBalanceTest(t)
	server := newMistralConsoleServer(t, 25, 0)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1"}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (pro)"}

	_, err := updateChannelBalance(channel)

	require.ErrorContains(t, err, "has no plan amount for its monthly allowance")
}

func TestMistralBalanceRejectsUnknownChannel(t *testing.T) {
	setupMistralBalanceTest(t)
	setMistralQuotaEnv(t, "http://127.0.0.1:1", `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (other)"}

	_, err := updateChannelBalance(channel)

	require.ErrorContains(t, err, `no Mistral quota account is configured for channel "Mistral (other)"`)
}

// newMistralAutoDisableServer serves the allowance of one account and flips it
// between an exhausted and a refilled plan, so a test can take a channel through
// the disable and the recovery of the same allowance. Both sources report the
// same figure, because the balance path reads the key endpoint first.
type mistralAutoDisableServer struct {
	*httptest.Server
	exhausted bool
}

func newMistralAutoDisableState(t *testing.T) *mistralAutoDisableServer {
	t.Helper()
	state := &mistralAutoDisableServer{}
	state.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		percentage, limit := 100.0, 150.0
		if !state.exhausted {
			percentage, limit = 65.79, 150.0
		}
		if r.URL.Path == "/v1/billing/budget" {
			_, _ = fmt.Fprintf(w,
				`{"vibe_budget":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}`,
				percentage, limit)
			return
		}
		_, _ = fmt.Fprintf(w,
			`{"result":{"data":{"json":{"usage_percentage":%v,"initial_budget":%v,"currency":"USD","reset_at":"2026-12-01T00:00:00Z","payg_enabled":false}}}}`,
			percentage, limit)
	}))
	t.Cleanup(state.Close)
	return state
}

// captureMistralSweepLog pins the sweep's pause between channels to zero and
// returns the buffer its lines land in.
func captureMistralSweepLog(t *testing.T) {
	t.Helper()
	previous := common.RequestInterval
	common.RequestInterval = 0
	t.Cleanup(func() { common.RequestInterval = previous })
}

// TestChannelBalanceSweepReenablesAChannelItDisabled requires a channel the sweep
// disabled at zero balance to come back once the allowance it draws on resets.
// The sweep used to visit enabled channels only, so the channel it disabled was
// never read again and stayed out of service until an admin re-enabled it by
// hand. The reset is the allowance of a merged monthly plan, which every channel
// of the account draws on.
func TestChannelBalanceSweepReenablesAChannelItDisabled(t *testing.T) {
	setupMistralBalanceTest(t)
	captureMistralSweepLog(t)
	state := newMistralAutoDisableState(t)
	setMistralQuotaEnv(t, state.Server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)

	channel := &model.Channel{
		Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (pro)",
		Status: common.ChannelStatusEnabled, OtherInfo: "{}",
	}
	require.NoError(t, model.DB.Create(channel).Error)

	state.exhausted = true
	require.NoError(t, updateAllChannelsBalance())
	disabled, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	require.Equal(t, common.ChannelStatusAutoDisabled, disabled.Status,
		"a channel with nothing left must be disabled by the sweep")
	require.Equal(t, channelStatusReasonInsufficientBalance, disabled.GetOtherInfo()["status_reason"])

	state.exhausted = false
	require.NoError(t, updateAllChannelsBalance())
	enabled, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, enabled.Status,
		"a refilled allowance must bring the channel back")
}

// TestChannelBalanceSweepLeavesAManualDisableAlone requires the recovery to stay
// with the balance: a channel an admin disabled by hand keeps that state even
// when its allowance is healthy.
func TestChannelBalanceSweepLeavesAManualDisableAlone(t *testing.T) {
	setupMistralBalanceTest(t)
	captureMistralSweepLog(t)
	server := newMistralConsoleServer(t, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)
	require.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual operation"))

	require.NoError(t, updateAllChannelsBalance())

	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
}

// TestChannelBalanceSweepLeavesARelayDisableAlone requires a channel the relay
// disabled after upstream failures to stay disabled when its balance is healthy:
// only a channel the sweep itself took out is the sweep's to bring back.
func TestChannelBalanceSweepLeavesARelayDisableAlone(t *testing.T) {
	setupMistralBalanceTest(t)
	captureMistralSweepLog(t)
	server := newMistralConsoleServer(t, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)
	require.True(t, model.UpdateChannelStatus(channel.Id, "", common.ChannelStatusAutoDisabled, "upstream returned 401"))

	require.NoError(t, updateAllChannelsBalance())

	stored, err := model.GetChannelById(channel.Id, true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.Status)
	assert.Equal(t, "upstream returned 401", stored.GetOtherInfo()["status_reason"])
}

// TestApplyMistralChannelBalanceReadsTheMergedAllowance requires the stored
// balance to be the plan amount minus what the merged allowance used, whatever
// key the channel carries.
func TestApplyMistralChannelBalanceReadsTheMergedAllowance(t *testing.T) {
	setupMistralBalanceTest(t)
	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_studio", Name: "Mistral (pro)"}
	quota := &service.MistralQuota{
		Channel: "Mistral (pro)",
		Allowance: &service.MistralBudget{
			UsagePercentage: 65.79,
			LimitUSD:        150,
			ResetAt:         time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		},
		FetchedAt: time.Unix(1793550000, 0),
	}

	balance, err := applyMistralChannelBalance(channel, quota)

	require.NoError(t, err)
	assert.InDelta(t, 51.315, balance, 1e-9)
	assert.InDelta(t, 51.315, channel.Balance, 1e-9)
}
