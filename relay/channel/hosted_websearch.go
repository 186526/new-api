package channel

import (
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

// PrepareHostedWebSearch uses the channel's non-streaming inference endpoint,
// with a model chosen by the operator from the abstract operation's mapping.
func PrepareHostedWebSearch(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract, adaptor interface {
	Adaptor
	SearchProviderMeta() abstract.ProviderCapabilities
}, configure func(*relaycommon.RelayInfo)) (WebSearchUpstream, error) {
	caps := adaptor.SearchProviderMeta()
	if !caps.Supports(contract) || info == nil || info.ChannelMeta == nil {
		return WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	model := HostedWebSearchModel(ctx.GetString("model_mapping"), contract)
	if model == "" {
		return WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	attempt := *info
	meta := *info.ChannelMeta
	attempt.ChannelMeta = &meta
	attempt.ChannelBaseUrl = strings.TrimRight(attempt.ChannelBaseUrl, "/")
	if attempt.ChannelBaseUrl == "" {
		attempt.ChannelBaseUrl = constant.GetChannelBaseURL(attempt.ChannelType)
	}
	attempt.OriginModelName, attempt.UpstreamModelName = model, model
	attempt.IsStream = false
	if configure != nil {
		configure(&attempt)
	}
	adaptor.Init(&attempt)
	endpoint, err := adaptor.GetRequestURL(&attempt)
	if err != nil {
		return WebSearchUpstream{}, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
		return WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	return WebSearchUpstream{Provider: caps.Provider, Endpoint: endpoint, Model: model, Prepare: prepareWebSearchHeaders(ctx, &attempt, adaptor)}, nil
}

func ConfigureHostedResponses(attempt *relaycommon.RelayInfo) {
	attempt.RelayMode = relayconstant.RelayModeResponses
	attempt.RelayFormat = types.RelayFormatOpenAIResponses
	attempt.RequestURLPath = "/v1/responses"
}

func HostedWebSearchModel(mapping string, contract abstract.Contract) string {
	return abstract.ToolOperationModel(mapping, contract)
}
