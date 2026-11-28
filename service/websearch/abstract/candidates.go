package abstract

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// Candidate is one provider that can serve a contract.
type Candidate struct {
	Provider string
	// Native reports that this provider's upstream contract is the same
	// document the client asked for, so its answer can be forwarded byte for
	// byte. It only affects ordering: a non-native provider serves the same
	// contract just as well, through rendering.
	Native bool
	// Origin is why the candidate ranks where it does.
	Origin CandidateOrigin
}

// CandidateOrigin records where a candidate came from. It orders the candidates
// when the operator has not written an order of their own.
type CandidateOrigin int

const (
	// OriginOperatorBinding is the provider the channel's websearch_provider
	// setting names.
	OriginOperatorBinding CandidateOrigin = iota
	// OriginNativeChannel is the channel's own upstream, which speaks this
	// contract itself.
	OriginNativeChannel
	// OriginChannelUpstream is the channel's own upstream, which answers this
	// contract only by being rendered.
	OriginChannelUpstream
	// OriginGlobalCredential is a provider the global settings hold a key for,
	// so it can serve every channel.
	OriginGlobalCredential
	// OriginLocal is the in-process fetcher.
	OriginLocal
)

// Candidates returns every provider that can serve the contract, best first.
//
// The list is the full set of providers this deployment can reach, reordered by
// preference. It is not a permission list: a provider appears because it has a
// credential, and the order only says which one is tried first. An empty list
// is the only signal that the channel can serve nothing, which the relay layer
// turns into a retry on another channel.
func Candidates(channel ChannelContext, contract Contract) ([]Candidate, error) {
	return CandidatesWithSettings(channel, contract, websearch_setting.Get())
}

func CandidatesWithSettings(channel ChannelContext, contract Contract, settings websearch_setting.WebSearchSetting) ([]Candidate, error) {
	candidates, _ := qualifiedCandidates(channel, contract, settings, nil)
	if len(candidates) == 0 {
		return nil, ErrUnsupportedChannel
	}
	return candidates, nil
}

func qualifiedCandidates(channel ChannelContext, contract Contract, settings websearch_setting.WebSearchSetting, qualification *Qualification) ([]Candidate, []CandidateEligibility) {
	bound := channelOperatorBinding(channel.ChannelType, channel.Settings.WebSearchProvider).Provider
	if channel.CustomOperation(contract) != nil {
		bound = websearch_setting.ProviderExa
	}

	own := ChannelProvider(channel.ChannelType)

	candidates := make([]Candidate, 0, 5)
	var diagnostics []CandidateEligibility
	add := func(provider string, origin CandidateOrigin) {
		candidate := Candidate{Provider: provider, Native: SpeaksNatively(provider, contract), Origin: origin}
		reason := candidateReason(channel, contract, candidate)
		if qualification != nil {
			if reason == EligibilityReady {
				reason = qualification.reason(channel, settings, contract, candidate)
			}
			diagnostics = append(diagnostics, CandidateEligibility{Candidate: candidate, Reason: reason})
		}
		if reason != EligibilityReady || slices.ContainsFunc(candidates, func(existing Candidate) bool { return existing.Provider == provider }) {
			return
		}
		candidates = append(candidates, candidate)
	}

	// The channel's own upstream always participates; whether it speaks the
	// contract only decides its Native flag and, without an operator order, its
	// rank.
	if own != "" {
		if SpeaksNatively(own, contract) {
			add(own, OriginNativeChannel)
		} else {
			add(own, OriginChannelUpstream)
		}
	}
	if bound != "" && bound != own {
		add(bound, OriginOperatorBinding)
	}
	if qualification != nil || strings.TrimSpace(settings.ExaAPIKey) != "" {
		add(websearch_setting.ProviderExa, OriginGlobalCredential)
	}
	if !contract.IsSearch() {
		add(websearch_setting.ProviderLocal, OriginLocal)
	}

	if len(candidates) == 0 {
		return nil, diagnostics
	}

	order := strings.Split(settings.CandidateOrder, ",")
	if settings.CandidateOrder == "" {
		order = nil
	} else {
		for index := range order {
			order[index] = strings.ToLower(strings.TrimSpace(order[index]))
		}
	}
	if len(order) > 0 || !settings.NativeFirst || bound != "" {
		rankCandidates(candidates, order, bound, settings.NativeFirst)
	}
	if len(channel.PreferredProviders) != 0 {
		positions := make(map[string]int, len(channel.PreferredProviders))
		for index, provider := range channel.PreferredProviders {
			positions[provider] = index + 1
		}
		sortCandidates(candidates, func(first, second Candidate) bool {
			firstRank, secondRank := positions[first.Provider], positions[second.Provider]
			return firstRank != 0 && (secondRank == 0 || firstRank < secondRank)
		})
	}
	pinFetchFallback(candidates, contract)
	return candidates, diagnostics
}

// rankCandidates applies the operator's preference. It reorders only: every
// candidate stays in the list, because a provider that lost the rank still has
// a credential and can still answer.
func rankCandidates(candidates []Candidate, order []string, bound string, nativeFirst bool) {
	position := make(map[string]int, len(order))
	for index, provider := range order {
		position[provider] = index
	}
	originRank := func(candidate Candidate) int {
		if !nativeFirst && candidate.Origin != OriginOperatorBinding {
			return int(OriginChannelUpstream) + 1
		}
		return int(candidate.Origin)
	}
	sortCandidates(candidates, func(a, b Candidate) bool {
		if bound != "" {
			aBound, bBound := a.Provider == bound, b.Provider == bound
			if aBound != bBound {
				return aBound
			}
		}
		aPos, aListed := position[a.Provider]
		bPos, bListed := position[b.Provider]
		if aListed != bListed {
			return aListed
		}
		if aListed && aPos != bPos {
			return aPos < bPos
		}
		if rankA, rankB := originRank(a), originRank(b); rankA != rankB {
			return rankA < rankB
		}
		return a.Provider < b.Provider
	})
}

// pinFetchFallback keeps the in-process fetcher last: it is the fallback, so an
// operator order that names it first must not change that.
func pinFetchFallback(candidates []Candidate, contract Contract) {
	if contract.IsSearch() {
		return
	}
	for index, candidate := range candidates {
		if candidate.Provider != websearch_setting.ProviderLocal {
			continue
		}
		local := candidate
		copy(candidates[index:], candidates[index+1:])
		candidates[len(candidates)-1] = local
		return
	}
}
