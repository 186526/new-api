package response

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

func init() {
	RegisterRenderer(toolsSearchRenderer{})
}

// toolsSearchRenderer writes the /v1/tools/search document.
type toolsSearchRenderer struct{}

func (toolsSearchRenderer) Contract() abstract.Contract {
	return abstract.ContractToolsSearch
}

func (toolsSearchRenderer) ContentType() string {
	return "application/json"
}

// toolsSearchResult fixes the member order the client reads. A struct keeps
// that order, which a map would not: encoding/json sorts map keys, so the output
// would no longer match the upstream's own layout. The pointer members are null
// when the provider published nothing, because the client tells an unknown
// member from an empty string.
type toolsSearchResult struct {
	RerankScore *float64 `json:"rerank_score"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet"`
	Summary     *string  `json:"summary"`
	Content     *string  `json:"content"`
	SiteName    *string  `json:"site_name"`
	SiteIcon    *string  `json:"site_icon"`
	Date        *string  `json:"date"`
	Authority   int      `json:"authority"`
}

func (renderer toolsSearchRenderer) RenderSearch(answer abstract.Search) ([]byte, error) {
	if body, ok := forwardNativeAnswer(answer.Raw, answer.RawContract, renderer.Contract()); ok {
		return body, nil
	}

	results := make([]toolsSearchResult, 0, len(answer.Results))
	for _, result := range answer.Results {
		rendered := toolsSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Snippet,
		}
		if result.Known.Summary {
			rendered.Summary = stringPointer(result.Summary)
		}
		if result.Known.Content {
			rendered.Content = stringPointer(result.Content)
		}
		if result.Known.SiteName {
			rendered.SiteName = stringPointer(result.SiteName)
		}
		if result.Known.SiteIcon {
			rendered.SiteIcon = stringPointer(result.SiteIcon)
		}
		if result.Known.Date {
			rendered.Date = stringPointer(result.Date)
		}
		if result.Known.RerankScore {
			score := result.RerankScore
			rendered.RerankScore = &score
		}
		// Authority is always written; a provider that names none leaves the
		// abstraction at -1, which the client reads as unranked.
		if result.Known.Authority {
			rendered.Authority = result.Authority
		} else {
			rendered.Authority = -1
		}
		results = append(results, rendered)
	}

	return json.Marshal(struct {
		Results []toolsSearchResult `json:"results"`
	}{Results: results})
}

func (renderer toolsSearchRenderer) RenderFetch(abstract.Fetch) ([]byte, error) {
	return nil, wrongAnswerHalf(renderer.Contract(), "fetch")
}

// stringPointer lets a present-but-empty member stay distinct from an absent
// one: an empty string is a value, and only a missing pointer becomes null.
func stringPointer(value string) *string {
	return &value
}
