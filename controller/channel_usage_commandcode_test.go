package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestNewCommandCodeUsageAccountMapsCreditsAndWindows(t *testing.T) {
	credits := commandCodeCredits{}
	credits.Credits.MonthlyCredits = 25
	credits.Credits.PurchasedCredits = 10
	credits.Credits.FreeCredits = 3.4
	credits.WindowLimits.FiveHour = commandCodeCreditWindow{Used: 1.2, Cap: 5, ResetAt: 1793551234000}
	credits.WindowLimits.Weekly = commandCodeCreditWindow{Used: 8, Cap: 30, ResetAt: 1793600000123}

	account := newCommandCodeUsageAccount(credits)

	assert.InDelta(t, 38.4, account.RemainingUSD, 1e-9)
	assert.Equal(t, 25.0, account.MonthlyCredits)
	assert.Equal(t, 10.0, account.PurchasedCredits)
	assert.InDelta(t, 3.4, account.FreeCredits, 1e-9)
	require.Len(t, account.Windows, 2)
	assert.Equal(t, commandCodeUsageWindow{Name: "5h", UsedUSD: 1.2, CapUSD: 5, ResetAt: 1793551234}, account.Windows[0])
	assert.Equal(t, commandCodeUsageWindow{Name: "weekly", UsedUSD: 8, CapUSD: 30, ResetAt: 1793600000}, account.Windows[1])
}

func TestNewCommandCodeUsageWindowWithoutResetStaysZero(t *testing.T) {
	window := newCommandCodeUsageWindow(commandCodeWindowWeekly, commandCodeCreditWindow{Used: 2, Cap: 35})

	assert.Equal(t, int64(0), window.ResetAt)
	assert.Equal(t, 2.0, window.UsedUSD)
	assert.Equal(t, 35.0, window.CapUSD)
}

func TestIsCommandCodeUsageChannel(t *testing.T) {
	commandCode := &model.Channel{Type: constant.ChannelTypeOpenAI}
	commandCode.SetSetting(dto.ChannelSettings{BalanceSource: dto.BalanceSourceCommandCode})
	disabled := &model.Channel{Type: constant.ChannelTypeOpenAI, Status: 2}
	disabled.SetSetting(dto.ChannelSettings{BalanceSource: dto.BalanceSourceCommandCode})

	assert.True(t, isCommandCodeUsageChannel(commandCode))
	assert.True(t, isCommandCodeUsageChannel(disabled))
	assert.False(t, isCommandCodeUsageChannel(&model.Channel{Type: constant.ChannelTypeOpenAI}))
	assert.False(t, isCommandCodeUsageChannel(&model.Channel{Type: constant.ChannelTypeMistral}))
}

func newCommandCodeUsageRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/channel/:id/commandcode/usage", GetCommandCodeChannelUsage)
	return router
}

func getCommandCodeUsage(t *testing.T, router *gin.Engine, id int) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/channel/%d/commandcode/usage", id), nil))
	require.Equal(t, http.StatusOK, response.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body
}

func newCommandCodeUsageChannel(t *testing.T, server *httptest.Server, keys string) *model.Channel {
	t.Helper()
	baseURL := server.URL + "/provider/v1"
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "commandcode", Key: keys, BaseURL: &baseURL}
	channel.SetSetting(dto.ChannelSettings{BalanceSource: dto.BalanceSourceCommandCode})
	require.NoError(t, model.DB.Create(channel).Error)
	return channel
}

func TestGetCommandCodeChannelUsageListsEveryKeyWithoutLeakingIt(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/alpha/billing/credits", r.URL.Path)
		assert.True(t, strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "))
		_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":25,"purchasedCredits":10,"freeCredits":3.4},"windowLimits":{"fiveHour":{"used":1.2,"cap":5,"resetAt":1793551234000},"weekly":{"used":8,"cap":30}}}`))
	}))
	t.Cleanup(server.Close)
	channel := newCommandCodeUsageChannel(t, server, "first-secret-key\nsecond-secret-key")

	body := getCommandCodeUsage(t, newCommandCodeUsageRouter(), channel.Id)

	require.Equal(t, true, body["success"])
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "first-secret-key")
	assert.NotContains(t, string(raw), "second-secret-key")
	accounts := body["data"].(map[string]any)["accounts"].([]any)
	require.Len(t, accounts, 2)
	first := accounts[0].(map[string]any)
	assert.Equal(t, 25.0, first["monthly_credits"])
	assert.Equal(t, 10.0, first["purchased_credits"])
	assert.InDelta(t, 3.4, first["free_credits"], 1e-9)
	windows := first["windows"].([]any)
	require.Len(t, windows, 2)
	assert.Equal(t, map[string]any{"name": "5h", "used_usd": 1.2, "cap_usd": 5.0, "reset_at": 1793551234.0}, windows[0])
	assert.Equal(t, map[string]any{"name": "weekly", "used_usd": 8.0, "cap_usd": 30.0, "reset_at": 0.0}, windows[1])
}

func TestGetCommandCodeChannelUsageSkipsAFailingKey(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.Header.Get("Authorization"), "bad-key") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":5}}`))
	}))
	t.Cleanup(server.Close)
	channel := newCommandCodeUsageChannel(t, server, "bad-key\ngood-key")

	body := getCommandCodeUsage(t, newCommandCodeUsageRouter(), channel.Id)

	require.Equal(t, true, body["success"])
	accounts := body["data"].(map[string]any)["accounts"].([]any)
	require.Len(t, accounts, 1)
	assert.Equal(t, 5.0, accounts[0].(map[string]any)["monthly_credits"])
}

func TestGetCommandCodeChannelUsageReportsUpstreamFailure(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	channel := newCommandCodeUsageChannel(t, server, "secret-key")

	body := getCommandCodeUsage(t, newCommandCodeUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Contains(t, body["message"], "status code: 401")
}

func TestGetCommandCodeChannelUsageRejectsOtherBalanceSources(t *testing.T) {
	setupMistralVibeBalanceTest(t)
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Name: "openai", Key: "sk-test"}
	require.NoError(t, model.DB.Create(channel).Error)

	body := getCommandCodeUsage(t, newCommandCodeUsageRouter(), channel.Id)

	assert.Equal(t, false, body["success"])
	assert.Equal(t, "channel balance source is not Command Code", body["message"])
}
