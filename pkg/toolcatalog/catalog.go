// Package toolcatalog defines the capabilities that tool routing and configuration share.
package toolcatalog

import (
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

const (
	Antigravity = "antigravity"
	BaiduV2     = "baidu_v2"
	Codex       = "codex"
	DeepSeek    = "deepseek"
	Exa         = "exa"
	Local       = "local"
	Moonshot    = "moonshot"
	Perplexity  = "perplexity"
	Zhipu       = "zhipu"

	AntigravitySearchModel = "gemini-3.5-flash-lite"
)

type Contract string

const (
	ToolsSearch    = "tools/search"
	ToolsFetch     = "tools/fetch"
	AlphaSearch    = "alpha/search"
	MoonshotSearch = "moonshot/search"
	MoonshotFetch  = "moonshot/fetch"
)

func Contracts() []Contract {
	return []Contract{ToolsSearch, ToolsFetch, AlphaSearch, MoonshotSearch, MoonshotFetch}
}

func (contract Contract) IsSearch() bool {
	switch contract {
	case ToolsSearch, AlphaSearch, MoonshotSearch:
		return true
	default:
		return false
	}
}

func (contract Contract) IsFetch() bool {
	return contract == ToolsFetch || contract == MoonshotFetch
}

type Provider struct {
	Provider          string
	SearchContract    Contract
	FetchContract     Contract
	ManagesSearch     bool
	ManagesFetch      bool
	MappedSearchModel bool
	MappedFetchModel  bool
	PreparedSearch    bool
	PreparedFetch     bool
	FixedSearchModel  string
	ChannelBinding    bool
	GlobalCredential  bool
	LocalExecution    bool
	endpointRule      endpointRule
}

type endpointRule uint8

const (
	defaultEndpoint endpointRule = iota
	restEndpoint
	zhipuChinaEndpoint
)

func (provider Provider) SupportsEndpoint(base string) bool {
	if provider.Provider == "" {
		return false
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" || provider.endpointRule == defaultEndpoint {
		return true
	}
	endpoint, err := url.Parse(base)
	if err != nil || endpoint.Hostname() == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return false
	}
	if provider.endpointRule == zhipuChinaEndpoint {
		host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
		return host != "z.ai" && !strings.HasSuffix(host, ".z.ai") && !strings.Contains(strings.ToLower(endpoint.Path), "/coding")
	}
	return true
}

func (provider Provider) Supports(contract Contract) bool {
	switch {
	case contract.IsSearch():
		return provider.SearchContract != "" || provider.ManagesSearch
	case contract.IsFetch():
		return provider.FetchContract != "" || provider.ManagesFetch
	default:
		return false
	}
}

func (provider Provider) RequiresMapping(contract Contract) bool {
	return contract.IsSearch() && provider.MappedSearchModel && contract != provider.SearchContract ||
		contract.IsFetch() && provider.MappedFetchModel && contract != provider.FetchContract
}

func (provider Provider) RequiresPreparation(contract Contract) bool {
	return contract.IsSearch() && provider.PreparedSearch || contract.IsFetch() && provider.PreparedFetch
}

func (provider Provider) Native(contract Contract) bool {
	return contract != "" && (provider.SearchContract == contract || provider.FetchContract == contract)
}

type registration struct {
	capabilities Provider
	channelTypes []int
}

// A provider is listed here only for operations implemented by an Adapter and
// covered by a response replay. Channel bindings are a separate opt-in.
var providers = []registration{
	{Provider{Provider: Antigravity, ManagesSearch: true, FixedSearchModel: AntigravitySearchModel, ChannelBinding: true}, []int{constant.ChannelTypeAntigravity}},
	{Provider{Provider: BaiduV2, ManagesSearch: true, endpointRule: restEndpoint}, []int{constant.ChannelTypeBaiduV2}},
	{Provider{Provider: Codex, SearchContract: AlphaSearch, MappedSearchModel: true}, []int{constant.ChannelTypeCodex}},
	{Provider{Provider: DeepSeek, SearchContract: ToolsSearch, FetchContract: ToolsFetch, ChannelBinding: true}, []int{constant.ChannelTypeDeepSeek, constant.ChannelTypeNewAPI, constant.ChannelTypeSub2API}},
	{Provider{Provider: Exa, ManagesSearch: true, ManagesFetch: true, ChannelBinding: true, GlobalCredential: true}, nil},
	{Provider{Provider: Local, ManagesFetch: true, LocalExecution: true}, nil},
	{Provider{Provider: Moonshot, ManagesSearch: true, ManagesFetch: true}, []int{constant.ChannelTypeMoonshot}},
	{Provider{Provider: Perplexity, ManagesSearch: true}, []int{constant.ChannelTypePerplexity}},
	{Provider{Provider: Zhipu, ManagesSearch: true, ManagesFetch: true, PreparedFetch: true, endpointRule: zhipuChinaEndpoint}, []int{constant.ChannelTypeZhipu_v4}},
	{Provider{Provider: "claude", ManagesSearch: true, ManagesFetch: true, MappedSearchModel: true, MappedFetchModel: true, PreparedSearch: true, PreparedFetch: true, endpointRule: restEndpoint}, []int{constant.ChannelTypeAnthropic}},
	{hostedSearch("gemini"), []int{constant.ChannelTypeGemini}},
	{hostedSearch("vertex"), []int{constant.ChannelTypeVertexAi}},
	{hostedSearch("openai"), []int{constant.ChannelTypeOpenAI}},
	{hostedSearch("azure"), []int{constant.ChannelTypeAzure}},
	{hostedSearch("xai"), []int{constant.ChannelTypeXai}},
	{hostedSearch("ali"), []int{constant.ChannelTypeAli}},
}

func hostedSearch(id string) Provider {
	return Provider{Provider: id, ManagesSearch: true, MappedSearchModel: true, PreparedSearch: true, endpointRule: restEndpoint}
}

func IDs() []string {
	ids := make([]string, 0, len(providers))
	for _, entry := range providers {
		ids = append(ids, entry.capabilities.Provider)
	}
	return ids
}

func Lookup(id string) Provider {
	for _, entry := range providers {
		if entry.capabilities.Provider == id {
			return entry.capabilities
		}
	}
	return Provider{}
}

var byChannel = func() map[int]Provider {
	channels := make(map[int]Provider)
	for _, entry := range providers {
		for _, channelType := range entry.channelTypes {
			channels[channelType] = entry.capabilities
		}
	}
	return channels
}()

func ForChannel(channelType int) Provider {
	return byChannel[channelType]
}

func ChannelTypes() map[int]Provider {
	channelTypes := make(map[int]Provider, len(byChannel))
	for channelType, capabilities := range byChannel {
		channelTypes[channelType] = capabilities
	}
	return channelTypes
}
