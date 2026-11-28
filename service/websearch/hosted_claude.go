package websearch

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
)

func claudePayload(model, prompt string, tool map[string]any) map[string]any {
	return map[string]any{"model": model, "max_tokens": 512, "messages": []map[string]string{{"role": "user", "content": prompt}}, "tools": []map[string]any{tool}}
}

type claudeBlock struct {
	Type, ID, Name string
	ToolUseID      string `json:"tool_use_id"`
	Input          struct{ URL string }
	Content        json.RawMessage
}

func claudeToolResults(raw []byte, tool string, accepts func(claudeBlock) bool) ([]json.RawMessage, error) {
	var response struct {
		Content []claudeBlock
		Error   json.RawMessage
	}
	if json.Unmarshal(raw, &response) != nil || refused(response.Error) || response.Content == nil {
		return nil, errors.New("websearch: invalid Claude Messages response")
	}
	calls := map[string]bool{}
	var results []json.RawMessage
	for _, block := range response.Content {
		if block.Type == "server_tool_use" && block.ID != "" && block.Name == tool && accepts(block) {
			calls[block.ID] = true
		}
		if block.Type == tool+"_tool_result" && calls[block.ToolUseID] {
			results = append(results, block.Content)
		}
	}
	return results, nil
}

func claudeSearch(query request.Query) hostedCall {
	payload := claudePayload(query.Model, encodeSearchInput(query.Text), map[string]any{"type": "web_search_20250305", "name": "web_search", "max_uses": 1})
	return hostedCall{payload, func(raw []byte) (hostedBody, error) {
		results, err := claudeToolResults(raw, "web_search", func(claudeBlock) bool { return true })
		if err != nil {
			return hostedBody{}, err
		}
		seen := map[string]bool{}
		var hits []abstract.Result
		for _, block := range results {
			var rows []struct{ Type, URL, Title string }
			if json.Unmarshal(block, &rows) != nil || rows == nil {
				return hostedBody{}, errors.New("websearch: Claude search tool failed")
			}
			for _, row := range rows {
				if row.Type == "web_search_result" {
					hits = addHostedHit(hits, seen, row.URL, row.Title)
				}
			}
		}
		return hostedBody{Hits: hits}, nil
	}}
}

func claudeFetch(query request.Query) hostedCall {
	target := parsedWebURL(query.URL)
	payload := claudePayload(query.Model, "Fetch exactly this web page using the web fetch tool. Do not summarize it. URL: "+query.URL, map[string]any{
		"type": "web_fetch_20250910", "name": "web_fetch", "max_uses": 1, "max_content_tokens": 20000, "allowed_domains": []string{target.Hostname()},
	})
	return hostedCall{payload, func(raw []byte) (hostedBody, error) {
		results, err := claudeToolResults(raw, "web_fetch", func(call claudeBlock) bool { return sameWebTarget(query.URL, call.Input.URL) })
		if err != nil {
			return hostedBody{}, err
		}
		for _, block := range results {
			var result struct {
				Type, URL string
				Content   struct {
					Type, Title string
					Source      struct {
						Type, Data string
						MediaType  string `json:"media_type"`
					}
				}
			}
			if json.Unmarshal(block, &result) != nil || result.Type != "web_fetch_result" {
				return hostedBody{}, errors.New("websearch: Claude fetch tool failed")
			}
			document := result.Content
			if sameWebTarget(query.URL, result.URL) && document.Type == "document" && document.Source.Type == "text" && document.Source.MediaType == "text/plain" && strings.TrimSpace(document.Source.Data) != "" {
				return hostedBody{Page: abstract.Page{Title: document.Title, Content: document.Source.Data}}, nil
			}
		}
		return hostedBody{}, nil
	}}
}
