package response

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

func init() {
	RegisterRenderer(moonshotSearchRenderer{})
}

// moonshotSearchRenderer writes the /moonshot.service/v1/search document the
// Kimi Code client reads.
type moonshotSearchRenderer struct{}

func (moonshotSearchRenderer) Contract() abstract.Contract {
	return abstract.ContractMoonshotSearch
}

func (moonshotSearchRenderer) ContentType() string {
	return "application/json"
}

// moonshotSearchResult matches the field set the client already reads. The
// provider's summary, content and icon are dropped: this client shows the
// snippet and links the URL.
//
// Date carries no omitempty: a result whose page names no date writes null, so a
// caller can tell "no date" from "this build does not know dates". SiteName keeps
// omitempty because an unknown site reads as a missing member.
type moonshotSearchResult struct {
	Title    string  `json:"title"`
	URL      string  `json:"url"`
	Snippet  string  `json:"snippet"`
	Date     *string `json:"date"`
	SiteName string  `json:"site_name,omitempty"`
}

func (renderer moonshotSearchRenderer) RenderSearch(answer abstract.Search) ([]byte, error) {
	if body, ok := forwardNativeAnswer(answer.Raw, answer.RawContract, renderer.Contract()); ok {
		return body, nil
	}

	// An empty list must serialize as [], because the client reads null as a
	// malformed answer rather than as "no hit".
	results := make([]moonshotSearchResult, 0, len(answer.Results))
	for _, result := range answer.Results {
		rendered := moonshotSearchResult{
			Title:    result.Title,
			URL:      result.URL,
			Snippet:  result.Snippet,
			SiteName: result.SiteName,
		}
		if result.Date != "" {
			rendered.Date = stringPointer(result.Date)
		}
		results = append(results, rendered)
	}

	return json.Marshal(struct {
		SearchResults []moonshotSearchResult `json:"search_results"`
	}{SearchResults: results})
}

func (renderer moonshotSearchRenderer) RenderFetch(abstract.Fetch) ([]byte, error) {
	return nil, wrongAnswerHalf(renderer.Contract(), "fetch")
}
