package response

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

func init() {
	RegisterRenderer(alphaSearchRenderer{})
}

// alphaSearchRenderer writes the /v1/alpha/search document, the Codex search
// shape.
type alphaSearchRenderer struct{}

func (alphaSearchRenderer) Contract() abstract.Contract {
	return abstract.ContractAlphaSearch
}

func (alphaSearchRenderer) ContentType() string {
	return "application/json"
}

// alphaSearchResult fixes the member order and the type tag the client reads.
type alphaSearchResult struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (renderer alphaSearchRenderer) RenderSearch(answer abstract.Search) ([]byte, error) {
	if body, ok := forwardNativeAnswer(answer.Raw, answer.RawContract, renderer.Contract()); ok {
		return body, nil
	}

	results := make([]alphaSearchResult, 0, len(answer.Results))
	for _, result := range answer.Results {
		results = append(results, alphaSearchResult{
			// This contract has a single result kind, so the tag is a constant.
			Type:    "text_result",
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Snippet,
		})
	}

	// The client sends no id and the upstream's id is not part of the abstract
	// answer, so the rendered document always carries an empty one.
	return json.Marshal(struct {
		ID      string              `json:"id"`
		Results []alphaSearchResult `json:"results"`
	}{ID: "", Results: results})
}

func (renderer alphaSearchRenderer) RenderFetch(abstract.Fetch) ([]byte, error) {
	return nil, wrongAnswerHalf(renderer.Contract(), "fetch")
}
