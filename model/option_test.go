package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateOptionMapRepublishesWebSearchCandidateOrder checks that the layered
// config branch of updateOptionMap reaches websearch_setting.UpdateAndSync, so
// the new order is readable without a restart.
func TestUpdateOptionMapRepublishesWebSearchCandidateOrder(t *testing.T) {
	originalMap := common.OptionMap
	savedConfigs := config.GlobalConfig.ExportAllConfigs()
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = originalMap
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfigs))
		// LoadFromDB writes the struct but does not publish it.
		require.NoError(t, websearch_setting.UpdateAndSync())
	})

	require.NoError(t, updateOptionMap("websearch_setting.candidate_order", "exa,local"))

	assert.Equal(t, "exa,local", common.OptionMap["websearch_setting.candidate_order"])
	assert.Equal(t, []string{"exa", "local"}, websearch_setting.ProviderOrder())
}

func TestWebSearchInvalidBulkOptionsKeepPublishedSettings(t *testing.T) {
	originalMap := common.OptionMap
	savedConfigs := config.GlobalConfig.ExportAllConfigs()
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = originalMap
		require.NoError(t, config.GlobalConfig.LoadFromDB(savedConfigs))
		require.NoError(t, websearch_setting.UpdateAndSync())
	})

	const ttlKey = "websearch_setting.search_cache_ttl_seconds"
	const retryKey = "websearch_setting.retry_attempts"
	require.NoError(t, applyWebSearchOptions(map[string]string{ttlKey: "-1", retryKey: "0"}))
	require.ErrorContains(t, UpdateOptionsBulk(map[string]string{ttlKey: "30", retryKey: "1.5"}), "retry_attempts")
	require.ErrorContains(t, updateOptionMap(ttlKey, "-2"), "search_cache_ttl_seconds")
	assert.Equal(t, "-1", common.OptionMap[ttlKey])
	assert.Equal(t, "0", common.OptionMap[retryKey])
	assert.Equal(t, -1, websearch_setting.Get().SearchCacheTTLSeconds)
	assert.Zero(t, websearch_setting.Get().RetryAttempts)
}

func TestWebSearchGroupPoliciesInvalidSyncKeepsPublishedRules(t *testing.T) {
	previousOptions := common.OptionMap
	previousPolicies := websearch_setting.Get().GroupPolicies
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.OptionMap = previousOptions
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{websearch_setting.GroupPoliciesOptionKey: previousPolicies}))
		require.NoError(t, websearch_setting.UpdateAndSync())
	})

	key := websearch_setting.GroupPoliciesOptionKey
	const valid = `{"default":{"tools/fetch":{"mode":"disabled"}}}`
	require.NoError(t, updateOptionMap(key, valid))
	require.Error(t, updateOptionMap(key, `{"default":{"tools/fetch":{"mode":"wrong"}}}`))
	assert.Equal(t, valid, common.OptionMap[key])
	assert.Equal(t, websearch_setting.GroupPolicyDisabled, websearch_setting.Get().GroupPolicyMode("default", "tools/fetch"))

	require.NoError(t, updateOptionMap(key, `{}`))
	assert.Equal(t, websearch_setting.GroupPolicyInherit, websearch_setting.Get().GroupPolicyMode("default", "tools/fetch"))
}
