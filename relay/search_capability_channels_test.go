package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// TestEverySearchChannelPublishesTheCapabilityNames requires each channel type
// that can serve a search or fetch contract to publish that capability name in
// its adaptor model list.
//
// The admin model picker fills itself from /api/channel/models, which
// controller.DashboardListModels builds from exactly these lists. A capability
// the list omits cannot be ticked in the dashboard, so the channel can never be
// given the ability and every request for it answers "no channel for this
// model". Ticking it is the only way to enable a capability, so the list is part
// of the contract rather than a convenience.
func TestEverySearchChannelPublishesTheCapabilityNames(t *testing.T) {
	// The channel types abstract.channelProvider maps to a provider that can
	// answer a search or a fetch. Exa is absent on purpose: it is reached through
	// a global key, never through a channel of its own type.
	for _, channelType := range []int{
		constant.ChannelTypeAntigravity,
		constant.ChannelTypeDeepSeek,
		constant.ChannelTypeNewAPI,
		constant.ChannelTypeSub2API,
	} {
		apiType, ok := common.ChannelType2APIType(channelType)
		if !ok {
			t.Fatalf("channel type %d has no api type", channelType)
		}
		adaptor := GetAdaptor(apiType)
		if adaptor == nil {
			t.Fatalf("channel type %d has no adaptor", channelType)
		}
		published := make(map[string]struct{})
		for _, name := range adaptor.GetModelList() {
			published[name] = struct{}{}
		}

		// A search needs an upstream that can search; a fetch always has the
		// in-process extractor as its last candidate, which needs no channel
		// credential, so any of these channels can carry either route.
		for _, capability := range []string{constant.ToolsSearchModel, constant.ToolsFetchModel} {
			if _, found := published[capability]; !found {
				t.Errorf("channel type %d (%s) does not publish %q; the dashboard cannot enable a capability its model list omits",
					channelType, adaptor.GetChannelName(), capability)
			}
		}
	}
}

// TestSearchCapabilityNamesAreNotProviderNames keeps the routing names out of
// the provider namespace, so a channel setting that names a provider can never
// be read as a routing name or the reverse.
func TestSearchCapabilityNamesAreNotProviderNames(t *testing.T) {
	providers := make(map[string]struct{})
	for _, provider := range websearch_setting.KnownProviders() {
		providers[provider] = struct{}{}
	}
	for _, capability := range []string{constant.ToolsSearchModel, constant.ToolsFetchModel} {
		if _, collides := providers[capability]; collides {
			t.Errorf("routing name %q collides with a provider name", capability)
		}
	}
}
