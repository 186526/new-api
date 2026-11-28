package abstract

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

type routingKey struct{}
type cacheTenantKey struct{}

func WithCacheTenant(ctx context.Context, userID int) context.Context {
	return context.WithValue(ctx, cacheTenantKey{}, userID)
}

func CacheTenant(ctx context.Context) int {
	userID, _ := ctx.Value(cacheTenantKey{}).(int)
	return userID
}

type RoutingPolicy struct {
	Settings websearch_setting.WebSearchSetting
	Group    string
}

func WithRoutingPolicy(ctx context.Context, policy RoutingPolicy) context.Context {
	return context.WithValue(ctx, routingKey{}, policy)
}

func RequestRoutingPolicy(ctx context.Context) (RoutingPolicy, bool) {
	policy, ok := ctx.Value(routingKey{}).(RoutingPolicy)
	return policy, ok
}

func CandidatesForRequest(ctx context.Context, channel ChannelContext, contract Contract) ([]Candidate, error) {
	if policy, ok := RequestRoutingPolicy(ctx); ok {
		return policy.Candidates(channel, contract)
	}
	return Candidates(channel, contract)
}

func (policy RoutingPolicy) Candidates(channel ChannelContext, contract Contract) ([]Candidate, error) {
	mode := policy.Settings.GroupPolicyMode(policy.Group, string(contract))
	if mode == websearch_setting.GroupPolicyDisabled {
		return nil, ErrUnsupportedChannel
	}
	return CandidatesWithSettings(policy.channelPreferences(channel, contract, mode), contract, policy.Settings)
}

func (policy RoutingPolicy) Qualify(channel ChannelContext, contract Contract, input Qualification) ([]Candidate, []CandidateEligibility) {
	mode := policy.Settings.GroupPolicyMode(policy.Group, string(contract))
	if mode == websearch_setting.GroupPolicyDisabled {
		return nil, []CandidateEligibility{{Reason: EligibilityGroupDisabled}}
	}
	return qualifiedCandidates(policy.channelPreferences(channel, contract, mode), contract, policy.Settings, &input)
}

func QualifyWithSettings(channel ChannelContext, contract Contract, settings websearch_setting.WebSearchSetting, input Qualification) ([]Candidate, []CandidateEligibility) {
	return qualifiedCandidates(channel, contract, settings, &input)
}

func (policy RoutingPolicy) channelPreferences(channel ChannelContext, contract Contract, mode websearch_setting.GroupPolicyMode) ChannelContext {
	channel.PreferredProviders = policy.Settings.PreferredProviders(policy.Group, string(contract))
	channel.OnlyProviders = mode == websearch_setting.GroupPolicyOnly
	return channel
}

func CodexSearchModel(mapping string) string {
	return ToolOperationModel(mapping, ContractToolsSearch)
}

func ToolOperationModel(mapping string, contract Contract) string {
	var models map[string]string
	if json.Unmarshal([]byte(mapping), &models) != nil {
		return ""
	}
	current := constant.ToolsFetchModel
	if contract.IsSearch() {
		current = constant.ToolsSearchModel
	}
	seen := make(map[string]bool)
	for !seen[current] {
		seen[current] = true
		next := strings.TrimSpace(models[current])
		if next == "" || next == current {
			if current != constant.ToolsSearchModel && current != constant.ToolsFetchModel && !strings.ContainsAny(current, "/?#\\ \r\n\t") {
				return current
			}
			return ""
		}
		current = next
	}
	return ""
}
