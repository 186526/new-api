package websearch_setting

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupPoliciesOnlyRetainsProviderOrder(t *testing.T) {
	saveAndRestore(t)
	setting := defaultSettingWith("")
	setting.GroupPolicies = `{"default":{"tools/fetch":{"mode":"only","providers":["exa","local"]},"alpha/search":{"mode":"prefer","providers":["codex","deepseek"]}}}`
	require.NoError(t, apply(t, setting))
	current := Get()
	require.Equal(t, GroupPolicyOnly, current.GroupPolicyMode("default", ContractToolsFetch))
	require.Equal(t, []string{ProviderExa, ProviderLocal}, current.PreferredProviders("default", ContractToolsFetch))
	require.Equal(t, GroupPolicyPrefer, current.GroupPolicyMode("default", ContractAlphaSearch))
	require.Equal(t, []string{ProviderCodex, ProviderDeepSeek}, current.PreferredProviders("default", ContractAlphaSearch))
}

func TestGroupPoliciesRejectInvalidProviderLists(t *testing.T) {
	for _, mode := range []GroupPolicyMode{GroupPolicyPrefer, GroupPolicyOnly} {
		for _, suffix := range []string{"", `,"providers":[]`, `,"providers":null`, `,"providers":["exa","exa"]`, `,"providers":["unknown"]`} {
			raw := fmt.Sprintf(`{"default":{"tools/search":{"mode":%q%s}}}`, mode, suffix)
			require.Error(t, ValidateGroupPolicies(raw), raw)
		}
		for _, contract := range []string{ContractToolsSearch, ContractAlphaSearch, ContractMoonshotSearch} {
			raw := fmt.Sprintf(`{"default":{%q:{"mode":%q,"providers":["local"]}}}`, contract, mode)
			require.ErrorContains(t, ValidateGroupPolicies(raw), `provider "local" does not support`)
		}
		for _, provider := range []string{ProviderAntigravity, ProviderCodex} {
			raw := fmt.Sprintf(`{"default":{"moonshot/fetch":{"mode":%q,"providers":[%q]}}}`, mode, provider)
			require.ErrorContains(t, ValidateGroupPolicies(raw), "does not support")
		}
	}
}
