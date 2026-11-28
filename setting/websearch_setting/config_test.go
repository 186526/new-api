package websearch_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveAndRestore stores the current configuration and puts it back, publishing
// it again because LoadFromDB only writes into the struct the manager holds.
func saveAndRestore(t *testing.T) {
	t.Helper()
	saved := config.GlobalConfig.ExportAllConfigs()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		_ = UpdateAndSync()
	})
}

// apply writes a setting the way the config manager does: into the struct, then
// through the same publish step the option handler calls.
func apply(t *testing.T, setting WebSearchSetting) error {
	t.Helper()
	webSearchSetting = setting
	return UpdateAndSync()
}

func TestWebSearchSettingDefaults(t *testing.T) {
	saveAndRestore(t)

	setting := Get()
	assert.True(t, setting.NativeFirst)
	assert.Equal(t, 600, setting.SearchCacheTTLSeconds)
	assert.Equal(t, 3600, setting.FetchCacheTTLSeconds)
	assert.Equal(t, 86400, setting.RedirectCacheTTLSeconds)
	assert.Equal(t, 3, setting.RetryAttempts)
	assert.Equal(t, "https://api.exa.ai", setting.ExaBaseURL)

	assert.Equal(t, 600, SearchCacheTTL())
	assert.Equal(t, 3600, FetchCacheTTL())
	assert.Equal(t, 86400, RedirectCacheTTL())
	assert.Equal(t, 3, RetryAttempts())
	assert.Equal(t, "https://api.exa.ai", ExaEndpoint())
}

func TestWebSearchSettingConfigToMapRoundTripsCandidateOrder(t *testing.T) {
	saveAndRestore(t)

	webSearchSetting.CandidateOrder = "local, exa"
	raw, err := config.ConfigToMap(&webSearchSetting)
	require.NoError(t, err)
	require.Equal(t, "local, exa", raw["candidate_order"])

	options := make(map[string]string, len(raw))
	for key, value := range raw {
		options["websearch_setting."+key] = value
	}
	require.NoError(t, config.GlobalConfig.LoadFromDB(options))
	webSearchSetting = *config.GlobalConfig.Get("websearch_setting").(*WebSearchSetting)
	require.NoError(t, UpdateAndSync())

	assert.Equal(t, "local, exa", Get().CandidateOrder)
	assert.Equal(t, []string{"local", "exa"}, ProviderOrder())
}

func TestProviderOrderRejectsUnknownAndDuplicateNames(t *testing.T) {
	saveAndRestore(t)

	require.NoError(t, apply(t, defaultSettingWith("  Local ,, EXA  ")))
	assert.Equal(t, []string{"local", "exa"}, ProviderOrder())
	require.ErrorContains(t, apply(t, defaultSettingWith("not-a-provider")), "not-a-provider")
	require.ErrorContains(t, apply(t, defaultSettingWith("EXA,exa")), "duplicate provider")
	assert.Equal(t, []string{"local", "exa"}, ProviderOrder())

	require.NoError(t, apply(t, defaultSettingWith("")))
	assert.Nil(t, ProviderOrder())
}

func TestUpdateAndSyncRejectsInvalidValuesAndKeepsServing(t *testing.T) {
	saveAndRestore(t)

	invalid := WebSearchSetting{
		CandidateOrder:               "exa",
		SearchCacheTTLSeconds:        defaultSearchCacheTTLSeconds,
		FetchCacheTTLSeconds:         defaultFetchCacheTTLSeconds,
		RedirectCacheTTLSeconds:      defaultRedirectCacheTTLSeconds,
		ResponseHeaderTimeoutSeconds: defaultResponseHeaderTimeoutSeconds,
		RetryAttempts:                11,
		RetryBaseDelayMilliseconds:   defaultRetryBaseDelayMilliseconds,
		ExaBaseURL:                   defaultExaBaseURL,
	}
	require.ErrorContains(t, apply(t, invalid), "retry_attempts")

	invalid.CandidateOrder = "not-a-provider"
	invalid.RetryAttempts = defaultRetryAttempts
	require.ErrorContains(t, apply(t, invalid), "not-a-provider")

	invalid.CandidateOrder = "exa"
	invalid.ResponseHeaderTimeoutSeconds = -1
	require.ErrorContains(t, apply(t, invalid), "response_header_timeout_seconds")

	assert.Equal(t, "", Get().CandidateOrder)
	assert.Equal(t, defaultResponseHeaderTimeoutSeconds, Get().ResponseHeaderTimeoutSeconds)
	assert.Equal(t, defaultRetryAttempts, Get().RetryAttempts)
}

func TestGlobalProvidersFollowsTheExaKey(t *testing.T) {
	saveAndRestore(t)

	setting := defaultSettingWith("")
	setting.ExaAPIKey = ""
	require.NoError(t, apply(t, setting))
	assert.Nil(t, GlobalProviders())

	setting.ExaAPIKey = "sk-exa"
	require.NoError(t, apply(t, setting))
	assert.Equal(t, []string{ProviderExa}, GlobalProviders())

	setting.ExaAPIKey = "   "
	require.NoError(t, apply(t, setting))
	assert.Nil(t, GlobalProviders())
}

func TestExaEndpointFallsBackAndTrimsTrailingSlash(t *testing.T) {
	saveAndRestore(t)

	for _, endpoint := range []string{"", "   "} {
		setting := defaultSettingWith("")
		setting.ExaBaseURL = endpoint
		require.NoError(t, apply(t, setting))
		assert.Equal(t, defaultExaBaseURL, ExaEndpoint())
	}

	setting := defaultSettingWith("")
	setting.ExaBaseURL = "https://exa.example/"
	require.NoError(t, apply(t, setting))
	assert.Equal(t, "https://exa.example", ExaEndpoint())
}

func TestWebSearchGroupPoliciesInvalidConfigKeepsLastSnapshot(t *testing.T) {
	saveAndRestore(t)

	const raw = `{"default":{"tools/search":{"mode":"prefer","providers":["exa"]}}}`
	setting := defaultSettingWith("")
	setting.GroupPolicies = raw
	require.NoError(t, apply(t, setting))
	setting.GroupPolicies = `{"default":{"tools/search":{"mode":null}}}`
	require.Error(t, apply(t, setting))

	assert.Equal(t, raw, Get().GroupPolicies)
	providers := Get().PreferredProviders("default", "tools/search")
	require.Equal(t, []string{"exa"}, providers)
	providers[0] = "deepseek"
	assert.Equal(t, []string{"exa"}, Get().PreferredProviders("default", "tools/search"))
}

// defaultSettingWith builds a setting that differs from the defaults only in
// the candidate order, so a validation failure can only come from that field.
func defaultSettingWith(candidateOrder string) WebSearchSetting {
	return WebSearchSetting{
		CandidateOrder:               candidateOrder,
		NativeFirst:                  true,
		SearchCacheTTLSeconds:        defaultSearchCacheTTLSeconds,
		FetchCacheTTLSeconds:         defaultFetchCacheTTLSeconds,
		RedirectCacheTTLSeconds:      defaultRedirectCacheTTLSeconds,
		ResponseHeaderTimeoutSeconds: defaultResponseHeaderTimeoutSeconds,
		RetryAttempts:                defaultRetryAttempts,
		RetryBaseDelayMilliseconds:   defaultRetryBaseDelayMilliseconds,
		ExaBaseURL:                   defaultExaBaseURL,
	}
}
