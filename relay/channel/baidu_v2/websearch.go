package baidu_v2

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/gin-gonic/gin"
)

var _ channel.WebSearchAdaptor = (*Adaptor)(nil)

func (*Adaptor) SearchProviderMeta() abstract.ProviderCapabilities {
	return abstract.ChannelWebSearchCapabilities(constant.ChannelTypeBaiduV2)
}

func (adaptor *Adaptor) PrepareWebSearch(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract) (channel.WebSearchUpstream, error) {
	if info == nil || info.ChannelMeta == nil || !abstract.RESTSearchSupportsKey(constant.ChannelTypeBaiduV2, info.ApiKey) {
		return channel.WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	return channel.PrepareManagedWebSearch(ctx, info, contract, adaptor)
}
