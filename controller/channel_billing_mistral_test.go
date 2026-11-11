package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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

// newMistralSubscriptionServer serves the console page with an API budget used
// at apiPercentage percent of apiLimit and a Vibe budget used at
// vibePercentage percent of vibeLimit.
func newMistralSubscriptionServer(t *testing.T, apiPercentage float64, apiLimit float64, vibePercentage float64, vibeLimit float64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/subscription", r.URL.Path)
		assert.Contains(t, r.Header.Get("Cookie"), "ory_session_")
		assert.Contains(t, r.Header.Get("User-Agent"), "Mozilla/5.0")
		_, _ = fmt.Fprintf(w, `<script>self.__next_f.push([1,"\"budget\":{\"usage_percentage\":0,\"initial_budget\":0,\"currency\":\"USD\",\"reset_at\":\"2026-12-01T00:00:00Z\",\"api_budget\":{\"usage_percentage\":%v,\"initial_budget\":%v,\"currency\":\"USD\",\"reset_at\":\"2026-12-01T00:00:00Z\"},\"vibe_budget\":{\"usage_percentage\":%v,\"initial_budget\":%v,\"currency\":\"USD\",\"reset_at\":\"2026-12-01T00:00:00Z\"}}"])</script>`,
			apiPercentage, apiLimit, vibePercentage, vibeLimit)
	}))
	t.Cleanup(server.Close)
	return server
}

func setMistralQuotaEnv(t *testing.T, serverURL string, accounts string) {
	t.Helper()
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_QUOTA_ADMIN_BASE_URL", serverURL)
	t.Setenv("MISTRAL_VIBE_QUOTA_BASE_URL", serverURL)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", accounts)
}

func TestMistralBalanceUsesTheAllowanceOfTheChannelKey(t *testing.T) {
	setupMistralBalanceTest(t)
	server := newMistralSubscriptionServer(t, 60, 15, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","studioKey":"mstrl_studio"}]`)

	studio := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_studio", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(studio).Error)
	result, err := updateChannelBalance(studio)
	require.NoError(t, err)
	assert.InDelta(t, 6.0, result.Balance, 1e-9)
	assert.InDelta(t, 6.0, studio.Balance, 1e-9)

	vibe := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_vibe", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(vibe).Error)
	result, err = updateChannelBalance(vibe)
	require.NoError(t, err)
	assert.InDelta(t, 112.5, result.Balance, 1e-9)
}

func TestMistralBalanceFallsBackToTheVibeAllowance(t *testing.T) {
	setupMistralBalanceTest(t)
	server := newMistralSubscriptionServer(t, 60, 15, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","studioKey":"mstrl_studio"}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_unlisted", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	result, err := updateChannelBalance(channel)

	require.NoError(t, err)
	assert.InDelta(t, 112.5, result.Balance, 1e-9)
}

func TestMistralBalanceRejectsUnknownChannel(t *testing.T) {
	setupMistralBalanceTest(t)
	setMistralQuotaEnv(t, "http://127.0.0.1:1", `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (other)"}

	_, err := updateChannelBalance(channel)

	require.ErrorContains(t, err, `no Mistral quota account is configured for channel "Mistral (other)"`)
}
