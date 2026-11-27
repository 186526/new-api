package abstract

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// codexParser reads the Codex alpha search document. The document carries only
// a title, a URL and a snippet per hit, so every field of KnownFields stays
// false: that struct says which of the richer fields the upstream published,
// and Codex publishes none of them.
type codexParser struct{}

func init() {
	RegisterParser(websearch_setting.ProviderCodex, codexParser{})
}

type codexSearchBody struct {
	ID      string           `json:"id"`
	Results []codexSearchHit `json:"results"`
}

type codexSearchHit struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func (codexParser) ParseSearch(body []byte) ([]Result, error) {
	var payload codexSearchBody
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("websearch: decode codex search body: %w", err)
	}

	results := make([]Result, 0, len(payload.Results))
	for _, hit := range payload.Results {
		if strings.TrimSpace(hit.URL) == "" {
			continue
		}
		results = append(results, Result{
			Title:   hit.Title,
			URL:     hit.URL,
			Snippet: hit.Snippet,
			// The document names no per-page score, so the field keeps its
			// "unknown" value.
			Authority: -1,
		})
	}
	if len(results) == 0 {
		return nil, ErrEmptyResults
	}
	return results, nil
}

// ParseFetch refuses the call because Codex publishes a search document only;
// no fetch contract exists on that upstream.
func (codexParser) ParseFetch([]byte) (Page, error) {
	return Page{}, ErrUnsupportedContentType
}
