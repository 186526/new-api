package abstract

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

func TestWebSearchProviderCapabilitiesMatchPolicyValidation(t *testing.T) {
	for _, provider := range append(websearch_setting.KnownProviders(), "unknown") {
		for _, contract := range AllContracts() {
			if want := websearch_setting.ProviderSupportsContract(provider, string(contract)); CapabilitiesForProvider(provider).Supports(contract) != want {
				t.Errorf("%s/%s: policy validation and candidate discovery disagree", provider, contract)
			}
		}
	}
}

func TestWebSearchHostedFetchRequiresMappingAndPreparation(t *testing.T) {
	channel := ChannelContext{ChannelType: constant.ChannelTypeAnthropic}
	policy := RoutingPolicy{}
	input := Qualification{ChannelEnabled: true, AbilityEnabled: true, ChannelCredentialSelectable: true, CredentialConfigured: true}
	_, decisions := policy.Qualify(channel, ContractToolsFetch, input)
	if len(decisions) == 0 || decisions[0].Reason != EligibilityMissingMapping {
		t.Fatalf("qualifications = %+v, want missing fetch mapping", decisions)
	}
	channel.ModelMapping = `{"tools-fetch":"test-claude"}`
	input.Preparation = &RequestPreparation{Provider: "claude"}
	_, decisions = policy.Qualify(channel, ContractToolsFetch, input)
	if len(decisions) == 0 || decisions[0].Reason != EligibilityMissingPreparation {
		t.Fatalf("qualifications = %+v, want missing preparation", decisions)
	}
	input.Preparation.EndpointConfigured = true
	input.Preparation.HasHeaders = true
	input.Preparation.HasMappedModel = true
	candidates, _ := policy.Qualify(channel, ContractToolsFetch, input)
	if len(candidates) == 0 || candidates[0].Provider != "claude" {
		t.Fatalf("candidates = %+v, want Claude first", candidates)
	}
}

func TestWebSearchCodexMappingIsRequiredOnlyAcrossContracts(t *testing.T) {
	context := ChannelContext{ChannelType: constant.ChannelTypeCodex, ModelMapping: "{}"}
	settings := websearch_setting.WebSearchSetting{NativeFirst: true}
	if _, err := CandidatesWithSettings(context, ContractMoonshotSearch, settings); err != ErrUnsupportedChannel {
		t.Fatalf("unmapped cross-contract search: %v, want ErrUnsupportedChannel", err)
	}
	if candidates, err := CandidatesWithSettings(context, ContractAlphaSearch, settings); err != nil || len(candidates) != 1 || !candidates[0].Native {
		t.Fatalf("native candidates = %+v, err = %v", candidates, err)
	}
	context.ModelMapping = `{"tools-search":"test-model"}`
	if candidates, err := CandidatesWithSettings(context, ContractMoonshotSearch, settings); err != nil || len(candidates) != 1 || candidates[0].Native {
		t.Fatalf("mapped candidates = %+v, err = %v", candidates, err)
	}
}
