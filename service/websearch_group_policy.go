package service

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/gin-gonic/gin"
)

const WebSearchGroupDisabledCode = "websearch_group_disabled"
const WebSearchGroupDisabledMessage = "Web search or fetch is disabled for this group."

var ErrWebSearchGroupDisabled = errors.New(WebSearchGroupDisabledMessage)

func webSearchGroupPolicyContract(path string) string {
	switch path {
	case "/v1/tools/search":
		return websearch_setting.ContractToolsSearch
	case "/v1/tools/fetch":
		return websearch_setting.ContractToolsFetch
	case "/v1/alpha/search":
		return websearch_setting.ContractAlphaSearch
	case relayconstant.MoonshotSearchPath:
		return websearch_setting.ContractMoonshotSearch
	case relayconstant.MoonshotFetchPath:
		return websearch_setting.ContractMoonshotFetch
	default:
		return ""
	}
}

type webSearchGroupPolicy struct {
	contract string
	settings websearch_setting.WebSearchSetting
}

func requestWebSearchGroupPolicy(c *gin.Context, path string) webSearchGroupPolicy {
	const contextKey = "websearch_group_policy_snapshot"
	if path == "" && c.Request != nil {
		path = c.Request.URL.Path
	}
	contract := webSearchGroupPolicyContract(path)
	if contract == "" {
		return webSearchGroupPolicy{}
	}
	if value, ok := c.Get(contextKey); ok {
		if policy, ok := value.(webSearchGroupPolicy); ok && policy.contract == contract {
			return policy
		}
	}
	policy := webSearchGroupPolicy{contract: contract, settings: websearch_setting.Get()}
	c.Set(contextKey, policy)
	return policy
}

func (policy webSearchGroupPolicy) mode(group string) websearch_setting.GroupPolicyMode {
	return policy.settings.GroupPolicyMode(group, policy.contract)
}

func (policy webSearchGroupPolicy) disabled(group string) bool {
	return policy.mode(group) == websearch_setting.GroupPolicyDisabled
}

// WebSearchPreferredProviders reads the same policy snapshot used for channel
// selection and applies it to the concrete group of the current attempt.
func WebSearchPreferredProviders(c *gin.Context) []string {
	policy := requestWebSearchGroupPolicy(c, "")
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" {
		group = common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
	}
	return policy.settings.PreferredProviders(group, policy.contract)
}

func ApplyWebSearchRoutingPolicy(c *gin.Context) {
	policy := requestWebSearchGroupPolicy(c, "")
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" {
		group = common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
	}
	if policy.contract != "" {
		c.Request = c.Request.WithContext(abstract.WithRoutingPolicy(c.Request.Context(), policy.routing(group)))
	}
}

func (policy webSearchGroupPolicy) routing(group string) abstract.RoutingPolicy {
	return abstract.RoutingPolicy{Settings: policy.settings, Group: group}
}

func (policy webSearchGroupPolicy) candidates(group string, channel *model.Channel) []abstract.Candidate {
	candidates, _ := policy.channelQualification(group, channel, true)
	return candidates
}

func (policy webSearchGroupPolicy) channelQualification(group string, channel *model.Channel, abilityEnabled bool) ([]abstract.Candidate, []abstract.CandidateEligibility) {
	return policy.routing(group).Qualify(readOnlyToolsChannel(channel), abstract.Contract(policy.contract), channelQualification(channel, abilityEnabled))
}

func channelQualification(channel *model.Channel, abilityEnabled bool) abstract.Qualification {
	configured := false
	if !channel.ChannelInfo.IsMultiKey {
		configured = strings.TrimSpace(channel.Key) != ""
	} else {
		for index, key := range channel.GetKeys() {
			status, exists := channel.ChannelInfo.MultiKeyStatusList[index]
			if (!exists || status == common.ChannelStatusEnabled) && strings.TrimSpace(key) != "" {
				configured = true
				break
			}
		}
	}
	return abstract.Qualification{
		ChannelEnabled: channel.Status == common.ChannelStatusEnabled, AbilityEnabled: abilityEnabled,
		CredentialConfigured: configured, ChannelCredentialSelectable: configured,
	}
}

func toolsRoutingModels(modelName string) []string {
	if normalized := ratio_setting.RoutingMatchModelName(modelName); normalized != "" && normalized != modelName {
		return []string{modelName, normalized}
	}
	return []string{modelName}
}

func toolsChannelPassesFilters(channel *model.Channel, modelName string, filters []dto.ChannelFilter) bool {
	for _, filter := range filters {
		if filter.Kind == dto.FilterRequestPath {
			if !model.ChannelSupportsRequestPathReadOnly(channel, filter.RequestPath, modelName) {
				return false
			}
			continue
		}
		if ok, _ := model.ChannelSatisfiesFilters(channel, modelName, []dto.ChannelFilter{filter}); !ok {
			return false
		}
	}
	return true
}

func (policy webSearchGroupPolicy) selectChannel(group, modelName string, retry int, filters []dto.ChannelFilter) (*model.Channel, error) {
	if policy.contract == "" {
		return model.GetRandomSatisfiedChannel(group, modelName, retry, filters)
	}
	memory := common.MemoryCacheEnabled
	routes, err := model.EnabledChannelRouteAbilities(group, toolsRoutingModels(modelName), memory)
	if err != nil {
		return nil, err
	}
	choices := make(map[int][]abstract.Candidate)
	var eligible []toolsChannelRoute
	for _, route := range routes {
		channel := route.Channel
		providers, found := choices[channel.Id]
		if !found {
			if toolsChannelPassesFilters(channel, modelName, filters) {
				providers = policy.candidates(group, channel)
			}
			choices[channel.Id] = providers
		}
		if len(providers) != 0 {
			eligible = append(eligible, toolsChannelRoute{route, providers})
		}
	}
	return policy.channelAttempt(group, modelName, eligible, retry, memory).pick(memory), nil
}

type toolsChannelRoute struct {
	model.ChannelRouteAbility
	providers []abstract.Candidate
}

type toolsWeightedRoute struct {
	toolsChannelRoute
	weight int
}

type toolsChannelAttempt []toolsWeightedRoute

func (attempt toolsChannelAttempt) pick(memory bool) *model.Channel {
	if len(attempt) == 0 {
		return nil
	}
	if len(attempt) == 1 {
		return attempt[0].Channel
	}
	var total int
	for _, route := range attempt {
		total += route.weight
	}
	draw := common.GetRandomInt(total)
	for _, route := range attempt {
		draw -= route.weight
		if draw < 0 || (!memory && draw == 0) {
			return route.Channel
		}
	}
	return nil
}

func (policy webSearchGroupPolicy) channelAttempt(group, modelName string, eligible []toolsChannelRoute, retry int, memory bool) toolsChannelAttempt {
	if len(eligible) == 0 {
		return nil
	}
	preferred := policy.settings.PreferredProviders(group, policy.contract)
	bestRank := len(preferred)
	bestModel := ""
	var bucket []toolsChannelRoute
	for _, route := range eligible {
		rank := toolsProviderRank(preferred, route.providers)
		if rank < bestRank || (rank == bestRank && bestModel != modelName && route.Model == modelName) {
			bucket = bucket[:0]
			bestRank, bestModel = rank, route.Model
		}
		if rank == bestRank && (bestModel == "" || route.Model == bestModel) {
			bestModel = route.Model
			bucket = append(bucket, route)
		}
	}
	return weightToolsPriority(bucket, retry, memory)
}

func toolsProviderRank(preferred []string, providers []abstract.Candidate) int {
	for index, provider := range preferred {
		if provider != websearch_setting.ProviderLocal && slices.ContainsFunc(providers, func(candidate abstract.Candidate) bool {
			return candidate.Provider == provider && candidate.Origin != abstract.OriginGlobalCredential
		}) {
			return index
		}
	}
	return len(preferred)
}

func weightToolsPriority(bucket []toolsChannelRoute, retry int, memory bool) toolsChannelAttempt {
	priorities := make([]int64, 0, len(bucket))
	for _, route := range bucket {
		priorities = append(priorities, route.Priority)
	}
	sort.Slice(priorities, func(left, right int) bool { return priorities[left] > priorities[right] })
	priorities = slices.Compact(priorities)
	priority := priorities[min(max(retry, 0), len(priorities)-1)]
	var attempt toolsChannelAttempt
	var sum int
	for _, route := range bucket {
		if route.Priority == priority {
			attempt = append(attempt, toolsWeightedRoute{route, int(route.Weight)})
			sum += int(route.Weight)
		}
	}
	for index := range attempt {
		switch {
		case !memory:
			attempt[index].weight += 10
		case sum == 0:
			attempt[index].weight = 100
		case sum/len(attempt) < 10:
			attempt[index].weight *= 100
		}
	}
	return attempt
}

func (policy webSearchGroupPolicy) channelEligible(group, modelName string, channel *model.Channel) bool {
	return !policy.disabled(group) && model.IsChannelEnabledForGroupModel(group, modelName, channel.Id) &&
		(policy.contract == "" || len(policy.candidates(group, channel)) != 0)
}

func (policy webSearchGroupPolicy) restrictsAffinity(c *gin.Context, group string) bool {
	if group == "auto" {
		for _, actual := range GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup)) {
			if mode := policy.mode(actual); mode == websearch_setting.GroupPolicyOnly || mode == websearch_setting.GroupPolicyPrefer {
				return true
			}
		}
		return false
	}
	mode := policy.mode(group)
	return mode == websearch_setting.GroupPolicyOnly || mode == websearch_setting.GroupPolicyPrefer
}

func (policy webSearchGroupPolicy) resolvePinnedAutoGroup(c *gin.Context, modelName string, channelID int) (string, *ChannelSelectError) {
	if policy.contract == "" {
		return "", nil
	}
	groups := GetRequestAutoGroups(c, common.GetContextKeyString(c, constant.ContextKeyUserGroup))
	channel, err := model.CacheGetChannel(channelID)
	if err != nil {
		return "", nil // The pin's caller reports invalid channel IDs.
	}
	blocked, allowed := false, false
	for _, group := range groups {
		if !model.IsChannelEnabledForGroupModel(group, modelName, channelID) {
			continue
		}
		if policy.disabled(group) {
			blocked = true
			continue
		}
		allowed = true
		if len(policy.candidates(group, channel)) == 0 {
			continue
		}
		return group, nil
	}
	if allowed {
		return "", &ChannelSelectError{
			StatusCode: http.StatusServiceUnavailable, Code: types.ErrorCodeModelNotFound,
			MessageID: i18n.MsgDistributorNoAvailableChannel, Params: map[string]any{"Group": "auto", "Model": modelName},
			NoAvailableChannel: true,
		}
	}
	if blocked {
		return "", webSearchGroupDenied()
	}
	return "", &ChannelSelectError{StatusCode: http.StatusForbidden, Code: types.ErrorCodeModelNotFound, MessageID: i18n.MsgDistributorGroupAccessDenied}
}

func webSearchGroupDenied() *ChannelSelectError {
	return &ChannelSelectError{
		StatusCode: http.StatusForbidden,
		Code:       WebSearchGroupDisabledCode,
		MessageID:  WebSearchGroupDisabledMessage,
	}
}
