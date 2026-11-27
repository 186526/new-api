package response

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

func init() {
	RegisterRenderer(toolsFetchRenderer{})
}

// toolsFetchRenderer writes the /v1/tools/fetch document.
type toolsFetchRenderer struct{}

func (toolsFetchRenderer) Contract() abstract.Contract {
	return abstract.ContractToolsFetch
}

func (toolsFetchRenderer) ContentType() string {
	return "application/json"
}

// toolsFetchPage carries the two members this contract defines. Both are always
// written, because an empty title or body is a value the client shows rather
// than an unknown member.
type toolsFetchPage struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (renderer toolsFetchRenderer) RenderSearch(abstract.Search) ([]byte, error) {
	return nil, wrongAnswerHalf(renderer.Contract(), "search")
}

func (renderer toolsFetchRenderer) RenderFetch(answer abstract.Fetch) ([]byte, error) {
	if body, ok := forwardNativeAnswer(answer.Raw, answer.RawContract, renderer.Contract()); ok {
		return body, nil
	}
	return json.Marshal(toolsFetchPage{
		Title:   answer.Page.Title,
		Content: answer.Page.Content,
	})
}
