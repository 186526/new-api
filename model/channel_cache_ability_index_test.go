package model

import (
	"slices"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

func TestIndexEnabledChannelAbilitiesUsesPublishedAbilityRows(t *testing.T) {
	channels := map[int]*Channel{
		1: {Id: 1, Status: common.ChannelStatusEnabled, Group: "default,undeclared", Models: "tools-search,tools-fetch"},
		2: {Id: 2, Status: common.ChannelStatusManuallyDisabled},
	}
	abilities := []*Ability{
		{Group: "default", Model: constant.ToolsSearchModel, ChannelId: 1, Enabled: true},
		{Group: "default", Model: constant.ToolsFetchModel, ChannelId: 1, Enabled: false},
		{Group: "default", Model: constant.ToolsSearchModel, ChannelId: 2, Enabled: true},
		{Group: "deleted", Model: constant.ToolsFetchModel, ChannelId: 3, Enabled: true},
	}
	indexed, models := indexEnabledChannelAbilities(channels, abilities)
	if !slices.Equal(indexed["default"][constant.ToolsSearchModel], []int{1}) || len(indexed["default"]) != 1 || len(indexed) != 1 {
		t.Fatalf("indexed = %+v, want only enabled search in default", indexed)
	}
	if _, exists := models[constant.ToolsSearchModel]; !exists || len(models) != 1 {
		t.Fatalf("enabled models = %+v, want only search", models)
	}
}
