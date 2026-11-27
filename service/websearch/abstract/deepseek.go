package abstract

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// deepSeekParser reads the DeepSeek tools documents. DeepSeek is the only
// upstream here that publishes a null member apart from an absent one, so its
// shape is read through pointers: a null and a missing member both decode to a
// nil pointer, and Known then says the document carried neither.
type deepSeekParser struct{}

func init() {
	RegisterParser(websearch_setting.ProviderDeepSeek, deepSeekParser{})
}

type deepSeekSearchBody struct {
	Results []deepSeekHit `json:"results"`
}

type deepSeekHit struct {
	RerankScore *float64 `json:"rerank_score"`
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Snippet     string   `json:"snippet"`
	Summary     *string  `json:"summary"`
	Content     *string  `json:"content"`
	SiteName    *string  `json:"site_name"`
	SiteIcon    *string  `json:"site_icon"`
	Date        *string  `json:"date"`
	Authority   *int     `json:"authority"`
}

type deepSeekFetchBody struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func (deepSeekParser) ParseSearch(body []byte) ([]Result, error) {
	var payload deepSeekSearchBody
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("websearch: decode deepseek search body: %w", err)
	}

	results := make([]Result, 0, len(payload.Results))
	seen := make(map[string]struct{}, len(payload.Results))
	for _, hit := range payload.Results {
		// A hit without a URL cannot be fetched or cited, so the caller can do
		// nothing with it.
		if strings.TrimSpace(hit.URL) == "" {
			continue
		}
		if _, duplicated := seen[hit.URL]; duplicated {
			continue
		}
		seen[hit.URL] = struct{}{}

		results = append(results, Result{
			Title:       hit.Title,
			URL:         hit.URL,
			Snippet:     hit.Snippet,
			Summary:     stringOrEmpty(hit.Summary),
			Content:     stringOrEmpty(hit.Content),
			SiteName:    stringOrEmpty(hit.SiteName),
			SiteIcon:    stringOrEmpty(hit.SiteIcon),
			Date:        stringOrEmpty(hit.Date),
			Authority:   intOrMinusOne(hit.Authority),
			RerankScore: floatOrZero(hit.RerankScore),
			Known: KnownFields{
				Summary:     hit.Summary != nil,
				Content:     hit.Content != nil,
				SiteName:    hit.SiteName != nil,
				SiteIcon:    hit.SiteIcon != nil,
				Date:        hit.Date != nil,
				Authority:   hit.Authority != nil,
				RerankScore: hit.RerankScore != nil,
			},
		})
	}
	if len(results) == 0 {
		return nil, ErrEmptyResults
	}
	return results, nil
}

func (deepSeekParser) ParseFetch(body []byte) (Page, error) {
	var payload deepSeekFetchBody
	if err := common.Unmarshal(body, &payload); err != nil {
		return Page{}, fmt.Errorf("websearch: decode deepseek fetch body: %w", err)
	}
	// The upstream bills a fetch that produced no text as a failed one, so an
	// empty body is an error rather than a page with an empty Content.
	if strings.TrimSpace(payload.Content) == "" {
		return Page{}, ErrEmptyContent
	}
	return Page{Title: payload.Title, Content: payload.Content}, nil
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// intOrMinusOne keeps an unknown authority as -1, the value the caller reads
// as "the provider names none".
func intOrMinusOne(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}

func floatOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
