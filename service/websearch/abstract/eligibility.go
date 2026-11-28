package abstract

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

type EligibilityReason string

const (
	EligibilityReady              EligibilityReason = "eligible"
	EligibilityDisabled           EligibilityReason = "channel_disabled"
	EligibilityGroupDisabled      EligibilityReason = "group_disabled"
	EligibilityNoAbility          EligibilityReason = "ability_not_enabled"
	EligibilityUnsupported        EligibilityReason = "unsupported_contract"
	EligibilityNotPreferred       EligibilityReason = "provider_not_allowed"
	EligibilityMissingCredential  EligibilityReason = "missing_credential"
	EligibilityMissingMapping     EligibilityReason = "missing_model_mapping"
	EligibilityMissingPreparation EligibilityReason = "missing_request_preparation"
	EligibilityBuiltinDisabled    EligibilityReason = "builtin_fallback_disabled"
	EligibilityCustomUnverified   EligibilityReason = "custom_operation_unverified"
)

type Qualification struct {
	Builtin                     bool
	ChannelEnabled              bool
	AbilityEnabled              bool
	CredentialConfigured        bool
	ChannelCredentialSelectable bool
	Preparation                 *RequestPreparation
}

type RequestPreparation struct {
	Provider           string
	EndpointConfigured bool
	HasHeaders         bool
	HasBody            bool
	HasMappedModel     bool
}

type CandidateEligibility struct {
	Candidate
	Reason EligibilityReason
}

func candidateReason(channel ChannelContext, contract Contract, candidate Candidate) EligibilityReason {
	capabilities := CapabilitiesForProvider(candidate.Provider)
	if !capabilities.Supports(contract) {
		return EligibilityUnsupported
	}
	if (candidate.Origin == OriginNativeChannel || candidate.Origin == OriginChannelUpstream) && !capabilities.SupportsEndpoint(channel.BaseURL) {
		return EligibilityUnsupported
	}
	if channel.OnlyProviders && !slices.Contains(channel.PreferredProviders, candidate.Provider) {
		return EligibilityNotPreferred
	}
	if candidate.Origin == OriginOperatorBinding && candidate.Provider == websearch_setting.ProviderExa {
		if operation := channel.CustomOperation(contract); operation != nil && !operation.Verified(channel.ChannelID, contract, channel.BaseURL) {
			return EligibilityCustomUnverified
		}
	}
	if MissingOperationMapping(capabilities, channel.ModelMapping, contract) {
		return EligibilityMissingMapping
	}
	return EligibilityReady
}

func (input Qualification) reason(channel ChannelContext, settings websearch_setting.WebSearchSetting, contract Contract, candidate Candidate) EligibilityReason {
	if input.Builtin {
		if !settings.BuiltinFallbackEnabled || contract == ContractAlphaSearch || (candidate.Origin != OriginGlobalCredential && candidate.Origin != OriginLocal) {
			return EligibilityBuiltinDisabled
		}
	} else {
		if !input.ChannelEnabled {
			return EligibilityDisabled
		}
		if !input.AbilityEnabled {
			return EligibilityNoAbility
		}
		if !input.ChannelCredentialSelectable {
			return EligibilityMissingCredential
		}
	}
	if candidate.Origin == OriginLocal {
		return EligibilityReady
	}
	if candidate.Origin == OriginGlobalCredential {
		if strings.TrimSpace(settings.ExaAPIKey) == "" {
			return EligibilityMissingCredential
		}
		return EligibilityReady
	}
	if candidate.Origin == OriginOperatorBinding && candidate.Provider == websearch_setting.ProviderExa && channel.CustomOperation(contract) != nil {
		return EligibilityReady
	}
	prepared := input.Preparation
	ownsPreparation := prepared != nil && prepared.Provider == candidate.Provider && candidate.Origin != OriginOperatorBinding
	if !input.CredentialConfigured && (!ownsPreparation || !prepared.HasHeaders) {
		return EligibilityMissingCredential
	}
	if prepared == nil {
		return EligibilityReady
	}
	capabilities := CapabilitiesForProvider(candidate.Provider)
	if capabilities.RequiresPreparation(contract) {
		if !ownsPreparation || !prepared.EndpointConfigured || !prepared.HasHeaders || (capabilities.RequiresMapping(contract) && !prepared.HasMappedModel) {
			return EligibilityMissingPreparation
		}
		return EligibilityReady
	}
	if candidate.Provider != websearch_setting.ProviderCodex {
		return EligibilityReady
	}
	if !ownsPreparation || !prepared.EndpointConfigured {
		return EligibilityMissingPreparation
	}
	if candidate.Native && prepared.HasBody || !candidate.Native && prepared.HasHeaders && prepared.HasMappedModel {
		return EligibilityReady
	}
	return EligibilityMissingPreparation
}
