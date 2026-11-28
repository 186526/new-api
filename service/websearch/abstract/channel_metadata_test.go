package abstract

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/toolcatalog"
)

func TestChannelOperationMapping(t *testing.T) {
	const mapping = `{"tools-search":"alias","alias":"claude-search","tools-fetch":"claude-fetch"}`
	claude := ChannelWebSearchCapabilities(constant.ChannelTypeAnthropic)
	if got := ToolOperationModel(mapping, ContractToolsSearch); got != "claude-search" {
		t.Fatalf("resolved search model = %q", got)
	}
	if got := ToolOperationModel(mapping, ContractMoonshotFetch); got != "claude-fetch" {
		t.Fatalf("resolved fetch model = %q", got)
	}
	if !MissingOperationMapping(claude, `{"tools-search":"claude-search"}`, ContractToolsFetch) {
		t.Fatal("search mapping must not satisfy fetch")
	}
	codex := ChannelWebSearchCapabilities(constant.ChannelTypeCodex)
	if MissingOperationMapping(codex, "", ContractAlphaSearch) || !MissingOperationMapping(codex, "", ContractToolsSearch) {
		t.Fatal("Codex must require mapping only for converted search")
	}
}

func TestPreferredChannelOperation(t *testing.T) {
	if got := PreferredChannelOperation(constant.ChannelTypeAnthropic, toolcatalog.Antigravity, ContractToolsFetch); got.Provider != "claude" {
		t.Fatalf("unsupported preference should preserve channel fetch: %q", got.Provider)
	}
	if got := PreferredChannelOperation(constant.ChannelTypeAnthropic, toolcatalog.Antigravity, ContractToolsSearch); got.FixedSearchModel != toolcatalog.AntigravitySearchModel {
		t.Fatalf("unexpected preferred search capability: %+v", got)
	}
	if got := PreferredChannelOperation(constant.ChannelTypeAdvancedCustom, toolcatalog.Exa, ContractToolsSearch); got.Provider != "" {
		t.Fatalf("legacy custom preference cannot enable Exa: %+v", got)
	}
}
