package channel

import (
	"net/http"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

// WebSearchAdaptor describes a channel's own upstream. The zero endpoint in a
// successful preparation means the provider requester manages its own URL and
// credentials. Preparation never sends a request.
type WebSearchAdaptor interface {
	SearchProviderMeta() abstract.ProviderCapabilities
	PrepareWebSearch(*gin.Context, *relaycommon.RelayInfo, abstract.Contract) (WebSearchUpstream, error)
}

type WebSearchUpstream struct {
	Provider string
	Endpoint string
	Model    string
	Prepare  func(*http.Request) error
}

func PrepareWebSearchUpstream(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract, adaptor interface {
	Adaptor
	SearchProviderMeta() abstract.ProviderCapabilities
}) (WebSearchUpstream, error) {
	capabilities := adaptor.SearchProviderMeta()
	if !capabilities.Supports(contract) {
		return WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	upstream := WebSearchUpstream{Provider: capabilities.Provider}
	wireContract := capabilities.FetchContract
	if contract.IsSearch() {
		wireContract = capabilities.SearchContract
	}
	if wireContract == "" {
		return upstream, nil
	}
	if info == nil || info.ChannelMeta == nil {
		return WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	attempt := *info
	channelMeta := *info.ChannelMeta
	attempt.ChannelMeta = &channelMeta
	if capabilities.RequiresMapping(contract) {
		upstream.Model = abstract.CodexSearchModel(ctx.GetString("model_mapping"))
		if upstream.Model == "" {
			return WebSearchUpstream{}, abstract.ErrUnsupportedChannel
		}
		attempt.UpstreamModelName = upstream.Model
	}
	if contract != wireContract {
		switch wireContract {
		case abstract.ContractToolsSearch:
			attempt.RelayMode, attempt.RequestURLPath = relayconstant.RelayModeToolsSearch, "/v1/tools/search"
		case abstract.ContractToolsFetch:
			attempt.RelayMode, attempt.RequestURLPath = relayconstant.RelayModeToolsFetch, "/v1/tools/fetch"
		case abstract.ContractAlphaSearch:
			attempt.RelayMode, attempt.RequestURLPath = relayconstant.RelayModeAlphaSearch, "/v1/alpha/search"
		}
	}
	adaptor.Init(&attempt)
	endpoint, err := adaptor.GetRequestURL(&attempt)
	if err != nil {
		return WebSearchUpstream{}, err
	}
	upstream.Endpoint = endpoint
	upstream.Prepare = prepareWebSearchHeaders(ctx, &attempt, adaptor)
	return upstream, nil
}

// PrepareManagedWebSearch lets the requester construct a provider-specific URL
// and body while keeping header preparation on the selected channel adaptor.
func PrepareManagedWebSearch(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract, adaptor interface {
	Adaptor
	SearchProviderMeta() abstract.ProviderCapabilities
}) (WebSearchUpstream, error) {
	capabilities := adaptor.SearchProviderMeta()
	if !capabilities.Supports(contract) || info == nil || info.ChannelMeta == nil {
		return WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	attempt := *info
	channelMeta := *info.ChannelMeta
	attempt.ChannelMeta = &channelMeta
	return WebSearchUpstream{
		Provider: capabilities.Provider,
		Prepare:  prepareWebSearchHeaders(ctx, &attempt, adaptor),
	}, nil
}

func prepareWebSearchHeaders(ctx *gin.Context, info *relaycommon.RelayInfo, adaptor Adaptor) func(*http.Request) error {
	return func(req *http.Request) error {
		if err := adaptor.SetupRequestHeader(ctx, &req.Header, info); err != nil {
			return err
		}
		return ApplyHeaderOverride(info, ctx, req)
	}
}
