package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRelayBuiltinToolsWithoutChannels(t *testing.T) {
	user, token := setupResponsesWSRequestTest(t)
	const startQuota = 25000
	require.NoError(t, model.DB.Model(user).Update("quota", startQuota).Error)
	require.NoError(t, model.DB.Model(token).Update("remain_quota", startQuota).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.UserSubscription{}))
	require.NoError(t, model.LOG_DB.AutoMigrate(&model.Log{}))

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/search" || r.Header.Get("x-api-key") != "fixture" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected upstream path or credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"url":"https://example.org/result","title":"Fixture","text":"Search result"}]}`)
	}))
	defer server.Close()
	previous := websearch_setting.Get()
	load := func(options map[string]string) {
		require.NoError(t, config.GlobalConfig.LoadFromDB(options))
		require.NoError(t, websearch_setting.UpdateAndSync())
	}
	t.Cleanup(func() {
		load(map[string]string{
			"websearch_setting.builtin_fallback_enabled": "false",
			"websearch_setting.exa_api_key":              previous.ExaAPIKey,
			"websearch_setting.exa_base_url":             previous.ExaBaseURL,
			websearch_setting.GroupPoliciesOptionKey:     previous.GroupPolicies,
		})
	})
	load(map[string]string{
		"websearch_setting.builtin_fallback_enabled": "true",
		"websearch_setting.exa_api_key":              "fixture",
		"websearch_setting.exa_base_url":             server.URL,
		websearch_setting.GroupPoliciesOptionKey:     `{"default":{"tools/search":{"mode":"only","providers":["exa"]}}}`,
	})

	engine := gin.New()
	engine.Use(middleware.BodyStorageCleanup())
	engine.POST("/v1/tools/search", middleware.TokenAuth(), middleware.Distribute(), func(c *gin.Context) {
		Relay(c, types.RelayFormatDeepSeekTools)
	})
	send := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/v1/tools/search", strings.NewReader(`{"query":"builtin fixture search"}`))
		request.Header.Set("Authorization", "Bearer sk-"+token.Key)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	response := send()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "https://example.org/result")
	require.EqualValues(t, 1, calls.Load())
	var usage model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).First(&usage).Error)
	require.Equal(t, constant.ToolsSearchModel, usage.ModelName)
	require.Equal(t, "default", usage.Group)
	require.Zero(t, usage.ChannelId)
	require.Equal(t, 5000, usage.Quota)
	require.NoError(t, model.DB.First(user, user.Id).Error)
	require.NoError(t, model.DB.First(token, token.Id).Error)
	require.Equal(t, startQuota-usage.Quota, user.Quota)
	require.Equal(t, startQuota-usage.Quota, token.RemainQuota)

	require.NoError(t, model.DB.Model(token).Update("remain_quota", 1).Error)
	response = send()
	require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	require.EqualValues(t, 1, calls.Load())

	load(map[string]string{"websearch_setting.builtin_fallback_enabled": "false"})
	response = send()
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.EqualValues(t, 1, calls.Load())
}
