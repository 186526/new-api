package abstract

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/stretchr/testify/require"
)

func TestCustomToolProofBindsOperationChannelEndpointAndCredential(t *testing.T) {
	const base = "https://tools.example.com/prefix"
	operation := &CustomToolOperation{Protocol: CustomToolsProtocol, Path: "/search"}
	var err error
	operation.Credential, err = SealCustomToolCredential(13, ContractToolsSearch, "synthetic-search")
	require.NoError(t, err)
	operation.Proof = operation.ProofFor(13, ContractToolsSearch, base)
	require.True(t, operation.Verified(13, ContractMoonshotSearch, base))
	require.False(t, operation.Verified(13, ContractToolsFetch, base))
	require.False(t, operation.Verified(14, ContractToolsSearch, base))
	require.False(t, operation.Verified(13, ContractToolsSearch, "https://different.example.com"))
	_, err = operation.OpenCredential(13, ContractToolsFetch)
	require.Error(t, err)
	updated := *operation
	updated.Path = "/different"
	require.False(t, updated.Verified(13, ContractToolsSearch, base))
	updated = *operation
	updated.Credential, err = SealCustomToolCredential(13, ContractToolsSearch, "synthetic-search")
	require.NoError(t, err)
	require.False(t, updated.Verified(13, ContractToolsSearch, base))
	endpoint, err := operation.Endpoint(base)
	require.NoError(t, err)
	require.Equal(t, "https://tools.example.com/prefix/search", endpoint)
}

func TestCustomToolQualificationSeparatesOperationsAndGlobalFallback(t *testing.T) {
	channel := ChannelContext{ChannelID: 13, ChannelType: constant.ChannelTypeAdvancedCustom, BaseURL: "https://tools.example.com"}
	operation := &CustomToolOperation{Protocol: CustomToolsProtocol, Path: "/search"}
	var err error
	operation.Credential, err = SealCustomToolCredential(13, ContractToolsSearch, "synthetic-search")
	require.NoError(t, err)
	setConfiguration := func() {
		encoded, err := json.Marshal(CustomTools{Search: operation})
		require.NoError(t, err)
		raw := string(encoded)
		channel.CustomTools = &raw
	}
	settings := websearch_setting.WebSearchSetting{ExaAPIKey: "synthetic-global"}
	input := Qualification{ChannelEnabled: true, AbilityEnabled: true, ChannelCredentialSelectable: true}
	setConfiguration()
	candidates, reasons := QualifyWithSettings(channel, ContractToolsSearch, settings, input)
	require.Len(t, candidates, 1)
	require.Equal(t, OriginGlobalCredential, candidates[0].Origin)
	require.Equal(t, EligibilityCustomUnverified, reasons[0].Reason)
	operation.Proof = operation.ProofFor(13, ContractToolsSearch, channel.BaseURL)
	setConfiguration()
	candidates, _ = QualifyWithSettings(channel, ContractToolsSearch, settings, input)
	require.Len(t, candidates, 1)
	require.Equal(t, OriginOperatorBinding, candidates[0].Origin)
	candidates, _ = QualifyWithSettings(channel, ContractToolsFetch, settings, input)
	for _, candidate := range candidates {
		require.NotEqual(t, OriginOperatorBinding, candidate.Origin)
	}
}
