package reasoning

import (
	"testing"

	kit "github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/stretchr/testify/require"
)

func TestAutoEffortInclusiveThresholdAndOverflow(t *testing.T) {
	rules, err := ParseAutoEffortOption(`{"tiers":[{"max_scale":12,"effort":"low"}],"overflow_effort":"high"}`)
	require.NoError(t, err)
	effort, tier := rules.Tier(12)
	require.Equal(t, kit.EffortLow, effort)
	require.Equal(t, 1, tier)
	effort, tier = rules.Tier(13)
	require.Equal(t, kit.EffortHigh, effort)
	require.Zero(t, tier)
}

func TestAutoEffortRejectsUnorderedThresholdsAndInvalidRE2(t *testing.T) {
	for _, input := range []string{
		`{"tiers":[{"max_scale":12,"effort":"low"},{"max_scale":12,"effort":"high"}]}`,
		`{"model_patterns":["re:(?=gpt)"]}`,
	} {
		_, err := ParseAutoEffortOption(input)
		require.Error(t, err)
	}
}

func TestAutoEffortCapDoesNotRaiseClientEffort(t *testing.T) {
	rules, err := ParseAutoEffortOption(`{"conflict_policy":"cap_to_auto"}`)
	require.NoError(t, err)
	decision := rules.Resolve(kit.Intent{Effort: kit.EffortLow}, 1501)
	require.Equal(t, "within_cap", decision.Reason)
	require.False(t, decision.Applied)
	require.Equal(t, kit.EffortLow, decision.EffectiveEffort)
	decision = rules.Resolve(kit.Intent{Effort: kit.EffortHigh}, 1501)
	require.Equal(t, "capped", decision.Reason)
	require.True(t, decision.Applied)
	require.Equal(t, 1501, decision.Scale)
	require.Equal(t, 2, decision.Tier)
	require.Equal(t, kit.EffortMedium, decision.TargetEffort)
	require.Equal(t, kit.EffortMedium, decision.EffectiveEffort)
	require.Equal(t, kit.SourceAutoScale, decision.Intent.Source)
}

func TestAutoEffortOverrideRespectsZeroBudget(t *testing.T) {
	rules, err := ParseAutoEffortOption(`{"conflict_policy":"always_override"}`)
	require.NoError(t, err)
	zero := 0
	decision := rules.Resolve(kit.Intent{BudgetTokens: &zero}, 8001)
	require.Equal(t, "explicit_disable", decision.Reason)
	require.False(t, decision.Applied)
	require.Equal(t, kit.EffortHigh, decision.TargetEffort)
	require.Equal(t, kit.EffortNone, decision.EffectiveEffort)

	visible := true
	decision = rules.Resolve(kit.Intent{Effort: kit.EffortLow, IncludeThoughts: &visible}, 32001)
	require.Equal(t, "overridden", decision.Reason)
	require.True(t, decision.Applied)
	require.Zero(t, decision.Tier)
	require.Equal(t, kit.EffortHigh, decision.EffectiveEffort)
	require.Equal(t, &visible, decision.Intent.IncludeThoughts)
}
