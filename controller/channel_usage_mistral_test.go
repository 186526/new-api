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

func TestNewMistralUsageReportsTheMergedAllowance(t *testing.T) {
	quota := &service.MistralQuota{
		Channel: "Mistral (pro)",
		Allowance: &service.MistralBudget{
			UsagePercentage: 65.79,
			LimitUSD:        150,
			ResetAt:         time.Unix(1796083200, 0),
		},
		PaygEnabled: false,
		FetchedAt:   time.Unix(1793550000, 0),
	}

	usage := newMistralUsage(service.MistralQuotaAccount{Channel: "Mistral (pro)"}, quota)

	assert.Equal(t, "Mistral (pro)", usage.ChannelName)
	require.Len(t, usage.Budgets, 1, "Vibe and Studio share one allowance, so the dialog reports one row")

	merged := usage.Budgets[0]
	assert.Equal(t, service.MistralBudgetScopeVibe, merged.Scope)
	assert.Equal(t, 65.79, merged.UsagePercentage)
	assert.InDelta(t, 0.3421, merged.RemainingRatio, 1e-9)
	assert.Equal(t, 150.0, merged.LimitUSD)
	assert.InDelta(t, 51.315, merged.RemainingUSD, 1e-9)
	assert.Equal(t, int64(1796083200), merged.ResetAt)

	assert.False(t, usage.PaygEnabled)
	assert.Equal(t, int64(1793550000), usage.FetchedAt)
}

func TestNewMistralUsageWithoutAnAllowanceReportsNoRows(t *testing.T) {
	quota := &service.MistralQuota{
		FetchedAt: time.Unix(100, 0),
	}

	usage := newMistralUsage(service.MistralQuotaAccount{Channel: "Mistral (pro)"}, quota)

	assert.Empty(t, usage.Budgets)
}

func TestNewMistralUsageWithoutPlanAmountKeepsZeroDollars(t *testing.T) {
	quota := &service.MistralQuota{
		Allowance: &service.MistralBudget{UsagePercentage: 25},
		FetchedAt: time.Unix(100, 0),
	}

	usage := newMistralUsage(service.MistralQuotaAccount{Channel: "Mistral (pro)"}, quota)

	require.Len(t, usage.Budgets, 1)
	assert.Equal(t, 0.0, usage.Budgets[0].LimitUSD)
	assert.Equal(t, 0.0, usage.Budgets[0].RemainingUSD)
	assert.InDelta(t, 0.75, usage.Budgets[0].RemainingRatio, 1e-9)
	assert.Equal(t, int64(0), usage.Budgets[0].ResetAt)
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

func TestGetMistralChannelUsageReturnsTheMergedAllowance(t *testing.T) {
	setupMistralBalanceTest(t)
	server := newMistralConsoleServer(t, 25, 150)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","vibeKey":"mstrl_vibe","studioKey":"mstrl_studio"}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_studio", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	status, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	assert.Equal(t, "Mistral (pro)", data["channel_name"])
	budgets := data["budgets"].([]any)
	require.Len(t, budgets, 1, "Vibe and Studio share one allowance, so the dialog reports one row")

	merged := budgets[0].(map[string]any)
	assert.Equal(t, service.MistralBudgetScopeVibe, merged["scope"])
	assert.Equal(t, 25.0, merged["usage_percentage"])
	assert.InDelta(t, 0.75, merged["remaining_ratio"], 1e-9)
	assert.Equal(t, 150.0, merged["limit_usd"])
	assert.InDelta(t, 112.5, merged["remaining_usd"], 1e-9)
	assert.Equal(t, float64(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).Unix()), merged["reset_at"])
	assert.NotZero(t, data["fetched_at"])
}

func TestGetMistralChannelUsageRejectsOtherChannelTypes(t *testing.T) {
	setupMistralBalanceTest(t)
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "sk-test", Name: "openai"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Equal(t, "channel type is not Mistral", body["message"])
}

func TestGetMistralChannelUsageWithoutAccount(t *testing.T) {
	setupMistralBalanceTest(t)
	setMistralQuotaEnv(t, "http://127.0.0.1:1", `[{"channel":"other","cookie":"ory_session_x=1"}]`)
	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Equal(t, `no Mistral quota account is configured for channel "Mistral (pro)"`, body["message"])
}

// TestGetMistralChannelUsageReadsTheChannelKey requires the usage dialog of a
// channel to read the allowance with the key the channel carries. The accounts
// file names a key of the free plan (100% of 10 USD), which is what the dialog
// reported while the channel's own key still had about 148.14 USD of 150.
func TestGetMistralChannelUsageReadsTheChannelKey(t *testing.T) {
	setupMistralBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/billing/budget" {
			if r.Header.Get("Authorization") == "Bearer mstrl_free" {
				_, _ = w.Write([]byte(`{"vibe_budget":{"usage_percentage":100,"initial_budget":10}}`))
				return
			}
			_, _ = w.Write([]byte(`{"vibe_budget":{"usage_percentage":1.238795712,"initial_budget":150,"reset_at":"2026-12-01T00:00:00Z"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral Vibe (xu.bo)","cookie":"ory_session_x=stale","vibeKey":"mstrl_free","monthlyLimitUsd":150}]`)

	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_channel", Name: "Mistral Vibe (xu.bo)"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	budgets := data["budgets"].([]any)
	require.Len(t, budgets, 1)
	merged := budgets[0].(map[string]any)
	assert.InDelta(t, 1.238795712, merged["usage_percentage"], 1e-9)
	assert.InDelta(t, 150.0, merged["limit_usd"], 1e-9)
	assert.InDelta(t, 148.14, merged["remaining_usd"], 0.01)
}

func TestGetMistralChannelUsageReportsRejectedSession(t *testing.T) {
	setupMistralBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	setMistralQuotaEnv(t, server.URL, `[{"channel":"Mistral (pro)","cookie":"ory_session_x=1","monthlyLimitUsd":150}]`)
	channel := &model.Channel{Type: constant.ChannelTypeMistral, Key: "mstrl_key", Name: "Mistral (pro)"}
	require.NoError(t, model.DB.Create(channel).Error)

	_, body := getMistralUsage(t, newMistralUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "rejected the credential")
}

func TestGetMistralChannelUsageForMissingChannel(t *testing.T) {
	setupMistralBalanceTest(t)

	_, body := getMistralUsage(t, newMistralUsageRouter(), 999)

	assert.Equal(t, false, body["success"])
	assert.Equal(t, "channel not found", body["message"])
}
