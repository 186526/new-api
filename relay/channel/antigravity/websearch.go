package antigravity

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

var _ channel.WebSearchAdaptor = (*Adaptor)(nil)

func (*Adaptor) SearchProviderMeta() abstract.ProviderCapabilities {
	return abstract.ChannelWebSearchCapabilities(constant.ChannelTypeAntigravity)
}

func (adaptor *Adaptor) PrepareWebSearch(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract) (channel.WebSearchUpstream, error) {
	return channel.PrepareWebSearchUpstream(ctx, info, contract, adaptor)
}
