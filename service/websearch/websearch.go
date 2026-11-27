package websearch

import (
	"context"
	"net/http"
	"net/url"
	"strings"

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
	// Cached reports that this answer came from the cache rather than from an
	// upstream call, so a caller can count the hit in its metrics.
	Cached bool
}

var (
	ErrUnsupportedChannel = abstract.ErrUnsupportedChannel
	ErrEmptyResults       = abstract.ErrEmptyResults
	ErrEmptyContent       = abstract.ErrEmptyContent
)

// Search answers a search contract. It walks the candidates in the order the
// abstract layer ranks them and stops at the first one whose answer it can use.
func Search(ctx context.Context, channel abstract.ChannelContext, query request.Query) (Answer, error) {
	return answer(ctx, channel, query, true)
}

// Fetch answers a fetch contract with the same candidate walk as Search.
func Fetch(ctx context.Context, channel abstract.ChannelContext, query request.Query) (Answer, error) {
	return answer(ctx, channel, query, false)
}

// Candidates exposes the ordered provider list, so a caller can log what it is
// about to try without depending on the abstract package directly.
func Candidates(channel abstract.ChannelContext, contract abstract.Contract) ([]abstract.Candidate, error) {
	return abstract.Candidates(channel, contract)
}

// LogCandidates writes the candidate order at debug level, which is the only
// record of why one provider was tried before another.
func LogCandidates(ctx context.Context, channel abstract.ChannelContext, contract abstract.Contract) {
	candidates, err := abstract.Candidates(channel, contract)
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

func answer(ctx context.Context, channel abstract.ChannelContext, query request.Query, search bool) (Answer, error) {
	candidates, err := abstract.Candidates(channel, query.Contract)
	if err != nil {
		return Answer{}, err
	}
	renderer := response.For(query.Contract)
	if renderer == nil {
		return Answer{}, ErrUnsupportedChannel
	}

	var lastErr error
	for _, candidate := range candidates {
		requester, err := request.For(candidate.Provider)
		if err != nil {
			if request.WasSent(err) {
				return Answer{}, err
			}
			lastErr = err
			continue
		}

		upstream, err := send(ctx, requester, channel, query, search)
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

		return render(ctx, upstream, renderer, channel, candidate, query, search)
	}
	if lastErr == nil {
		lastErr = ErrUnsupportedChannel
	}
	return Answer{}, lastErr
}

func send(ctx context.Context, requester request.Requester, channel abstract.ChannelContext, query request.Query, search bool) (request.Upstream, error) {
	if search {
		return requester.Search(ctx, channel, query)
	}
	return requester.Fetch(ctx, channel, query)
}

// render turns one upstream answer into the client's contract. A native
// candidate's body is forwarded as it arrived, which is what keeps an existing
// channel's answer byte for byte what it was.
func render(ctx context.Context, upstream request.Upstream, renderer response.Renderer, channel abstract.ChannelContext, candidate abstract.Candidate, query request.Query, search bool) (Answer, error) {
	answer := Answer{
		ContentType: renderer.ContentType(),
		StatusCode:  upstream.StatusCode,
		Header:      upstream.Header,
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
		return Answer{}, err
	}

	if search {
		results, err := parser.ParseSearch(upstream.Body)
		if err != nil {
			return Answer{}, err
		}
		parsed := abstract.Search{
			Results:    results,
			StatusCode: upstream.StatusCode,
			Header:     upstream.Header,
		}
		if resolvesCitations(upstream.Family) {
			// A rendered Antigravity answer cites pages through a redirect
			// service, so its links are resolved before the client reads them.
			request.ResolveSearchRedirects(ctx, channel, &parsed)
		}
		// Every result gets a snippet: the page's own lead paragraph when it can
		// be read inside the budget, the model's own sentence otherwise.
		fillSnippets(ctx, channel, parsed.Results)
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
			return Answer{}, err
		}
		answer.Body = body
		return answer, nil
	}

	page, err := parser.ParseFetch(upstream.Body)
	if err != nil {
		return Answer{}, err
	}
	parsed := abstract.Fetch{
		Page:       page,
		StatusCode: upstream.StatusCode,
		Header:     upstream.Header,
	}
	body, err := renderer.RenderFetch(parsed)
	if err != nil {
		return Answer{}, err
	}
	answer.Body = body
	return answer, nil
}

// resolvesCitations reports whether a provider cites pages through a redirect
// service, which only Antigravity does.
func resolvesCitations(family string) bool {
	return family == websearch_setting.ProviderAntigravity
}
