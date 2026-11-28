package websearch

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/stretchr/testify/require"
)

func TestRequesterDiscoveryBindsOnlyTheCandidatesConnection(t *testing.T) {
	settings := websearch_setting.WebSearchSetting{
		ExaAPIKey: "global-key", ExaBaseURL: "https://exa.example.test", CandidateOrder: "local,exa,deepseek",
	}
	ctx := abstract.WithRoutingPolicy(t.Context(), abstract.RoutingPolicy{Settings: settings})
	channel := abstract.ChannelContext{ChannelID: 11, ChannelType: constant.ChannelTypeDeepSeek, BaseURL: "https://channel.example.test", Key: "channel-key"}
	channel.Settings.Proxy = "http://proxy.example.test"
	channel.Settings.WebSearchFetchCacheTTLSeconds = -1
	query := request.Query{
		Contract: abstract.ContractToolsSearch, Text: "synthetic query", EndpointProvider: websearch_setting.ProviderDeepSeek,
		Endpoint: channel.BaseURL + "/v1/tools/search", Raw: []byte(`{"query":"synthetic query"}`), Model: "synthetic-model",
		Prepare: func(*http.Request) error {
			t.Fatal("discovery called the adaptor's header preparation")
			return nil
		},
	}
	search, err := requestSearch(ctx, channel, query)
	require.NoError(t, err)
	require.Len(t, search, 2)
	require.Equal(t, websearch_setting.ProviderExa, search[0].candidate.Provider)
	require.Equal(t, abstract.OriginGlobalCredential, search[0].candidate.Origin)
	require.Equal(t, abstract.ChannelContext{BaseURL: settings.ExaBaseURL, Key: settings.ExaAPIKey}, search[0].channel)
	require.Equal(t, request.Query{Contract: query.Contract, Text: query.Text}, search[0].query)
	require.NotNil(t, search[0].send)
	require.Equal(t, websearch_setting.ProviderDeepSeek, search[1].candidate.Provider)
	require.True(t, search[1].candidate.Native)
	require.Equal(t, channel, search[1].channel)
	require.Equal(t, query.Endpoint, search[1].query.Endpoint)
	require.Equal(t, query.Raw, search[1].query.Raw)
	require.Equal(t, query.Model, search[1].query.Model)
	require.NotNil(t, search[1].query.Prepare)

	channel.Settings.WebSearchProvider = websearch_setting.ProviderExa
	bound, err := requestSearch(ctx, channel, query)
	require.NoError(t, err)
	require.Len(t, bound, 2)
	require.Equal(t, abstract.OriginOperatorBinding, bound[0].candidate.Origin)
	require.Equal(t, channel, bound[0].channel)
	require.Equal(t, search[0].query, bound[0].query)

	query.Contract = abstract.ContractToolsFetch
	query.URL = "https://page.example.test"
	fetch, err := requestFetch(ctx, channel, query)
	require.NoError(t, err)
	require.Len(t, fetch, 3)
	local := fetch[1]
	require.Equal(t, websearch_setting.ProviderLocal, local.candidate.Provider)
	require.Equal(t, abstract.OriginLocal, local.candidate.Origin)
	var localChannel abstract.ChannelContext
	localChannel.Settings.WebSearchFetchCacheTTLSeconds = -1
	require.Equal(t, localChannel, local.channel)
	require.Equal(t, request.Query{Contract: query.Contract, Text: query.Text, URL: query.URL}, local.query)
}

func TestRequesterDiscoveryRespectsPolicyAndCodexPreparation(t *testing.T) {
	ctx := t.Context()
	channel := abstract.ChannelContext{
		ChannelType: constant.ChannelTypeCodex, Key: "synthetic-key",
		OnlyProviders: true, PreferredProviders: []string{websearch_setting.ProviderCodex},
	}
	query := request.Query{
		Contract: abstract.ContractToolsSearch, Text: "synthetic query", EndpointProvider: websearch_setting.ProviderCodex,
		Endpoint: "https://channel.example.test/backend-api/codex/alpha/search", Model: "synthetic-model",
		Prepare: func(*http.Request) error {
			t.Fatal("discovery called the adaptor's header preparation")
			return nil
		},
	}
	_, err := requestSearch(ctx, channel, query)
	require.ErrorIs(t, err, ErrUnsupportedChannel)
	channel.ModelMapping = `{"tools-search":"synthetic-model"}`
	items, err := requestSearch(ctx, channel, query)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.False(t, items[0].candidate.Native)
	require.Equal(t, query.Model, items[0].query.Model)
	query.EndpointProvider = websearch_setting.ProviderDeepSeek
	_, err = requestSearch(ctx, channel, query)
	require.ErrorIs(t, err, ErrUnsupportedChannel)

	query = request.Query{Contract: abstract.ContractToolsFetch, URL: "https://page.example.test"}
	ctx = abstract.WithRoutingPolicy(t.Context(), abstract.RoutingPolicy{})
	_, err = requestFetch(ctx, abstract.ChannelContext{}, query)
	require.ErrorIs(t, err, ErrUnsupportedChannel)
	settings := websearch_setting.WebSearchSetting{BuiltinFallbackEnabled: true}
	ctx = abstract.WithRoutingPolicy(t.Context(), abstract.RoutingPolicy{Settings: settings})
	items, err = requestFetch(ctx, abstract.ChannelContext{}, query)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, websearch_setting.ProviderLocal, items[0].candidate.Provider)
}
