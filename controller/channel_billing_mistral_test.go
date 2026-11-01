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

func setupMistralVibeBalanceTest(t *testing.T) {
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

func newMistralVibeUsageServer(t *testing.T, percentage string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/code-trpc/apiKey.getUsage", r.URL.Path)
		assert.Contains(t, r.Header.Get("Cookie"), "ory_session_")
		assert.Contains(t, r.Header.Get("User-Agent"), "Mozilla/5.0")
		_, _ = fmt.Fprintf(w, `[{"result":{"data":{"json":{"usagePercentage":%s,"paygEnabled":false,"resetAt":"2026-12-01T00:00:00Z"}}}}]`, percentage)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestMistralVibeBalanceUsesTheMatchingAccount(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	server := newMistralVibeUsageServer(t, "25")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_BASE_URL", server.URL)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", `[{"channel":"Mistral Vibe (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":100}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral Vibe (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	result, err := updateChannelBalance(channel)

	require.NoError(t, err)
	assert.InDelta(t, 75.0, result.Balance, 1e-9)
	assert.InDelta(t, 75.0, channel.Balance, 1e-9)
}

func TestMistralVibeBalanceRejectsUnknownChannel(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", `[{"channel":"Mistral Vibe (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":100}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral Vibe (other)"}

	_, err := updateChannelBalance(channel)

	require.ErrorContains(t, err, `no Mistral Vibe account is configured for channel "Mistral Vibe (other)"`)
}

func TestMistralVibeBalanceRequiresMonthlyLimit(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", `[{"channel":"Mistral Vibe (pro)","cookie":"ory_session_x=1"}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral Vibe (pro)"}

	_, err := updateChannelBalance(channel)

	require.ErrorContains(t, err, "has no monthlyLimitUsd")
}
