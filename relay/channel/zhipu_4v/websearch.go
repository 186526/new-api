package zhipu_4v

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
	return abstract.ChannelWebSearchCapabilities(constant.ChannelTypeZhipu_v4)
}

func (adaptor *Adaptor) PrepareWebSearch(ctx *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract) (channel.WebSearchUpstream, error) {
	if info == nil || info.ChannelMeta == nil || !abstract.RESTSearchSupportsBase(constant.ChannelTypeZhipu_v4, info.ChannelBaseUrl) {
		return channel.WebSearchUpstream{}, abstract.ErrUnsupportedChannel
	}
	prepared, err := channel.PrepareManagedWebSearch(ctx, info, contract, adaptor)
	if err != nil || contract.IsSearch() {
		return prepared, err
	}
	base := strings.TrimRight(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if base == "" {
		base = constant.GetChannelBaseURL(info.ChannelType)
	}
	prepared.Endpoint = base + "/api/paas/v4/reader"
	return prepared, nil
}
