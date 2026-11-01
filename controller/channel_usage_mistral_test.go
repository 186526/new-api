package controller

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

func TestNewMistralVibeUsageDerivesAmountsFromThePlan(t *testing.T) {
	quota := &service.MistralVibeQuota{
		Channel:         "Mistral Vibe (pro)",
		UsagePercentage: 12.34,
		ResetAt:         time.Unix(1796083200, 0),
		PaygEnabled:     false,
		FetchedAt:       time.Unix(1793550000, 0),
	}

	usage := newMistralVibeUsage(service.MistralVibeAccount{Channel: "Mistral Vibe (pro)", MonthlyLimitUSD: 150}, quota)

	assert.Equal(t, "Mistral Vibe (pro)", usage.ChannelName)
	assert.Equal(t, 12.34, usage.UsagePercentage)
	assert.InDelta(t, 0.8766, usage.RemainingRatio, 1e-9)
	assert.Equal(t, 150.0, usage.MonthlyLimitUSD)
	assert.InDelta(t, 131.49, usage.RemainingUSD, 1e-9)
	assert.Equal(t, int64(1796083200), usage.ResetAt)
	assert.False(t, usage.PaygEnabled)
	assert.Equal(t, int64(1793550000), usage.FetchedAt)
}

func TestNewMistralVibeUsageWithoutPlanAmountKeepsZeroDollars(t *testing.T) {
	quota := &service.MistralVibeQuota{Channel: "Mistral Vibe (pro)", UsagePercentage: 25, FetchedAt: time.Unix(100, 0)}

	usage := newMistralVibeUsage(service.MistralVibeAccount{Channel: "Mistral Vibe (pro)"}, quota)

	assert.Equal(t, 0.0, usage.MonthlyLimitUSD)
	assert.Equal(t, 0.0, usage.RemainingUSD)
	assert.InDelta(t, 0.75, usage.RemainingRatio, 1e-9)
	assert.Equal(t, int64(0), usage.ResetAt)
}

func TestClampRatio(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "half", in: 0.5, want: 0.5},
		{name: "overdrawn stays at zero", in: -0.2, want: 0},
		{name: "above one stays at one", in: 1.2, want: 1},
		{name: "not a number becomes zero", in: math.NaN(), want: 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.want, clampRatio(testCase.in))
		})
	}
}

func TestUnixSeconds(t *testing.T) {
	assert.Equal(t, int64(0), unixSeconds(time.Time{}))
	assert.Equal(t, int64(1796083200), unixSeconds(time.Unix(1796083200, 0)))
}

func newMistralUsageRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/channel/:id/mistral/usage", GetMistralChannelUsage)
	return router
}

func getMistralUsage(t *testing.T, router *gin.Engine, id int) (int, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channel/%d/mistral/usage", id), nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return response.Code, body
}

func TestGetMistralChannelUsageWithoutPlanAmountReturnsThePercentage(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	server := newMistralVibeUsageServer(t, "25")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_BASE_URL", server.URL)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", `[{"channel":"Mistral Vibe (pro)","cookie":"ory_session_x=1"}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral Vibe (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	status, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	assert.Equal(t, "Mistral Vibe (pro)", data["channel_name"])
	assert.Equal(t, 25.0, data["usage_percentage"])
	assert.InDelta(t, 0.75, data["remaining_ratio"], 1e-9)
	assert.Equal(t, 0.0, data["monthly_limit_usd"])
	assert.Equal(t, 0.0, data["remaining_usd"])
	assert.Equal(t, float64(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).Unix()), data["reset_at"])
	assert.NotZero(t, data["fetched_at"])
}

func TestGetMistralChannelUsageRejectsOtherChannelTypes(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "sk-test", Name: "openai"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Equal(t, "channel type is not Mistral", body["message"])
}

func TestGetMistralChannelUsageWithoutAccount(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", `[{"channel":"other","cookie":"ory_session_x=1"}]`)
	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral Vibe (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Equal(t, `no Mistral Vibe account is configured for channel "Mistral Vibe (pro)"`, body["message"])
}

func TestGetMistralChannelUsageReportsRejectedSession(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS_FILE", "")
	t.Setenv("MISTRAL_VIBE_QUOTA_BASE_URL", server.URL)
	t.Setenv("MISTRAL_VIBE_QUOTA_ACCOUNTS", `[{"channel":"Mistral Vibe (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)
	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral Vibe (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "session was rejected")
}

func TestGetMistralChannelUsageForMissingChannel(t *testing.T) {
	setupMistralVibeBalanceTest(t)

	_, body := getMistralUsage(t, newMistralUsageRouter(), 999)

	assert.Equal(t, false, body["success"])
	assert.Equal(t, "channel not found", body["message"])
}
