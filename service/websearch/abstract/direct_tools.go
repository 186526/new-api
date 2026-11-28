package abstract

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

func init() {
	RegisterParser(websearch_setting.ProviderMoonshot, directToolsParser{moonshot: true})
	RegisterParser(websearch_setting.ProviderPerplexity, directToolsParser{})
}

type directToolsParser struct{ moonshot bool }

type directSearchHit struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Date    string `json:"date"`
	Site    string `json:"site_name"`
	Icon    string `json:"icon"`
	MIME    string `json:"mime"`
	Text    string `json:"text"`
}

func (parser directToolsParser) ParseSearch(body []byte) ([]Result, error) {
	var payload struct {
		Moonshot   []directSearchHit `json:"search_results"`
		Perplexity []directSearchHit `json:"results"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("websearch: decode search results: %w", err)
	}
	hits := payload.Perplexity
	if parser.moonshot {
		hits = payload.Moonshot
	}
	if hits == nil {
		return nil, fmt.Errorf("websearch: missing search results array")
	}
	results := make([]Result, 0, len(hits))
	for _, hit := range hits {
		if strings.TrimSpace(hit.URL) == "" {
			continue
		}
		site := hit.Site
		if site == "" {
			site = siteNameOf(hit.URL)
		}
		result := Result{Title: hit.Title, URL: hit.URL, Snippet: hit.Snippet, Date: hit.Date, SiteName: site, SiteIcon: hit.Icon, Content: hit.Text, Authority: -1}
		result.Known = KnownFields{Date: hit.Date != "", SiteName: site != "", SiteIcon: hit.Icon != "", Content: hit.Text != ""}
		results = append(results, result)
	}
	if len(results) == 0 {
		return nil, ErrEmptyResults
	}
	return results, nil
}

func (parser directToolsParser) ParseFetch(body []byte) (Page, error) {
	if !parser.moonshot {
		return Page{}, ErrUnsupportedChannel
	}
	var payload struct {
		Title    string  `json:"title"`
		Markdown *string `json:"markdown"`
	}
	if err := common.Unmarshal(body, &payload); err != nil {
		return Page{}, fmt.Errorf("websearch: decode Moonshot page: %w", err)
	}
	if payload.Markdown == nil {
		return Page{}, fmt.Errorf("websearch: missing Moonshot markdown")
	}
	if strings.TrimSpace(*payload.Markdown) == "" {
		return Page{}, ErrEmptyContent
	}
	return Page{Title: payload.Title, Content: *payload.Markdown}, nil
}
