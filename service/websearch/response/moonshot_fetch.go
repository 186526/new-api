package response

import (
	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

func init() {
	RegisterRenderer(moonshotFetchRenderer{})
}

// moonshotFetchRenderer answers /moonshot.service/v1/fetch with the page body
// itself, not a JSON document, because the client reads the bytes as markdown.
type moonshotFetchRenderer struct{}

func (moonshotFetchRenderer) Contract() abstract.Contract {
	return abstract.ContractMoonshotFetch
}

func (moonshotFetchRenderer) ContentType() string {
	return "text/markdown; charset=utf-8"
}

func (renderer moonshotFetchRenderer) RenderSearch(abstract.Search) ([]byte, error) {
	return nil, wrongAnswerHalf(renderer.Contract(), "search")
}

func (renderer moonshotFetchRenderer) RenderFetch(answer abstract.Fetch) ([]byte, error) {
	if body, ok := forwardNativeAnswer(answer.Raw, answer.RawContract, renderer.Contract()); ok {
		return body, nil
	}
	return []byte(answer.Page.Content), nil
}
