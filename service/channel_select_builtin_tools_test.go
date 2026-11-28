package service

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuiltinToolsSelectionAndSimulation(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	model.InitChannelCache()
	const optionKey = "websearch_setting.builtin_fallback_enabled"
	previous := websearch_setting.Get().BuiltinFallbackEnabled
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{optionKey: strconv.FormatBool(previous)}))
		require.NoError(t, websearch_setting.UpdateAndSync())
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{optionKey: "true"}))
	require.NoError(t, websearch_setting.UpdateAndSync())
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default"]`))
	applyWebSearchGroupPolicies(t, `{"vip":{"tools/fetch":{"mode":"disabled"}},"default":{"tools/fetch":{"mode":"only","providers":["local"]}}}`)

	for _, fixture := range []struct {
		name      string
		group     string
		configure func(*gin.Context)
		status    int
	}{
		{name: "auto skips disabled group", group: "auto", status: http.StatusOK},
		{name: "explicit group", group: "default", status: http.StatusOK},
		{name: "disabled group", group: "vip", status: http.StatusForbidden},
		{name: "empty token auto list", group: "auto", configure: func(ctx *gin.Context) {
			common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{})
		}, status: http.StatusForbidden},
		{name: "auth required", group: "default", configure: func(ctx *gin.Context) {
			common.SetContextKey(ctx, constant.ContextKeyTokenId, 0)
		}, status: http.StatusServiceUnavailable},
		{name: "strict session", group: "default", configure: func(ctx *gin.Context) {
			RequestPolicy(ctx).SessionMode = "strict"
		}, status: http.StatusServiceUnavailable},
		{name: "plugin filter", group: "default", configure: func(ctx *gin.Context) {
			GetChannelConstraints(ctx).AddFilter(dto.ChannelFilter{Kind: dto.FilterTaskPluginIdentity, TaskPluginKey: "fixture"})
		}, status: http.StatusServiceUnavailable},
		{name: "fixed channel", group: "default", configure: func(ctx *gin.Context) {
			GetChannelConstraints(ctx).AddPin(dto.ChannelPin{ChannelId: 9876})
		}, status: http.StatusBadRequest},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/tools/fetch", nil)
			common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(ctx, constant.ContextKeyTokenId, 10)
			if fixture.configure != nil {
				fixture.configure(ctx)
			}
			channel, group, selErr := SelectChannelForRequest(ctx, constant.ToolsFetchModel, &RetryParam{
				Ctx: ctx, TokenGroup: fixture.group, ModelName: constant.ToolsFetchModel,
			})
			require.Nil(t, channel)
			builtinGroup, builtin := BuiltinToolsGroup(ctx)
			if fixture.status != http.StatusOK {
				require.NotNil(t, selErr)
				require.Equal(t, fixture.status, selErr.StatusCode)
				require.False(t, builtin)
				return
			}
			require.Nil(t, selErr)
			require.Equal(t, "default", group)
			require.Equal(t, group, builtinGroup)
			require.True(t, builtin)
			result, err := SimulateWebSearchRouting(WebSearchSimulationInput{Group: fixture.group, Contract: abstract.ContractToolsFetch})
			require.NoError(t, err)
			require.Equal(t, group, result.Group)
			require.Equal(t, http.StatusOK, result.Status)
			require.Zero(t, result.ChannelID)
			require.Equal(t, "builtin_fallback", result.Source)
			require.Equal(t, []WebSearchSimulationCandidate{{Provider: "local", Origin: "local"}}, result.Candidates)
		})
	}
	createChannelSelectAutoGroupsChannel(t, db, 2002, "default", constant.ToolsFetchModel)
	model.InitChannelCache()
	result, err := SimulateWebSearchRouting(WebSearchSimulationInput{Group: "auto", Contract: abstract.ContractToolsFetch})
	require.NoError(t, err)
	require.Equal(t, 2002, result.ChannelID)
}
