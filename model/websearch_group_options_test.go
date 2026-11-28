package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/stretchr/testify/require"
)

func TestToolsAdminWebSearchGroupOptionsCAS(t *testing.T) {
	useFrontendOptionMigrationDB(t)
	key := websearch_setting.GroupPoliciesOptionKey
	common.OptionMapRWMutex.Lock()
	originalMap := common.OptionMap
	originalValue, hadOriginal := common.OptionMap[key]
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	common.OptionMapRWMutex.Unlock()
	originalSetting := websearch_setting.Get().GroupPolicies
	if originalSetting == "" {
		originalSetting = "{}"
	}
	t.Cleanup(func() {
		require.NoError(t, applyWebSearchOptions(map[string]string{key: originalSetting}))
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		common.OptionMap = originalMap
		if hadOriginal {
			common.OptionMap[key] = originalValue
		} else {
			delete(common.OptionMap, key)
		}
	})
	first, err := ReadWebSearchGroupOptions(t.Context())
	require.NoError(t, err)
	require.Equal(t, "absent", first.Version)
	saved, err := CompareAndSwapWebSearchGroupOptions(t.Context(), first.Version, `{"default":{"tools/search":{"mode":"disabled"}}}`)
	require.NoError(t, err)
	require.Equal(t, websearch_setting.GroupPolicyDisabled, websearch_setting.Get().GroupPolicyMode("default", websearch_setting.ContractToolsSearch))
	latest, err := CompareAndSwapWebSearchGroupOptions(t.Context(), first.Version, `{}`)
	require.ErrorIs(t, err, ErrWebSearchGroupPolicyConflict)
	require.Equal(t, saved, latest)
	_, err = CompareAndSwapWebSearchGroupOptions(t.Context(), saved.Version, `{"default":{"tools/search":{"mode":"only","providers":["local"]}}}`)
	require.Error(t, err)
	require.Equal(t, saved.Value, requireOptionValue(t, DB, key))
	reset, err := CompareAndSwapWebSearchGroupOptions(t.Context(), saved.Version, `{}`)
	require.NoError(t, err)
	require.NotEqual(t, saved.Version, reset.Version)
}
