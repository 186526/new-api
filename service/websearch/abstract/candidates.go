package abstract

import (
	"strings"

	"github.com/QuantumNous/new-api/constant"
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

// nativeContracts names the contracts each provider speaks itself. A provider
// missing from this table serves nothing natively; one listed for a contract
// answers that document without rendering.
var nativeContracts = map[string][]Contract{
	websearch_setting.ProviderDeepSeek: {
		ContractToolsSearch,
		ContractToolsFetch,
	},
	websearch_setting.ProviderCodex: {
		ContractAlphaSearch,
	},
}

// SpeaksNatively reports whether the provider's upstream publishes the
// contract itself.
func SpeaksNatively(provider string, contract Contract) bool {
	for _, known := range nativeContracts[provider] {
		if known == contract {
			return true
		}
	}
	return false
}

// channelProvider is the provider a channel type maps to on its own, empty when
// the type has no web search upstream.
func channelProvider(channelType int) string {
	switch channelType {
	case constant.ChannelTypeAntigravity:
		return websearch_setting.ProviderAntigravity
	case constant.ChannelTypeDeepSeek,
		constant.ChannelTypeNewAPI,
		constant.ChannelTypeSub2API:
		return websearch_setting.ProviderDeepSeek
	case constant.ChannelTypeCodex:
		return websearch_setting.ProviderCodex
	default:
		return ""
	}
}

// Candidates returns every provider that can serve the contract, best first.
//
// The list is the full set of providers this deployment can reach, reordered by
// preference. It is not a permission list: a provider appears because it has a
// credential, and the order only says which one is tried first. An empty list
// is the only signal that the channel can serve nothing, which the relay layer
// turns into a retry on another channel.
func Candidates(channel ChannelContext, contract Contract) ([]Candidate, error) {
	settings := websearch_setting.Get()

	bound := ""
	switch strings.ToLower(strings.TrimSpace(channel.Settings.WebSearchProvider)) {
	case "":
	case websearch_setting.ProviderAntigravity,
		websearch_setting.ProviderDeepSeek,
		websearch_setting.ProviderExa:
		bound = strings.ToLower(strings.TrimSpace(channel.Settings.WebSearchProvider))
	default:
		// A name this build does not know is treated as no binding rather than
		// as a refusal: the channel's own upstream still serves the request.
	}

	own := channelProvider(channel.ChannelType)

	candidates := make([]Candidate, 0, 5)
	add := func(provider string, origin CandidateOrigin) {
		if provider == "" {
			return
		}
		for _, existing := range candidates {
			if existing.Provider == provider {
				return
			}
		}
		candidates = append(candidates, Candidate{
			Provider: provider,
			Native:   SpeaksNatively(provider, contract),
			Origin:   origin,
		})
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
	for _, provider := range websearch_setting.GlobalProviders() {
		if provider == bound || provider == own {
			continue
		}
		add(provider, OriginGlobalCredential)
	}
	// The in-process fetcher answers any fetch without a credential. A search
	// has no local implementation, so it never appears there.
	if !contract.IsSearch() {
		add(websearch_setting.ProviderLocal, OriginLocal)
	}

	if len(candidates) == 0 {
		return nil, ErrUnsupportedChannel
	}

	order := websearch_setting.ProviderOrder()
	if len(order) > 0 || !settings.NativeFirst || bound != "" {
		rankCandidates(candidates, order, bound, settings.NativeFirst)
	}
	pinFetchFallback(candidates, contract)
	return candidates, nil
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
