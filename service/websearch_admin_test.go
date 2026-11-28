package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/stretchr/testify/require"
)

func TestToolsAdminDiagnosticsAreReadOnly(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	corrupt := "{broken"
	channel := model.Channel{Id: 98, Name: "diagnostic fixture", Type: constant.ChannelTypeDeepSeek, Key: "synthetic-key", Status: common.ChannelStatusEnabled, Models: constant.ToolsSearchModel, Group: "vip", Setting: &corrupt}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "vip", Model: constant.ToolsSearchModel, ChannelId: channel.Id, Enabled: true}).Error)
	model.InitChannelCache()

	summary, err := WebSearchProviderSummaries(t.Context())
	require.NoError(t, err)
	for _, provider := range summary {
		if provider.Provider == websearch_setting.ProviderDeepSeek {
			require.Equal(t, 1, provider.AvailableChannels)
			require.Equal(t, toolsContracts, provider.Contracts)
			require.Equal(t, map[string]int{"vip": 1}, provider.ContractDetails[0].AvailableByGroup)
		}
	}
	result, err := SimulateWebSearchRouting(WebSearchSimulationInput{Group: "vip", Contract: abstract.ContractToolsSearch, ChannelID: channel.Id})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.Status)
	require.Equal(t, channel.Id, result.ChannelID)
	require.NotEmpty(t, result.Candidates)
	require.Equal(t, websearch_setting.ProviderDeepSeek, result.Candidates[0].Provider)

	var stored model.Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	require.NotNil(t, stored.Setting)
	require.Equal(t, corrupt, *stored.Setting)
	require.True(t, result.Preflight)
	require.False(t, result.AuthorizationVerified)
	require.False(t, result.ChannelIsSample)
}

func TestToolsSimulationListsFirstAttemptChannelsAndRejections(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	applyWebSearchGroupPolicies(t, `{}`)

	for _, fixture := range []struct {
		id       int
		priority int64
		weight   uint
		enabled  bool
	}{
		{101, 10, 20, true}, {102, 10, 35, true}, {103, 10, 0, true},
		{104, 5, 50, true}, {105, 20, 50, false},
	} {
		channel := model.Channel{Id: fixture.id, Type: constant.ChannelTypeDeepSeek, Key: "synthetic-key", Status: common.ChannelStatusEnabled, Priority: &fixture.priority, Weight: &fixture.weight}
		ability := model.Ability{Group: "vip", Model: constant.ToolsSearchModel, ChannelId: fixture.id, Enabled: fixture.enabled, Priority: &fixture.priority, Weight: fixture.weight}
		require.NoError(t, db.Create(&channel).Error)
		require.NoError(t, db.Create(&ability).Error)
	}
	input := WebSearchSimulationInput{Group: "vip", Contract: abstract.ContractToolsSearch}
	cached, err := SimulateWebSearchRouting(input)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, cached.Status)
	require.True(t, cached.ChannelIsSample)
	require.Equal(t, 101, cached.ChannelID)
	require.Len(t, cached.Channels, 4)
	require.Equal(t, []bool{true, true, false, false}, []bool{cached.Channels[0].FirstAttempt, cached.Channels[1].FirstAttempt, cached.Channels[2].FirstAttempt, cached.Channels[3].FirstAttempt})
	require.Contains(t, cached.Eligibility, WebSearchSimulationEligibility{Group: "vip", ChannelID: 105, Provider: websearch_setting.ProviderDeepSeek, Origin: "native_channel", Reason: abstract.EligibilityNoAbility})

	common.MemoryCacheEnabled = false
	uncached, err := SimulateWebSearchRouting(input)
	require.NoError(t, err)
	require.True(t, uncached.Channels[2].FirstAttempt)
	require.False(t, uncached.Channels[3].FirstAttempt)
}

func TestToolsSimulationPreferenceDoesNotHideOtherEligibleChannels(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	applyWebSearchGroupPolicies(t, `{"vip":{"tools/search":{"mode":"prefer","providers":["moonshot"]}}}`)
	priority := int64(10)
	channels := []model.Channel{
		{Id: 111, Type: constant.ChannelTypeDeepSeek, Key: "synthetic-key", Status: common.ChannelStatusEnabled, Priority: &priority},
		{Id: 112, Type: constant.ChannelTypeMoonshot, Key: "synthetic-key", Status: common.ChannelStatusEnabled},
	}
	require.NoError(t, db.Create(&channels).Error)
	for _, channel := range channels {
		require.NoError(t, db.Create(&model.Ability{Group: "vip", Model: constant.ToolsSearchModel, ChannelId: channel.Id, Enabled: true}).Error)
	}
	input := WebSearchSimulationInput{Group: "vip", Contract: abstract.ContractToolsSearch}
	result, err := SimulateWebSearchRouting(input)
	require.NoError(t, err)
	require.Equal(t, "group_prefer", result.Source)
	require.Equal(t, 112, result.ChannelID)
	require.Len(t, result.Channels, 2)
	require.Equal(t, 111, result.Channels[1].ChannelID)
	require.False(t, result.Channels[1].FirstAttempt)
}

func TestBuiltinToolsSelectionAndSimulationAgreeOnExactModelBeforePriority(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	applyWebSearchGroupPolicies(t, `{}`)
	const exact = "gpt-4o-gizmo-fixture"
	const normalized = "gpt-4o-gizmo-*"
	require.Equal(t, []string{exact, normalized}, toolsRoutingModels(exact))
	low, high := int64(1), int64(9)
	channels := []model.Channel{
		{Id: 121, Type: constant.ChannelTypeCodex, Key: "synthetic-key", Status: common.ChannelStatusEnabled, Priority: &low},
		{Id: 122, Type: constant.ChannelTypeCodex, Key: "synthetic-key", Status: common.ChannelStatusEnabled, Priority: &high},
	}
	require.NoError(t, db.Create(&channels).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: exact, ChannelId: 121, Enabled: true, Priority: &low},
		{Group: "vip", Model: normalized, ChannelId: 121, Enabled: true, Priority: &high},
		{Group: "vip", Model: normalized, ChannelId: 122, Enabled: true, Priority: &high},
	}).Error)
	model.InitChannelCache()
	input := WebSearchSimulationInput{Group: "vip", Contract: abstract.ContractAlphaSearch, Model: exact}
	policy := webSearchGroupPolicy{contract: string(input.Contract), settings: websearch_setting.Get()}
	for _, memory := range []bool{true, false} {
		common.MemoryCacheEnabled = memory
		result, err := SimulateWebSearchRouting(input)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, result.Status)
		require.Len(t, result.Channels, 2)
		require.Equal(t, 121, result.ChannelID)
		require.Equal(t, exact, result.Channels[0].MatchedModel)
		require.True(t, result.Channels[0].FirstAttempt)
		require.False(t, result.Channels[1].FirstAttempt)
		channel, err := policy.selectChannel(input.Group, exact, 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		require.Equal(t, result.ChannelID, channel.Id)
	}
}

func TestToolsSimulationDiagnosesUnsupportedEndpoint(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	applyWebSearchGroupPolicies(t, `{"vip":{"tools/search":{"mode":"only","providers":["zhipu"]}}}`)
	baseURL := "https://api.z.ai/api/paas/v4"
	channel := model.Channel{Id: 113, Type: constant.ChannelTypeZhipu_v4, Key: "synthetic-key", Status: common.ChannelStatusEnabled, BaseURL: &baseURL}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "vip", Model: constant.ToolsSearchModel, ChannelId: channel.Id, Enabled: true}).Error)
	result, err := SimulateWebSearchRouting(WebSearchSimulationInput{Group: "vip", Contract: abstract.ContractToolsSearch, ChannelID: channel.Id})
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, result.Status)
	require.Contains(t, result.Eligibility, WebSearchSimulationEligibility{Group: "vip", ChannelID: channel.Id, Provider: websearch_setting.ProviderZhipu, Origin: "channel_upstream", Reason: abstract.EligibilityUnsupported})
}

func TestToolsCodexDirectorySeparatesNativeAlphaAndMapping(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	channel := model.Channel{Id: 99, Type: constant.ChannelTypeCodex, Key: "synthetic-key", Status: common.ChannelStatusEnabled, Models: "alpha-model,tools-search", Group: "vip"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "vip", Model: "alpha-model", ChannelId: channel.Id, Enabled: true},
		{Group: "vip", Model: constant.ToolsSearchModel, ChannelId: channel.Id, Enabled: true},
	}).Error)
	model.InitChannelCache()
	summary, err := WebSearchProviderSummaries(t.Context())
	require.NoError(t, err)
	var codex WebSearchProviderSummary
	for _, provider := range summary {
		if provider.Provider == websearch_setting.ProviderCodex {
			codex = provider
		}
	}
	require.Equal(t, 1, codex.AvailableChannels)
	require.Equal(t, 1, codex.MissingMappingChannels)
	result, err := SimulateWebSearchRouting(WebSearchSimulationInput{Group: "vip", Contract: abstract.ContractAlphaSearch, Model: "alpha-model", ChannelID: channel.Id})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.Status)
	require.True(t, result.Candidates[0].Native)
}
