package reasoning

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	kit "github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/setting/config"
)

const AutoEffortOptionKey = "reasoning_setting.auto_effort_policy"

type AutoEffortTier struct {
	MaxScale int        `json:"max_scale"`
	Effort   kit.Effort `json:"effort"`
}

type AutoEffortPolicy struct {
	Enabled                bool             `json:"enabled"`
	ScaleMetric            string           `json:"scale_metric"`
	ConflictPolicy         string           `json:"conflict_policy"`
	RespectExplicitDisable bool             `json:"respect_explicit_disable"`
	Tiers                  []AutoEffortTier `json:"tiers"`
	OverflowEffort         kit.Effort       `json:"overflow_effort"`
	Groups                 []string         `json:"groups"`
	ModelPatterns          []string         `json:"model_patterns"`
	ExcludeModelPatterns   []string         `json:"exclude_model_patterns"`
}

type AutoEffortRules struct {
	policy           AutoEffortPolicy
	include, exclude []func(string) bool
}

var activeAutoEffort atomic.Pointer[AutoEffortRules]

func DefaultAutoEffortPolicy() AutoEffortPolicy {
	return AutoEffortPolicy{
		ScaleMetric: "estimated_tokens", ConflictPolicy: "only_when_unset",
		RespectExplicitDisable: true,
		Tiers:                  []AutoEffortTier{{1500, kit.EffortLow}, {8000, kit.EffortMedium}, {32000, kit.EffortHigh}},
		OverflowEffort:         kit.EffortHigh,
		Groups:                 []string{}, ModelPatterns: []string{}, ExcludeModelPatterns: []string{},
	}
}

func init() {
	defaults := DefaultAutoEffortPolicy()
	raw, _ := json.Marshal(defaults)
	config.GlobalConfig.Register("reasoning_setting", &struct {
		AutoEffortPolicy string `json:"auto_effort_policy"`
	}{AutoEffortPolicy: string(raw)})
	_ = LoadAutoEffortOption(string(raw))
}

func CurrentAutoEffort() *AutoEffortRules { return activeAutoEffort.Load() }

func LoadAutoEffortOption(raw string) error {
	rules, err := ParseAutoEffortOption(raw)
	if err != nil {
		return err
	}
	activeAutoEffort.Store(rules)
	return nil
}

func ParseAutoEffortOption(raw string) (*AutoEffortRules, error) {
	if len(raw) > 32<<10 || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return nil, fmt.Errorf("auto_effort_policy must be a JSON object of at most 32 KiB")
	}
	policy := DefaultAutoEffortPolicy()
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return nil, fmt.Errorf("invalid auto_effort_policy: %w", err)
	}
	return compileAutoEffort(policy)
}

func validAutoEffort(effort kit.Effort) bool {
	return slices.Contains([]kit.Effort{kit.EffortNone, kit.EffortMinimal, kit.EffortLow, kit.EffortMedium, kit.EffortHigh, kit.EffortXHigh}, effort)
}

func compileAutoEffort(policy AutoEffortPolicy) (*AutoEffortRules, error) {
	if policy.ScaleMetric != "estimated_tokens" && policy.ScaleMetric != "input_chars" {
		return nil, fmt.Errorf("scale_metric must be estimated_tokens or input_chars")
	}
	switch policy.ConflictPolicy {
	case "only_when_unset", "cap_to_auto", "always_override":
	default:
		return nil, fmt.Errorf("invalid conflict_policy")
	}
	if len(policy.Tiers) < 1 || len(policy.Tiers) > 6 || !validAutoEffort(policy.OverflowEffort) {
		return nil, fmt.Errorf("specify one to six tiers and a valid overflow_effort")
	}
	previous := 0
	for i, tier := range policy.Tiers {
		if tier.MaxScale <= previous || tier.MaxScale > 1_000_000_000 || !validAutoEffort(tier.Effort) {
			return nil, fmt.Errorf("tiers.%d requires a strictly increasing max_scale and a valid effort", i)
		}
		previous = tier.MaxScale
	}
	if err := validateAutoEffortList(policy.Groups, "groups"); err != nil {
		return nil, err
	}
	include, err := compileAutoEffortPatterns(policy.ModelPatterns, "model_patterns")
	if err != nil {
		return nil, err
	}
	exclude, err := compileAutoEffortPatterns(policy.ExcludeModelPatterns, "exclude_model_patterns")
	if err != nil {
		return nil, err
	}
	return &AutoEffortRules{policy: policy, include: include, exclude: exclude}, nil
}

func validateAutoEffortList(values []string, field string) error {
	if len(values) > 32 {
		return fmt.Errorf("%s accepts at most 32 entries", field)
	}
	for index, value := range values {
		if len(value) > 256 || strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || slices.Contains(values[:index], value) {
			return fmt.Errorf("%s.%d must be a distinct, non-empty entry of at most 256 bytes", field, index)
		}
	}
	return nil
}

func compileAutoEffortPatterns(values []string, field string) ([]func(string) bool, error) {
	if err := validateAutoEffortList(values, field); err != nil {
		return nil, err
	}
	matchers := make([]func(string) bool, 0, len(values))
	for index, value := range values {
		switch {
		case strings.HasPrefix(value, "re:"):
			pattern := strings.TrimPrefix(value, "re:")
			expression, err := regexp.Compile(pattern)
			if err != nil || pattern == "" {
				return nil, fmt.Errorf("%s.%d is not a valid RE2 expression", field, index)
			}
			matchers = append(matchers, expression.MatchString)
		case strings.Contains(value, "*"):
			if _, err := path.Match(value, ""); err != nil {
				return nil, fmt.Errorf("%s.%d is not a valid wildcard pattern", field, index)
			}
			matchers = append(matchers, func(name string) bool {
				matched, _ := path.Match(value, name)
				return matched
			})
		default:
			matchers = append(matchers, func(name string) bool { return strings.HasPrefix(name, value) })
		}
	}
	return matchers, nil
}

func (rules *AutoEffortRules) Enabled() bool  { return rules != nil && rules.policy.Enabled }
func (rules *AutoEffortRules) Metric() string { return rules.policy.ScaleMetric }

func (rules *AutoEffortRules) Scope(group, origin, mapped string) string {
	if len(rules.policy.Groups) > 0 && !slices.Contains(rules.policy.Groups, group) {
		return "group_excluded"
	}
	if matchesAutoEffortModels(rules.exclude, origin, mapped) || len(rules.include) > 0 && !matchesAutoEffortModels(rules.include, origin, mapped) {
		return "model_excluded"
	}
	return ""
}

func matchesAutoEffortModels(matchers []func(string) bool, origin, mapped string) bool {
	return slices.ContainsFunc(matchers, func(match func(string) bool) bool { return match(origin) || match(mapped) })
}

func (rules *AutoEffortRules) Tier(scale int) (kit.Effort, int) {
	for index, tier := range rules.policy.Tiers {
		if scale <= tier.MaxScale {
			return tier.Effort, index + 1
		}
	}
	return rules.policy.OverflowEffort, 0
}

type AutoEffortDecision struct {
	Scale           int
	Tier            int
	TargetEffort    kit.Effort
	EffectiveEffort kit.Effort
	Intent          kit.Intent
	Reason          string
	Applied         bool
}

func (rules *AutoEffortRules) Resolve(explicit kit.Intent, scale int) AutoEffortDecision {
	goal, tier := rules.Tier(scale)
	current := kit.EffectiveEffort(explicit)
	decision := AutoEffortDecision{
		Scale: scale, Tier: tier, TargetEffort: goal,
		EffectiveEffort: current, Intent: explicit, Reason: "applied",
	}
	if rules.policy.RespectExplicitDisable && current == kit.EffortNone {
		decision.Reason = "explicit_disable"
		return decision
	}
	if explicit.HasStrength() {
		switch rules.policy.ConflictPolicy {
		case "only_when_unset":
			decision.Reason = "explicit_strength"
			return decision
		case "cap_to_auto":
			order := []kit.Effort{kit.EffortNone, kit.EffortMinimal, kit.EffortLow, kit.EffortMedium, kit.EffortHigh, kit.EffortXHigh, kit.EffortMax}
			if slices.Index(order, current) <= slices.Index(order, goal) {
				decision.Reason = "within_cap"
				return decision
			}
			decision.Reason = "capped"
		case "always_override":
			decision.Reason = "overridden"
		}
	}
	mode := kit.ModeEnabled
	if goal == kit.EffortNone {
		mode = kit.ModeDisabled
	}
	decision.Applied = true
	decision.EffectiveEffort = goal
	decision.Intent = kit.Intent{Mode: mode, Effort: goal, IncludeThoughts: explicit.IncludeThoughts, Source: kit.SourceAutoScale}
	return decision
}
