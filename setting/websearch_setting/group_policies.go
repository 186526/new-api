package websearch_setting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/toolcatalog"
)

const GroupPoliciesOptionKey = "websearch_setting.group_policies"

const (
	ContractToolsSearch    = toolcatalog.ToolsSearch
	ContractToolsFetch     = toolcatalog.ToolsFetch
	ContractAlphaSearch    = toolcatalog.AlphaSearch
	ContractMoonshotSearch = toolcatalog.MoonshotSearch
	ContractMoonshotFetch  = toolcatalog.MoonshotFetch
)

type GroupPolicyMode string

const (
	GroupPolicyInherit  GroupPolicyMode = "inherit"
	GroupPolicyDisabled GroupPolicyMode = "disabled"
	GroupPolicyPrefer   GroupPolicyMode = "prefer"
	GroupPolicyOnly     GroupPolicyMode = "only"
)

type groupPolicy struct {
	mode      GroupPolicyMode
	providers []string
}

func (setting WebSearchSetting) GroupPolicyMode(group, contract string) GroupPolicyMode {
	if policy, exists := setting.groupPolicies[group][contract]; exists {
		return policy.mode
	}
	return GroupPolicyInherit
}

func (setting WebSearchSetting) PreferredProviders(group, contract string) []string {
	return slices.Clone(setting.groupPolicies[group][contract].providers)
}

func ValidateGroupPolicies(raw string) error {
	_, err := parseGroupPolicies(raw)
	return err
}

func parseGroupPolicies(raw string) (map[string]map[string]groupPolicy, error) {
	groups, err := policyObject([]byte(raw))
	if err != nil {
		return nil, invalidGroupPolicies(err)
	}
	policies := make(map[string]map[string]groupPolicy, len(groups))
	for group, groupJSON := range groups {
		if group == "" || group != strings.TrimSpace(group) || group == "auto" {
			return nil, invalidGroupPolicies(fmt.Errorf("invalid group %q", group))
		}
		contracts, err := policyObject(groupJSON)
		if err != nil {
			return nil, invalidGroupPolicies(fmt.Errorf("group %q: %w", group, err))
		}
		groupPolicies := make(map[string]groupPolicy, len(contracts))
		for contract, policyJSON := range contracts {
			switch contract {
			case ContractToolsSearch, ContractToolsFetch, ContractAlphaSearch, ContractMoonshotSearch, ContractMoonshotFetch:
			default:
				return nil, invalidGroupPolicies(fmt.Errorf("group %q: unknown contract %q", group, contract))
			}
			policy, err := parseGroupPolicy(contract, policyJSON)
			if err != nil {
				return nil, invalidGroupPolicies(fmt.Errorf("group %q, contract %q: %w", group, contract, err))
			}
			groupPolicies[contract] = policy
		}
		policies[group] = groupPolicies
	}
	return policies, nil
}

func parseGroupPolicy(contract string, raw []byte) (groupPolicy, error) {
	fields, err := policyObject(raw)
	if err != nil {
		return groupPolicy{}, err
	}
	for field := range fields {
		if field != "mode" && field != "providers" {
			return groupPolicy{}, fmt.Errorf("unknown field %q", field)
		}
	}
	var policy groupPolicy
	const modeError = "mode must be inherit, disabled, prefer, or only"
	if err := json.Unmarshal(fields["mode"], &policy.mode); err != nil {
		return groupPolicy{}, fmt.Errorf(modeError)
	}
	providers, hasProviders := fields["providers"]
	switch policy.mode {
	case GroupPolicyInherit, GroupPolicyDisabled:
		if hasProviders {
			return groupPolicy{}, fmt.Errorf("providers are only allowed in prefer or only mode")
		}
	case GroupPolicyPrefer, GroupPolicyOnly:
		if !hasProviders || json.Unmarshal(providers, &policy.providers) != nil || len(policy.providers) == 0 {
			return groupPolicy{}, fmt.Errorf("providers must be a non-empty array in %s mode", policy.mode)
		}
		if err := validateGroupProviders(contract, policy.providers); err != nil {
			return groupPolicy{}, err
		}
	default:
		return groupPolicy{}, fmt.Errorf(modeError)
	}
	return policy, nil
}

func validateGroupProviders(contract string, providers []string) error {
	known := KnownProviders()
	for index, provider := range providers {
		if !slices.Contains(known, provider) {
			return fmt.Errorf("unknown provider %q", provider)
		}
		if !ProviderSupportsContract(provider, contract) {
			return fmt.Errorf("provider %q does not support contract %q", provider, contract)
		}
		if slices.Contains(providers[:index], provider) {
			return fmt.Errorf("duplicate provider %q", provider)
		}
	}
	return nil
}

func ProviderSupportsContract(provider, contract string) bool {
	return toolcatalog.Lookup(provider).Supports(toolcatalog.Contract(contract))
}

// policyObject rejects duplicate names before they can shadow a disabled rule.
func policyObject(raw []byte) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, fmt.Errorf("expected a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	_, _ = decoder.Token()
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name := key.(string)
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate key %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}
	return fields, nil
}

func invalidGroupPolicies(err error) error {
	return common.NewMessage("Invalid web search group policies: {{error}}", map[string]any{"error": err.Error()})
}
