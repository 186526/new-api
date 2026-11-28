package abstract

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// exaParser reads an Exa answer. Exa sends every field spelled out and never
// distinguishes a null member from an empty one, so Known is decided by whether
// the value has anything in it rather than by pointer presence: a renderer can
// only act on "the provider sent something", not on "the provider sent null".
type exaParser struct{}

func init() {
	RegisterParser(websearch_setting.ProviderExa, exaParser{})
}

type exaSearchBody struct {
	Results []exaHit `json:"results"`
}

type exaHit struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate"`
	Favicon       string   `json:"favicon"`
	Text          string   `json:"text"`
	Highlights    []string `json:"highlights"`
	Summary       string   `json:"summary"`
}

type exaFetchBody struct {
	Results []struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	} `json:"results"`
}

func (exaParser) ParseSearch(body []byte) ([]Result, error) {
	var payload exaSearchBody
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("websearch: decode exa search body: %w", err)
	}

	results := make([]Result, 0, len(payload.Results))
	for _, hit := range payload.Results {
		if strings.TrimSpace(hit.URL) == "" {
			continue
		}
		// Highlights are separate passages of the same page, so they read back
		// as one snippet rather than as one result each.
		snippet := strings.Join(hit.Highlights, "\n")
		siteName := siteNameOf(hit.URL)
		results = append(results, Result{
			Title:     hit.Title,
			URL:       hit.URL,
			Snippet:   snippet,
			Summary:   hit.Summary,
			Content:   hit.Text,
			SiteName:  siteName,
			SiteIcon:  hit.Favicon,
			Date:      hit.PublishedDate,
			Authority: -1,
			Known: KnownFields{
				Summary:  hit.Summary != "",
				Content:  hit.Text != "",
				SiteName: siteName != "",
				SiteIcon: hit.Favicon != "",
				Date:     hit.PublishedDate != "",
			},
		})
	}
	if len(results) == 0 {
		return nil, ErrEmptyResults
	}
	return results, nil
}

func (exaParser) ParseFetch(body []byte) (Page, error) {
	if len(body) == 0 {
		return Page{}, ErrEmptyContent
	}
	var payload exaFetchBody
	if err := common.Unmarshal(body, &payload); err != nil {
		return Page{}, fmt.Errorf("websearch: decode exa fetch body: %w", err)
	}
	if len(payload.Results) == 0 {
		return Page{}, ErrEmptyContent
	}
	first := payload.Results[0]
	if strings.TrimSpace(first.Text) == "" {
		return Page{}, ErrEmptyContent
	}
	return Page{Title: first.Title, Content: first.Text}, nil
}

// siteNameOf reads the host Exa's answer does not spell out. A URL the parser
// cannot read keeps an empty site name rather than failing the whole result.
func siteNameOf(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
