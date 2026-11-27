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
// snippet and links the URL. Date and site name are omitted when empty, the same
// tags relay/moonshot_service_handler.go writes.
type moonshotSearchResult struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Snippet  string `json:"snippet"`
	Date     string `json:"date,omitempty"`
	SiteName string `json:"site_name,omitempty"`
}

func (renderer moonshotSearchRenderer) RenderSearch(answer abstract.Search) ([]byte, error) {
	if body, ok := forwardNativeAnswer(answer.Raw, answer.RawContract, renderer.Contract()); ok {
		return body, nil
	}

	// An empty list must serialize as [], because the client reads null as a
	// malformed answer rather than as "no hit".
	results := make([]moonshotSearchResult, 0, len(answer.Results))
	for _, result := range answer.Results {
		results = append(results, moonshotSearchResult{
			Title:    result.Title,
			URL:      result.URL,
			Snippet:  result.Snippet,
			Date:     result.Date,
			SiteName: result.SiteName,
		})
	}

	return json.Marshal(struct {
		SearchResults []moonshotSearchResult `json:"search_results"`
	}{SearchResults: results})
}

func (renderer moonshotSearchRenderer) RenderFetch(abstract.Fetch) ([]byte, error) {
	return nil, wrongAnswerHalf(renderer.Contract(), "fetch")
}
