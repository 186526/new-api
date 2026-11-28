package openai

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

var _ channel.WebSearchAdaptor = (*Adaptor)(nil)

func (*Adaptor) SearchProviderMeta() abstract.ProviderCapabilities {
	return abstract.ChannelWebSearchCapabilities(constant.ChannelTypeOpenAI)
}

type azureSearchAdaptor struct{ *Adaptor }

func (azureSearchAdaptor) SearchProviderMeta() abstract.ProviderCapabilities {
	return abstract.ChannelWebSearchCapabilities(constant.ChannelTypeAzure)
}

func (adaptor *Adaptor) PrepareWebSearch(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract) (channel.WebSearchUpstream, error) {
	if info == nil || info.ChannelMeta == nil {
		return channel.WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	switch info.ChannelType {
	case constant.ChannelTypeOpenAI:
		return channel.PrepareHostedWebSearch(ctx, info, contract, adaptor, channel.ConfigureHostedResponses)
	case constant.ChannelTypeAzure:
		return channel.PrepareHostedWebSearch(ctx, info, contract, azureSearchAdaptor{adaptor}, channel.ConfigureHostedResponses)
	default:
		return channel.WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
}
