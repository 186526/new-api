package websearch

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
)

func groundedSearch(query request.Query) hostedCall {
	payload := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []map[string]string{{"text": encodeSearchInput(query.Text)}}}},
		"tools":    []any{map[string]any{"googleSearch": map[string]any{}}},
	}
	return hostedCall{payload, func(raw []byte) (hostedBody, error) {
		var response struct {
			Error          json.RawMessage
			PromptFeedback struct{ BlockReason string }
			Candidates     []struct {
				FinishReason      string
				GroundingMetadata struct {
					WebSearchQueries []string
					GroundingChunks  []struct{ Web struct{ URI, Title string } }
				}
			}
		}
		if json.Unmarshal(raw, &response) != nil || refused(response.Error) || response.PromptFeedback.BlockReason != "" {
			return hostedBody{}, errors.New("websearch: Google grounding request failed")
		}
		seen := map[string]bool{}
		var hits []abstract.Result
		for _, candidate := range response.Candidates {
			grounding := candidate.GroundingMetadata
			if len(grounding.WebSearchQueries) == 0 {
				continue
			}
			for _, chunk := range grounding.GroundingChunks {
				// Google's title can be only a domain; leave it absent in that case.
				title := chunk.Web.Title
				if target := parsedWebURL(chunk.Web.URI); target != nil && (strings.EqualFold(title, target.Hostname()) || strings.EqualFold(title, strings.TrimPrefix(target.Hostname(), "www."))) {
					title = ""
				}
				hits = addHostedHit(hits, seen, chunk.Web.URI, title)
			}
		}
		return hostedBody{Hits: hits}, nil
	}}
}

func responsesSearch(provider string) func(request.Query) hostedCall {
	return func(query request.Query) hostedCall {
		payload := map[string]any{"model": query.Model, "input": encodeSearchInput(query.Text), "tools": []map[string]string{{"type": "web_search"}}}
		if provider == "openai" || provider == "azure" {
			payload["include"] = []string{"web_search_call.action.sources"}
			payload["tool_choice"] = "required"
		}
		return hostedCall{payload, func(raw []byte) (hostedBody, error) { return parseResponsesSearch(raw, provider) }}
	}
}

type responsesSource struct{ Type, URL, Title string }

func parseResponsesSearch(raw []byte, provider string) (hostedBody, error) {
	var response struct {
		Status string
		Error  json.RawMessage
		Usage  struct {
			ServerTools struct {
				SearchCalls int `json:"web_search_calls"`
			} `json:"server_side_tool_usage_details"`
		}
		Output []struct {
			Type, Status string
			Action       struct {
				Type    string
				Sources []responsesSource
			}
			Content []struct {
				Type        string
				Annotations []responsesSource
			}
		}
	}
	if json.Unmarshal(raw, &response) != nil || refused(response.Error) || response.Output == nil || (response.Status != "" && response.Status != "completed") {
		return hostedBody{}, errors.New("websearch: invalid or incomplete Responses search")
	}
	var sources, citations []responsesSource
	searched := provider == "xai" && response.Usage.ServerTools.SearchCalls > 0
	for _, item := range response.Output {
		switch item.Type {
		case "web_search_call":
			if item.Status != "" && item.Status != "completed" {
				return hostedBody{}, errors.New("websearch: Responses search tool failed")
			}
			if item.Action.Type == "search" || (item.Action.Type == "" && len(item.Action.Sources) > 0) {
				searched = true
				sources = append(sources, item.Action.Sources...)
			}
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" {
					for _, citation := range content.Annotations {
						if citation.Type == "url_citation" {
							citations = append(citations, citation)
						}
					}
				}
			}
		}
	}
	if !searched {
		return hostedBody{}, nil
	}
	titles := map[string]string{}
	if provider != "xai" {
		for _, citation := range citations {
			titles[citation.URL] = citation.Title
		}
	}
	seen := map[string]bool{}
	var hits []abstract.Result
	for _, source := range sources {
		hits = addHostedHit(hits, seen, source.URL, titles[source.URL])
	}
	if provider != "ali" {
		for _, citation := range citations {
			hits = addHostedHit(hits, seen, citation.URL, titles[citation.URL])
		}
	}
	return hostedBody{Hits: hits}, nil
}
