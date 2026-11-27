package abstract

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

const (
	antigravitySnippetMaxRune = 300
	antigravityTitleMaxRune   = 120
)

// antigravityParser reads the Gemini answer an Antigravity search produces.
// The grounding metadata is the citation list; the model's own answer is not a
// result, because reporting it as one would tell the caller a page exists that
// nothing cited.
type antigravityParser struct{}

func init() {
	RegisterParser(websearch_setting.ProviderAntigravity, antigravityParser{})
}

type antigravitySearchBody struct {
	Candidates []struct {
		GroundingMetadata *antigravityGroundingMetadata `json:"groundingMetadata"`
	} `json:"candidates"`
}

type antigravityGroundingMetadata struct {
	GroundingChunks []struct {
		Web *struct {
			URI   string `json:"uri"`
			Title string `json:"title"`
		} `json:"web"`
	} `json:"groundingChunks"`
	GroundingSupports []struct {
		Segment *struct {
			Text string `json:"text"`
		} `json:"segment"`
		GroundingChunkIndices []int `json:"groundingChunkIndices"`
	} `json:"groundingSupports"`
}

func (antigravityParser) ParseSearch(body []byte) ([]Result, error) {
	var payload antigravitySearchBody
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("websearch: decode antigravity search body: %w", err)
	}

	var metadata *antigravityGroundingMetadata
	for _, candidate := range payload.Candidates {
		if candidate.GroundingMetadata != nil {
			metadata = candidate.GroundingMetadata
			break
		}
	}
	if metadata == nil {
		return nil, errors.New("websearch: antigravity answered without groundingMetadata")
	}

	// A segment cites one or more chunks, so its text has to be gathered per
	// chunk before the per-chunk snippet can be joined.
	snippets := make(map[int][]string, len(metadata.GroundingChunks))
	for _, support := range metadata.GroundingSupports {
		if support.Segment == nil {
			continue
		}
		text := strings.TrimSpace(support.Segment.Text)
		if text == "" {
			continue
		}
		for _, index := range support.GroundingChunkIndices {
			if index < 0 || index >= len(metadata.GroundingChunks) {
				continue
			}
			snippets[index] = append(snippets[index], text)
		}
	}

	results := make([]Result, 0, len(metadata.GroundingChunks))
	seen := make(map[string]struct{}, len(metadata.GroundingChunks))
	for index, chunk := range metadata.GroundingChunks {
		if chunk.Web == nil {
			continue
		}
		target := strings.TrimSpace(chunk.Web.URI)
		if target == "" {
			continue
		}
		if _, duplicated := seen[target]; duplicated {
			continue
		}
		seen[target] = struct{}{}

		siteName := strings.TrimSpace(chunk.Web.Title)
		snippet := clipRunes(strings.Join(snippets[index], " "), antigravitySnippetMaxRune)
		results = append(results, Result{
			Title:    resultTitle(snippet, siteName),
			URL:      target,
			Snippet:  snippet,
			SiteName: siteName,
			// The upstream names no per-page score, so the field keeps its
			// "unknown" value.
			Authority: -1,
			Known: KnownFields{
				SiteName: true,
				// Snippet has no flag in KnownFields, so the citation text is
				// published without one. The grounding document carries no
				// summary, body or score of its own, so those stay false.
			},
		})
	}
	if len(results) == 0 {
		return nil, ErrEmptyResults
	}
	return results, nil
}

// ParseFetch refuses the call because Antigravity publishes no fetch endpoint;
// its channel fetches through the in-process extractor instead.
func (antigravityParser) ParseFetch([]byte) (Page, error) {
	return Page{}, ErrUnsupportedContentType
}

// resultTitle uses the sentence the page was cited for as the title, because
// the citation carries no page title; the site name is the fallback.
func resultTitle(snippet, siteName string) string {
	if snippet == "" {
		return siteName
	}
	if stop := strings.IndexAny(snippet, ".!?。！？"); stop > 0 && stop < antigravityTitleMaxRune {
		return strings.TrimSpace(snippet[:stop])
	}
	return clipRunes(snippet, antigravityTitleMaxRune)
}

func clipRunes(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:limit]))
}
