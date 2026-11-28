package vertex

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

var _ channel.WebSearchAdaptor = (*Adaptor)(nil)

func (*Adaptor) SearchProviderMeta() abstract.ProviderCapabilities {
	return abstract.ChannelWebSearchCapabilities(constant.ChannelTypeVertexAi)
}

func (adaptor *Adaptor) PrepareWebSearch(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract) (channel.WebSearchUpstream, error) {
	model := channel.HostedWebSearchModel(ctx.GetString("model_mapping"), contract)
	if !strings.HasPrefix(model, "gemini-") || strings.Contains(model, "embedding") || strings.Contains(model, "-maas") {
		return channel.WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	return channel.PrepareHostedWebSearch(ctx, info, contract, adaptor, nil)
}
