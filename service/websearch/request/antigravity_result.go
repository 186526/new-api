package request

import (
	"context"
	"errors"
	"regexp"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

const antigravityResultFamily = "antigravity_normalized"

var antigravityURLText = regexp.MustCompile(`(?i)https?://[^\s<>"'()\[\]]+`)

func init() {
	abstract.RegisterParser(antigravityResultFamily, antigravityResultParser{})
}

type antigravityResultParser struct{}

func (antigravityResultParser) ParseSearch(body []byte) ([]abstract.Result, error) {
	var results []abstract.Result
	if err := common.Unmarshal(body, &results); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, abstract.ErrEmptyResults
	}
	return results, nil
}

func (antigravityResultParser) ParseFetch([]byte) (abstract.Page, error) {
	return abstract.Page{}, abstract.ErrUnsupportedContentType
}

func normalizeAntigravitySearch(ctx context.Context, channel abstract.ChannelContext, entry forwardEntry) (forwardEntry, error) {
	if entry.StatusCode < 200 || entry.StatusCode >= 300 {
		entry.Header.Del("Location")
		entry.Body = []byte(stripAntigravityShortLinks(string(entry.Body)))
		return entry, nil
	}
	body, err := unwrapAntigravityResponse(entry.Body)
	if err != nil {
		return forwardEntry{}, sentError{err}
	}
	parser, err := abstract.ParserFor(websearch_setting.ProviderAntigravity)
	if err != nil {
		return forwardEntry{}, sentError{err}
	}
	results, err := parser.ParseSearch(body)
	if err != nil && !errors.Is(err, abstract.ErrEmptyResults) {
		return forwardEntry{}, sentError{err}
	}
	parsed := abstract.Search{Results: results}
	ResolveSearchRedirects(ctx, channel, &parsed)
	for index := range parsed.Results {
		result := &parsed.Results[index]
		result.Title = stripAntigravityShortLinks(result.Title)
		result.Snippet = stripAntigravityShortLinks(result.Snippet)
		result.Summary = stripAntigravityShortLinks(result.Summary)
		result.SiteName = stripAntigravityShortLinks(result.SiteName)
		if result.URL != "" && result.Snippet != "" {
			// The composition layer only fetches when Snippet is empty.
			if result.Summary == "" {
				result.Summary = result.Snippet
			}
			result.Snippet = ""
		}
	}
	entry.Header = nil
	entry.Body, err = common.Marshal(parsed.Results)
	if err != nil {
		return forwardEntry{}, sentError{err}
	}
	return entry, nil
}

func stripAntigravityShortLinks(text string) string {
	return antigravityURLText.ReplaceAllStringFunc(text, func(target string) string {
		if isGroundingRedirect(target) {
			return ""
		}
		return target
	})
}
