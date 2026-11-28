package service

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidWebSearchSimulation = errors.New("invalid contract, group, channel or model")

var toolsContracts = []abstract.Contract{abstract.ContractToolsSearch, abstract.ContractToolsFetch, abstract.ContractAlphaSearch, abstract.ContractMoonshotSearch, abstract.ContractMoonshotFetch}

type WebSearchProviderSummary struct {
	Provider                   string                             `json:"provider"`
	Contracts                  []abstract.Contract                `json:"contracts"`
	NativeContracts            []abstract.Contract                `json:"native_contracts"`
	AvailableChannels          int                                `json:"available_channels"`
	MissingMappingChannels     int                                `json:"missing_mapping_channels"`
	GlobalCredentialConfigured bool                               `json:"global_credential_configured"`
	LocalFallback              bool                               `json:"local_fallback"`
	ContractDetails            []WebSearchProviderContractSummary `json:"contract_details"`
}

type WebSearchProviderContractSummary struct {
	Contract                 abstract.Contract `json:"contract"`
	Native                   bool              `json:"native"`
	AvailableChannels        int               `json:"available_channels"`
	MissingMappingChannels   int               `json:"missing_mapping_channels"`
	AvailableByGroup         map[string]int    `json:"available_by_group"`
	BuiltinFallbackAvailable bool              `json:"builtin_fallback_available"`
}

func WebSearchProviderSummaries(ctx context.Context) ([]WebSearchProviderSummary, error) {
	var channels []model.Channel
	err := model.DB.WithContext(ctx).Select("id", "type", "status", "key", "channel_info", "setting", "model_mapping", "base_url", "custom_tools").Where("status = ?", common.ChannelStatusEnabled).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	var abilities []model.Ability
	if err := model.DB.WithContext(ctx).Where("enabled = ?", true).Find(&abilities).Error; err != nil {
		return nil, err
	}
	groups := make(map[int]map[abstract.Contract]map[string]bool)
	for _, ability := range abilities {
		if groups[ability.ChannelId] == nil {
			groups[ability.ChannelId] = make(map[abstract.Contract]map[string]bool)
		}
		for _, contract := range toolsContracts {
			if !abilityMatchesContract(ability.Model, contract) {
				continue
			}
			if groups[ability.ChannelId][contract] == nil {
				groups[ability.ChannelId][contract] = make(map[string]bool)
			}
			groups[ability.ChannelId][contract][ability.Group] = true
		}
	}
	summaries := make([]WebSearchProviderSummary, 0)
	settings := websearch_setting.Get()
	for _, provider := range websearch_setting.KnownProviders() {
		summaries = append(summaries, summarizeToolProvider(provider, toolSupply{channels, groups, settings}))
	}
	return summaries, nil
}

func readOnlyToolsChannel(channel *model.Channel) abstract.ChannelContext {
	var settings dto.ChannelSettings
	if channel.Setting != nil && common.Unmarshal([]byte(*channel.Setting), &settings) != nil {
		settings = dto.ChannelSettings{}
	}
	return abstract.ChannelContext{ChannelID: channel.Id, ChannelType: channel.Type, Settings: settings, ModelMapping: channel.GetModelMapping(), BaseURL: channel.GetBaseURL(), CustomTools: channel.CustomTools}
}

type toolSupply struct {
	channels []model.Channel
	groups   map[int]map[abstract.Contract]map[string]bool
	settings websearch_setting.WebSearchSetting
}

func abilityMatchesContract(modelName string, contract abstract.Contract) bool {
	if contract == abstract.ContractAlphaSearch {
		return modelName != "" && modelName != constant.ToolsSearchModel && modelName != constant.ToolsFetchModel
	}
	return modelName == toolsBillingModel(contract, "")
}

func summarizeToolProvider(provider string, supply toolSupply) WebSearchProviderSummary {
	summary := WebSearchProviderSummary{Provider: provider, Contracts: []abstract.Contract{}, NativeContracts: []abstract.Contract{}, ContractDetails: []WebSearchProviderContractSummary{}}
	summary.GlobalCredentialConfigured = provider == websearch_setting.ProviderExa && strings.TrimSpace(supply.settings.ExaAPIKey) != ""
	summary.LocalFallback = provider == websearch_setting.ProviderLocal
	available := make(map[int]bool)
	missingMapping := make(map[int]bool)
	for _, contract := range toolsContracts {
		if !abstract.CapabilitiesForProvider(provider).Supports(contract) {
			continue
		}
		summary.Contracts = append(summary.Contracts, contract)
		detail := WebSearchProviderContractSummary{Contract: contract, Native: abstract.SpeaksNatively(provider, contract), AvailableByGroup: map[string]int{}}
		if detail.Native {
			summary.NativeContracts = append(summary.NativeContracts, contract)
		}
		policy := webSearchGroupPolicy{contract: string(contract), settings: supply.settings}
		detail.BuiltinFallbackAvailable = hasToolProvider(policy.builtinToolsCandidates("", toolsBillingModel(contract, "")), provider, false)
		for index := range supply.channels {
			channel := &supply.channels[index]
			groups := supply.groups[channel.Id][contract]
			if len(groups) == 0 {
				continue
			}
			candidates, reasons := policy.channelQualification("", channel, true)
			for _, reason := range reasons {
				if reason.Provider == provider && reason.Reason == abstract.EligibilityMissingMapping {
					detail.MissingMappingChannels++
					missingMapping[channel.Id] = true
					break
				}
			}
			if !hasToolProvider(candidates, provider, true) {
				continue
			}
			detail.AvailableChannels++
			available[channel.Id] = true
			for group := range groups {
				if hasToolProvider(policy.candidates(group, channel), provider, true) {
					detail.AvailableByGroup[group]++
				}
			}
		}
		summary.ContractDetails = append(summary.ContractDetails, detail)
	}
	summary.AvailableChannels, summary.MissingMappingChannels = len(available), len(missingMapping)
	return summary
}

func hasToolProvider(candidates []abstract.Candidate, provider string, channelOnly bool) bool {
	for _, candidate := range candidates {
		if candidate.Provider == provider && (!channelOnly || candidate.Origin != abstract.OriginGlobalCredential && candidate.Origin != abstract.OriginLocal) {
			return true
		}
	}
	return false
}

type WebSearchSimulationInput struct {
	Group     string            `json:"group"`
	UserGroup string            `json:"user_group"`
	Contract  abstract.Contract `json:"contract"`
	ChannelID int               `json:"channel_id"`
	Model     string            `json:"model"`
}

type WebSearchSimulationStep struct {
	Group  string `json:"group"`
	Reason string `json:"reason"`
}

type WebSearchSimulationCandidate struct {
	Provider string `json:"provider"`
	Native   bool   `json:"native"`
	Origin   string `json:"origin"`
}

type WebSearchSimulationEligibility struct {
	Group     string                     `json:"group"`
	ChannelID int                        `json:"channel_id,omitempty"`
	Provider  string                     `json:"provider,omitempty"`
	Origin    string                     `json:"origin,omitempty"`
	Reason    abstract.EligibilityReason `json:"reason"`
}

type WebSearchSimulationChannel struct {
	Group        string                         `json:"group"`
	ChannelID    int                            `json:"channel_id"`
	ChannelName  string                         `json:"channel_name"`
	MatchedModel string                         `json:"matched_model"`
	Priority     int64                          `json:"priority"`
	Weight       uint                           `json:"weight"`
	FirstAttempt bool                           `json:"first_attempt"`
	Providers    []WebSearchSimulationCandidate `json:"providers"`
}

type WebSearchSimulation struct {
	Channels              []WebSearchSimulationChannel     `json:"channels"`
	Preflight             bool                             `json:"preflight"`
	AuthorizationVerified bool                             `json:"authorization_verified"`
	Eligibility           []WebSearchSimulationEligibility `json:"eligibility"`
	ChannelIsSample       bool                             `json:"channel_is_sample"`
	Status                int                              `json:"status"`
	Group                 string                           `json:"group,omitempty"`
	ChannelID             int                              `json:"channel_id,omitempty"`
	ChannelName           string                           `json:"channel_name,omitempty"`
	ChannelType           int                              `json:"channel_type,omitempty"`
	Source                string                           `json:"source,omitempty"`
	Candidates            []WebSearchSimulationCandidate   `json:"candidates"`
	Steps                 []WebSearchSimulationStep        `json:"steps"`
	BillingModel          string                           `json:"billing_model"`
	PricingStatus         string                           `json:"pricing_status"`
	BillingExpression     string                           `json:"billing_expression,omitempty"`
	LegacyPrice           *float64                         `json:"legacy_price,omitempty"`
	LegacyModelRatio      *float64                         `json:"legacy_model_ratio,omitempty"`
	ToolPricePerThousand  *float64                         `json:"tool_price_per_thousand,omitempty"`
	GroupRatio            *float64                         `json:"group_ratio,omitempty"`
	WeightedSelection     bool                             `json:"weighted_selection"`
}

func toolsBillingModel(contract abstract.Contract, modelName string) string {
	if contract == abstract.ContractAlphaSearch {
		return modelName
	}
	if contract.IsSearch() {
		return constant.ToolsSearchModel
	}
	return constant.ToolsFetchModel
}

func simulationGroups(input WebSearchSimulationInput) []string {
	if input.Group == "auto" {
		userGroup := input.UserGroup
		if userGroup == "" {
			userGroup = "default"
		}
		return GetUserAutoGroup(userGroup)
	}
	if ratio_setting.ContainsGroupRatio(input.Group) {
		return []string{input.Group}
	}
	return nil
}

func SimulateWebSearchRouting(input WebSearchSimulationInput) (WebSearchSimulation, error) {
	valid := false
	for _, contract := range toolsContracts {
		valid = valid || input.Contract == contract
	}
	modelName := toolsBillingModel(input.Contract, strings.TrimSpace(input.Model))
	if !valid || modelName == "" || len(modelName) > 255 || input.ChannelID < 0 || (input.Group != "auto" && !ratio_setting.ContainsGroupRatio(input.Group)) || (input.UserGroup != "" && !ratio_setting.ContainsGroupRatio(input.UserGroup)) {
		return WebSearchSimulation{}, ErrInvalidWebSearchSimulation
	}
	groups := simulationGroups(input)
	policy := webSearchGroupPolicy{contract: string(input.Contract), settings: websearch_setting.Get()}
	result := WebSearchSimulation{Status: http.StatusServiceUnavailable, Channels: []WebSearchSimulationChannel{}, Candidates: []WebSearchSimulationCandidate{}, Steps: []WebSearchSimulationStep{}, Eligibility: []WebSearchSimulationEligibility{}, BillingModel: modelName, PricingStatus: "missing", Preflight: true}
	userGroup := input.UserGroup
	if userGroup == "" {
		userGroup = input.Group
		if userGroup == "auto" {
			userGroup = "default"
		}
	}
	if input.UserGroup != "" && input.Group != "auto" && !IsUserSelectableGroup(userGroup, input.Group) {
		result.Status = http.StatusForbidden
		result.Steps = append(result.Steps, WebSearchSimulationStep{input.Group, "group_not_selectable"})
		result.describePrice(input)
		return result, nil
	}
	supply, err := readSimulationSupply(input, groups, modelName)
	if err != nil {
		return WebSearchSimulation{}, err
	}
	anyAllowed, matchingPin, qualifiedPin := false, false, false
	filteredGroups := make(map[string]bool)
	for _, group := range groups {
		if !policy.disabled(group) {
			anyAllowed = true
			matched := supply.activeAbility(group, input.ChannelID) != ""
			matchingPin = matchingPin || matched
			if supply.fixed && matched {
				qualifiedPin = qualifiedPin || len(policy.candidates(group, &supply.channels[0])) != 0
			}
		}
		filteredGroups[group] = result.inspectGroup(policy, supply, group)
	}
	if supply.fixed && result.Status != http.StatusOK {
		channel := &supply.channels[0]
		switch {
		case (input.Group == "auto" && !matchingPin) || channel.Status != common.ChannelStatusEnabled:
			result.Status = http.StatusForbidden
		case (input.Group != "auto" || qualifiedPin) && !model.ChannelSupportsRequestPathReadOnly(channel, toolsContractPath(input.Contract), modelName):
			result.Status = http.StatusBadRequest
		}
	}
	if result.Status == http.StatusServiceUnavailable && input.ChannelID == 0 {
		for _, group := range groups {
			if !IsUserSelectableGroup(userGroup, group) {
				continue
			}
			if policy.disabled(group) {
				continue
			}
			_, reasons := policy.routing(group).Qualify(abstract.ChannelContext{}, input.Contract, abstract.Qualification{Builtin: true})
			result.addEligibility(group, 0, reasons)
			candidates := policy.builtinToolsCandidates(group, modelName)
			if len(candidates) == 0 || filteredGroups[group] {
				continue
			}
			result.Group, result.WeightedSelection = group, false
			if !hasBuiltinToolsPrice(modelName) {
				result.Status = http.StatusBadRequest
				result.Steps = append(result.Steps, WebSearchSimulationStep{group, "missing_price"})
				break
			}
			result.Status, result.Source = http.StatusOK, "builtin_fallback"
			result.Candidates = simulationProviders(candidates)
			result.Steps = append(result.Steps, WebSearchSimulationStep{group, "builtin_fallback"})
			break
		}
	}
	if !anyAllowed {
		result.Status = http.StatusForbidden
	}
	result.describePrice(input)
	return result, nil
}

var toolCandidateOrigins = [...]string{"operator_binding", "native_channel", "channel_upstream", "global_credential", "local"}

func simulationProviders(candidates []abstract.Candidate) []WebSearchSimulationCandidate {
	providers := make([]WebSearchSimulationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		providers = append(providers, WebSearchSimulationCandidate{candidate.Provider, candidate.Native, toolCandidateOrigins[candidate.Origin]})
	}
	return providers
}

type simulationSupply struct {
	channels []model.Channel
	byGroup  map[string]map[int]map[string]model.Ability
	models   []string
	fixed    bool
	memory   bool
}

func readSimulationSupply(input WebSearchSimulationInput, groups []string, modelName string) (simulationSupply, error) {
	supply := simulationSupply{
		byGroup: make(map[string]map[int]map[string]model.Ability),
		models:  toolsRoutingModels(modelName), fixed: input.ChannelID != 0, memory: common.MemoryCacheEnabled,
	}
	abilitiesQuery := model.DB.Model(&model.Ability{}).
		Where(clause.IN{Column: clause.Column{Name: "group"}, Values: simulationGroupValues(groups)}).
		Where("model IN ?", supply.models)
	channelsQuery := model.DB.Select("id", "name", "type", "status", "priority", "weight", "key", "channel_info", "setting", "settings", "model_mapping", "base_url", "custom_tools")
	if supply.fixed {
		abilitiesQuery = abilitiesQuery.Where("channel_id = ?", input.ChannelID)
		channelsQuery = channelsQuery.Where("id = ?", input.ChannelID)
	} else {
		channelsQuery = channelsQuery.Where("id IN (?)", abilitiesQuery.Session(&gorm.Session{}).Select("channel_id"))
	}
	if err := channelsQuery.Order("id").Find(&supply.channels).Error; err != nil {
		return supply, err
	}
	if supply.fixed && len(supply.channels) == 0 {
		return supply, ErrInvalidWebSearchSimulation
	}
	var abilities []model.Ability
	if err := abilitiesQuery.Find(&abilities).Error; err != nil {
		return supply, err
	}
	for _, ability := range abilities {
		byChannel := supply.byGroup[ability.Group]
		if byChannel == nil {
			byChannel = make(map[int]map[string]model.Ability)
			supply.byGroup[ability.Group] = byChannel
		}
		if byChannel[ability.ChannelId] == nil {
			byChannel[ability.ChannelId] = make(map[string]model.Ability)
		}
		byChannel[ability.ChannelId][ability.Model] = ability
	}
	return supply, nil
}

func simulationGroupValues(groups []string) []any {
	values := make([]any, len(groups))
	for index, group := range groups {
		values[index] = group
	}
	return values
}

func (supply simulationSupply) activeAbility(group string, channelID int) string {
	for _, name := range supply.models {
		if supply.byGroup[group][channelID][name].Enabled {
			return name
		}
	}
	return ""
}

func (result *WebSearchSimulation) addEligibility(group string, channelID int, reasons []abstract.CandidateEligibility) {
	for _, reason := range reasons {
		if reason.Reason == abstract.EligibilityReady {
			continue
		}
		entry := WebSearchSimulationEligibility{Group: group, ChannelID: channelID, Provider: reason.Provider, Reason: reason.Reason}
		if reason.Provider != "" {
			entry.Origin = toolCandidateOrigins[reason.Origin]
		}
		result.Eligibility = append(result.Eligibility, entry)
	}
}

func (result *WebSearchSimulation) inspectGroup(policy webSearchGroupPolicy, supply simulationSupply, group string) bool {
	if policy.disabled(group) {
		result.Steps = append(result.Steps, WebSearchSimulationStep{group, "group_disabled"})
		return false
	}
	start := len(result.Channels)
	var eligible []toolsChannelRoute
	filtered := false
	path := toolsContractPath(abstract.Contract(policy.contract))
	modelName := supply.models[0]
	for index := range supply.channels {
		channel := &supply.channels[index]
		if !supply.fixed && supply.byGroup[group][channel.Id] == nil {
			continue
		}
		matched := supply.activeAbility(group, channel.Id)
		providers, reasons := policy.channelQualification(group, channel, matched != "")
		result.addEligibility(group, channel.Id, reasons)
		pathOK := model.ChannelSupportsRequestPathReadOnly(channel, path, modelName)
		if !pathOK && (len(providers) != 0 || (supply.fixed && channel.Status == common.ChannelStatusEnabled)) {
			filtered = filtered || len(providers) != 0
			result.Eligibility = append(result.Eligibility, WebSearchSimulationEligibility{Group: group, ChannelID: channel.Id, Reason: "request_path"})
		}
		if len(providers) == 0 || !pathOK {
			continue
		}
		route := supply.byGroup[group][channel.Id][matched].ChannelRoute(channel, supply.memory)
		eligible = append(eligible, toolsChannelRoute{route, providers})
	}
	preferred := policy.settings.PreferredProviders(group, policy.contract)
	sort.SliceStable(eligible, func(left, right int) bool {
		first, second := eligible[left], eligible[right]
		firstRank, secondRank := toolsProviderRank(preferred, first.providers), toolsProviderRank(preferred, second.providers)
		if firstRank != secondRank {
			return firstRank < secondRank
		}
		if first.Model != second.Model {
			return first.Model == modelName
		}
		return first.Priority > second.Priority
	})
	firstAttempt := make(map[int]bool)
	if result.Status != http.StatusOK {
		for _, route := range policy.channelAttempt(group, modelName, eligible, 0, supply.memory) {
			firstAttempt[route.Channel.Id] = route.weight > 0
		}
	}
	for _, route := range eligible {
		result.Channels = append(result.Channels, WebSearchSimulationChannel{
			Group: group, ChannelID: route.Channel.Id, ChannelName: route.Channel.Name, MatchedModel: route.Model,
			Priority: route.Priority, Weight: route.Weight, Providers: simulationProviders(route.providers),
			FirstAttempt: firstAttempt[route.Channel.Id],
		})
	}
	if result.Status == http.StatusOK {
		return filtered
	}
	for _, channel := range result.Channels[start:] {
		if channel.FirstAttempt {
			result.describeChannelSelection(policy, group, channel, supply)
			return filtered
		}
	}
	reason := "no_eligible_channel"
	if supply.fixed {
		reason = "no_executable_provider"
	}
	result.Steps = append(result.Steps, WebSearchSimulationStep{group, reason})
	return filtered
}

func toolsContractPath(contract abstract.Contract) string {
	switch contract {
	case abstract.ContractToolsSearch:
		return "/v1/tools/search"
	case abstract.ContractToolsFetch:
		return "/v1/tools/fetch"
	case abstract.ContractAlphaSearch:
		return "/v1/alpha/search"
	case abstract.ContractMoonshotSearch:
		return relayconstant.MoonshotSearchPath
	case abstract.ContractMoonshotFetch:
		return relayconstant.MoonshotFetchPath
	default:
		return ""
	}
}

func (result *WebSearchSimulation) describeChannelSelection(policy webSearchGroupPolicy, group string, sample WebSearchSimulationChannel, supply simulationSupply) {
	result.Status, result.Group = http.StatusOK, group
	result.ChannelID, result.ChannelName = sample.ChannelID, sample.ChannelName
	for _, channel := range supply.channels {
		if channel.Id == sample.ChannelID {
			result.ChannelType = channel.Type
			break
		}
	}
	result.Source = "global_candidate_order"
	if mode := policy.mode(group); mode == websearch_setting.GroupPolicyOnly || mode == websearch_setting.GroupPolicyPrefer {
		result.Source = "group_" + string(mode)
	}
	result.Candidates = sample.Providers
	result.WeightedSelection, result.ChannelIsSample = !supply.fixed, !supply.fixed
	reason := "selected"
	if result.ChannelIsSample {
		reason = "weighted_sample"
	}
	result.Steps = append(result.Steps, WebSearchSimulationStep{group, reason})
}

func (result *WebSearchSimulation) describePrice(input WebSearchSimulationInput) {
	if result.Group != "" {
		userGroup := input.UserGroup
		if userGroup == "" {
			userGroup = input.Group
			if userGroup == "auto" {
				userGroup = "default"
			}
		}
		ratio := GetUserGroupRatio(userGroup, result.Group)
		result.GroupRatio = &ratio
	}
	if billing_setting.GetBillingMode(result.BillingModel) == billing_setting.BillingModeTieredExpr {
		if expression, exists := billing_setting.GetBillingExpr(result.BillingModel); exists {
			result.BillingExpression = expression
			result.PricingStatus = "expression"
		}
	} else if price, exists := ratio_setting.GetModelPrice(result.BillingModel, false); exists {
		result.LegacyPrice = &price
		result.PricingStatus = "fixed_price"
	} else if ratio, exists, _ := ratio_setting.GetModelRatio(result.BillingModel); exists {
		result.LegacyModelRatio = &ratio
		result.PricingStatus = "model_ratio"
	}
	if input.Contract == abstract.ContractAlphaSearch {
		price := operation_setting.GetToolPriceForModel(dto.BuildInToolWebSearchPreview, result.BillingModel)
		result.ToolPricePerThousand = &price
	}
}
