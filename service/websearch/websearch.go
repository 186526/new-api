package websearch

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/QuantumNous/new-api/service/websearch/response"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// dropRepeatedSiteResults removes a result that repeats another one's site and
// snippet. A page the model listed under two of its own URLs yields one hit
// carrying the same description twice.
//
// A site's separate pages stay listed: the snippet has to match as well, so a
// site's homepage and its documentation are both kept.
func dropRepeatedSiteResults(results []abstract.Result) []abstract.Result {
	if len(results) < 2 {
		return results
	}
	kept := results[:0]
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		// An empty snippet identifies nothing, so a result without one is kept
		// rather than compared: two such results would otherwise collapse.
		if snippet := strings.TrimSpace(result.Snippet); snippet != "" {
			key := searchResultHost(result.URL) + "\x00" + snippet
			if _, duplicated := seen[key]; duplicated {
				continue
			}
			seen[key] = struct{}{}
		}
		kept = append(kept, result)
	}
	return kept
}

// searchResultHost names the site a result belongs to. A URL the parser could not
// read keeps its whole text, which then matches only an identical entry.
func searchResultHost(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Host == "" {
		return strings.TrimSpace(target)
	}
	return strings.ToLower(parsed.Host)
}

// Answer is what a relay handler writes back: the client's own contract built
// from whichever provider answered it.
type Answer struct {
	Body        []byte
	ContentType string
	StatusCode  int
	Header      http.Header
	Provider    string
	// Cached reports that this answer came from the cache rather than from an
	// upstream call, so a caller can count the hit in its metrics.
	Cached bool
}

var (
	ErrUnsupportedChannel = abstract.ErrUnsupportedChannel
	ErrEmptyResults       = abstract.ErrEmptyResults
	ErrEmptyContent       = abstract.ErrEmptyContent
)

// Search answers a search contract using the ordered requesters available for it.
func Search(ctx context.Context, channel abstract.ChannelContext, query request.Query) (Answer, error) {
	items, err := requestSearch(ctx, channel, query)
	if err != nil {
		return Answer{}, err
	}
	return answer(ctx, query, items)
}

// Fetch answers a fetch contract using the ordered requesters available for it.
func Fetch(ctx context.Context, channel abstract.ChannelContext, query request.Query) (Answer, error) {
	items, err := requestFetch(ctx, channel, query)
	if err != nil {
		return Answer{}, err
	}
	return answer(ctx, query, items)
}

// Candidates exposes the ordered provider list, so a caller can log what it is
// about to try without depending on the abstract package directly.
func Candidates(channel abstract.ChannelContext, contract abstract.Contract) ([]abstract.Candidate, error) {
	return abstract.Candidates(channel, contract)
}

// LogCandidates writes the candidate order at debug level, which is the only
// record of why one provider was tried before another.
func LogCandidates(ctx context.Context, channel abstract.ChannelContext, contract abstract.Contract) {
	candidates, err := abstract.CandidatesForRequest(ctx, channel, contract)
	if err != nil {
		logger.LogDebug(ctx, "websearch: no candidate for %s: %v", contract, err)
		return
	}
	names := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		names = append(names, candidate.Provider)
	}
	logger.LogDebug(ctx, "websearch: candidates for %s: %v", contract, names)
}

type requesterItem struct {
	candidate abstract.Candidate
	channel   abstract.ChannelContext
	query     request.Query
	send      func(context.Context, abstract.ChannelContext, request.Query) (request.Upstream, error)
}

func requestSearch(ctx context.Context, channel abstract.ChannelContext, query request.Query) ([]requesterItem, error) {
	if !query.Contract.IsSearch() {
		return nil, ErrUnsupportedChannel
	}
	return discoverRequesters(ctx, channel, query)
}

func requestFetch(ctx context.Context, channel abstract.ChannelContext, query request.Query) ([]requesterItem, error) {
	if query.Contract.IsSearch() {
		return nil, ErrUnsupportedChannel
	}
	return discoverRequesters(ctx, channel, query)
}

func discoverRequesters(ctx context.Context, channel abstract.ChannelContext, query request.Query) ([]requesterItem, error) {
	candidates, settings := requestCandidates(ctx, channel, query)

	items := make([]requesterItem, 0, len(candidates))
	for _, candidate := range candidates {
		requester, err := request.For(candidate.Provider)
		if err != nil {
			continue
		}
		item := bindRequester(candidate, channel, query, settings)
		item.send = requester.Fetch
		if query.Contract.IsSearch() {
			item.send = requester.Search
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, ErrUnsupportedChannel
	}
	return items, nil
}

func bindRequester(candidate abstract.Candidate, channel abstract.ChannelContext, query request.Query, settings websearch_setting.WebSearchSetting) requesterItem {
	providedBy := query.EndpointProvider
	if providedBy == "" {
		providedBy = abstract.ChannelProvider(channel.ChannelType)
	}
	ownsPreparedRequest := providedBy == candidate.Provider
	switch candidate.Origin {
	case abstract.OriginGlobalCredential:
		channel = abstract.ChannelContext{BaseURL: settings.ExaBaseURL, Key: settings.ExaAPIKey}
		ownsPreparedRequest = false
	case abstract.OriginLocal:
		fetchTTL := channel.Settings.WebSearchFetchCacheTTLSeconds
		channel = abstract.ChannelContext{}
		channel.Settings.WebSearchFetchCacheTTLSeconds = fetchTTL
		ownsPreparedRequest = false
	case abstract.OriginOperatorBinding:
		if operation := channel.CustomOperation(query.Contract); operation != nil && candidate.Provider == websearch_setting.ProviderExa {
			channel.Key = ""
			query.CustomOperation = operation
		}
		ownsPreparedRequest = false
	}
	if !ownsPreparedRequest {
		query.Endpoint = ""
		query.EndpointProvider = ""
		query.Prepare = nil
		query.Model = ""
		query.Raw = nil
	}
	return requesterItem{candidate: candidate, channel: channel, query: query}
}

func requestCandidates(ctx context.Context, channel abstract.ChannelContext, query request.Query) ([]abstract.Candidate, websearch_setting.WebSearchSetting) {
	provider := query.EndpointProvider
	if provider == "" {
		provider = abstract.ChannelProvider(channel.ChannelType)
	}
	model := strings.TrimSpace(query.Model)
	input := abstract.Qualification{
		Builtin:        channel.ChannelID == 0 && channel.ChannelType == 0,
		ChannelEnabled: true, AbilityEnabled: true, ChannelCredentialSelectable: true,
		CredentialConfigured: strings.TrimSpace(channel.Key) != "",
		Preparation: &abstract.RequestPreparation{
			Provider: provider, EndpointConfigured: strings.TrimSpace(query.Endpoint) != "",
			HasHeaders: query.Prepare != nil, HasBody: len(query.Raw) != 0,
			HasMappedModel: model != "" && model != constant.ToolsSearchModel,
		},
	}
	if policy, ok := abstract.RequestRoutingPolicy(ctx); ok {
		candidates, _ := policy.Qualify(channel, query.Contract, input)
		return candidates, policy.Settings
	}
	settings := websearch_setting.Get()
	candidates, _ := abstract.QualifyWithSettings(channel, query.Contract, settings, input)
	return candidates, settings
}

func answer(ctx context.Context, query request.Query, items []requesterItem) (Answer, error) {
	renderer := response.For(query.Contract)
	if renderer == nil {
		return Answer{}, ErrUnsupportedChannel
	}

	var lastErr error
	for _, item := range items {
		upstream, err := item.send(ctx, item.channel, item.query)
		if err != nil {
			// A candidate is skipped only when its request provably never
			// reached the upstream. Once a request may have been billed, its
			// answer is what the client gets: trying another provider would pay
			// for one client request twice and could hand back another shape.
			if !request.WasSent(err) {
				lastErr = err
				continue
			}
			return Answer{}, err
		}

		return render(ctx, upstream, renderer, item.channel, item.candidate, query, query.Contract.IsSearch())
	}
	if lastErr == nil {
		lastErr = ErrUnsupportedChannel
	}
	return Answer{}, lastErr
}

// render turns one upstream answer into the client's contract. A native
// candidate's body is forwarded as it arrived, which is what keeps an existing
// channel's answer byte for byte what it was.
func render(ctx context.Context, upstream request.Upstream, renderer response.Renderer, channel abstract.ChannelContext, candidate abstract.Candidate, query request.Query, search bool) (Answer, error) {
	answer := Answer{
		ContentType: renderer.ContentType(),
		StatusCode:  upstream.StatusCode,
		Header:      upstream.Header,
		Provider:    candidate.Provider,
		Cached:      upstream.Cached,
	}

	// A refusal is the upstream's own document, not the client's contract: it
	// carries the message the error handler reports, so it is handed back
	// unparsed and unrendered.
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		answer.Body = upstream.Body
		return answer, nil
	}

	// A native candidate already speaks the client's contract, so its body is
	// what the client reads. Parsing it would fold duplicated URLs, drop
	// members this package does not model and turn an empty answer into an
	// error, none of which the client saw before.
	if candidate.Native {
		answer.Body = upstream.Body
		return answer, nil
	}

	parser, err := abstract.ParserFor(upstream.Family)
	if err != nil {
		return answer, err
	}

	if search {
		results, err := parser.ParseSearch(upstream.Body)
		if err != nil {
			return answer, err
		}
		parsed := abstract.Search{
			Results:    results,
			StatusCode: upstream.StatusCode,
			Header:     upstream.Header,
		}
		if resolvesCitations(answer.Provider) {
			request.ResolveSearchRedirects(ctx, channel, &parsed)
			if len(parsed.Results) == 0 {
				return answer, ErrEmptyResults
			}
		}
		if upstream.Family != hostedFamily && !(candidate.Origin == abstract.OriginOperatorBinding && channel.CustomOperation(query.Contract) != nil && candidate.Provider == websearch_setting.ProviderExa) {
			fillSnippets(ctx, channel, parsed.Results)
		}
		// A page the model listed twice under two of its own URLs still reads as
		// one hit: the second copy is dropped only when it also repeats the
		// first one's snippet, so a site's separate pages stay listed.
		parsed.Results = dropRepeatedSiteResults(parsed.Results)
		// A caller that named a count gets that many results. The cap is applied
		// here rather than at the upstream because the upstream decides its own
		// count, and because a native pass-through is never reread.
		if query.Count > 0 && len(parsed.Results) > query.Count {
			parsed.Results = parsed.Results[:query.Count]
		}
		body, err := renderer.RenderSearch(parsed)
		if err != nil {
			return answer, err
		}
		answer.Body = body
		return answer, nil
	}

	page, err := parser.ParseFetch(upstream.Body)
	if err != nil {
		return answer, err
	}
	parsed := abstract.Fetch{
		Page:       page,
		StatusCode: upstream.StatusCode,
		Header:     upstream.Header,
	}
	body, err := renderer.RenderFetch(parsed)
	if err != nil {
		return answer, err
	}
	answer.Body = body
	return answer, nil
}

func resolvesCitations(provider string) bool {
	return provider == websearch_setting.ProviderAntigravity || provider == "gemini" || provider == "vertex"
}
