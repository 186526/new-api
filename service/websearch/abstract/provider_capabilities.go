package abstract

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/toolcatalog"
)

type ProviderCapabilities = toolcatalog.Provider

func CapabilitiesForProvider(provider string) ProviderCapabilities {
	return toolcatalog.Lookup(provider)
}

func ChannelWebSearchCapabilities(channelType int) ProviderCapabilities {
	return toolcatalog.ForChannel(channelType)
}

func channelOperatorBinding(channelType int, provider string) ProviderCapabilities {
	bound := CapabilitiesForProvider(strings.ToLower(strings.TrimSpace(provider)))
	if !bound.ChannelBinding || (channelType == constant.ChannelTypeAdvancedCustom && bound.Provider == toolcatalog.Exa) {
		return ProviderCapabilities{}
	}
	return bound
}

func PreferredChannelOperation(channelType int, provider string, contract Contract) ProviderCapabilities {
	if bound := channelOperatorBinding(channelType, provider); bound.Supports(contract) {
		return bound
	}
	if own := ChannelWebSearchCapabilities(channelType); own.Supports(contract) {
		return own
	}
	return ProviderCapabilities{}
}

func ChannelProvider(channelType int) string {
	return ChannelWebSearchCapabilities(channelType).Provider
}

func SpeaksNatively(provider string, contract Contract) bool {
	return CapabilitiesForProvider(provider).Native(contract)
}
