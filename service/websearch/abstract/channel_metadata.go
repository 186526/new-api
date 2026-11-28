package abstract

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/toolcatalog"
)

type ChannelOperationMetadata struct {
	Supported                bool       `json:"supported"`
	Contracts                []Contract `json:"contracts"`
	NativeContracts          []Contract `json:"native_contracts"`
	MappingRequiredContracts []Contract `json:"mapping_required_contracts"`
	FixedModel               string     `json:"fixed_model,omitempty"`
	RequiresVerification     bool       `json:"requires_verification,omitempty"`
}

type ChannelProviderMetadata struct {
	Provider string                   `json:"provider"`
	Search   ChannelOperationMetadata `json:"search"`
	Fetch    ChannelOperationMetadata `json:"fetch"`
}

type ChannelToolCatalog struct {
	ChannelTypes map[int]ChannelProviderMetadata `json:"channel_types"`
	Fallbacks    []ChannelProviderMetadata       `json:"fallbacks"`
}

func ChannelToolCapabilities() ChannelToolCatalog {
	channelTypes := toolcatalog.ChannelTypes()
	catalog := ChannelToolCatalog{
		ChannelTypes: make(map[int]ChannelProviderMetadata, len(channelTypes)),
		Fallbacks:    []ChannelProviderMetadata{},
	}
	for channelType, capabilities := range channelTypes {
		catalog.ChannelTypes[channelType] = providerMetadata(capabilities)
	}
	custom := providerMetadata(toolcatalog.Lookup(toolcatalog.Exa))
	custom.Search.RequiresVerification, custom.Fetch.RequiresVerification = true, true
	catalog.ChannelTypes[constant.ChannelTypeAdvancedCustom] = custom
	for _, id := range toolcatalog.IDs() {
		capabilities := toolcatalog.Lookup(id)
		if capabilities.GlobalCredential || capabilities.LocalExecution {
			catalog.Fallbacks = append(catalog.Fallbacks, providerMetadata(capabilities))
		}
	}
	return catalog
}

func providerMetadata(capabilities ProviderCapabilities) ChannelProviderMetadata {
	return ChannelProviderMetadata{
		Provider: capabilities.Provider,
		Search:   operationMetadata(capabilities, true),
		Fetch:    operationMetadata(capabilities, false),
	}
}

func operationMetadata(capabilities ProviderCapabilities, search bool) ChannelOperationMetadata {
	metadata := ChannelOperationMetadata{Contracts: []Contract{}, NativeContracts: []Contract{}, MappingRequiredContracts: []Contract{}}
	if search {
		metadata.FixedModel = capabilities.FixedSearchModel
	}
	for _, contract := range AllContracts() {
		if contract.IsSearch() != search || !capabilities.Supports(contract) {
			continue
		}
		metadata.Supported = true
		metadata.Contracts = append(metadata.Contracts, contract)
		if SpeaksNatively(capabilities.Provider, contract) {
			metadata.NativeContracts = append(metadata.NativeContracts, contract)
		}
		if capabilities.RequiresMapping(contract) {
			metadata.MappingRequiredContracts = append(metadata.MappingRequiredContracts, contract)
		}
	}
	return metadata
}

func MissingOperationMapping(capabilities ProviderCapabilities, modelMapping string, contract Contract) bool {
	return capabilities.RequiresMapping(contract) && ToolOperationModel(modelMapping, contract) == ""
}

// MissingToolSearchMapping reports a missing channel-level mapping, not a live readiness check.
func MissingToolSearchMapping(channelType int, modelMapping string) bool {
	return MissingOperationMapping(ChannelWebSearchCapabilities(channelType), modelMapping, ContractToolsSearch)
}
