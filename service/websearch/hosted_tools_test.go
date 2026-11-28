package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/stretchr/testify/require"
)

func TestHostedToolsReplay(t *testing.T) {
	const target = "https://example.com/doc"
	const responseSources = `{"status":"completed","output":[{"type":"web_search_call","status":"completed","action":{"type":"search","sources":[{"url":"https://example.com/doc"}]}},{"type":"message","content":[{"type":"output_text","annotations":[{"type":"url_citation","title":"Page title","url":"https://example.com/doc"}]}]}]}`
	const responseOnlyAnnotation = `{"output":[{"type":"message","content":[{"type":"output_text","annotations":[{"type":"url_citation","url":"https://example.com/doc"}]}]}]}`
	const grounding = `{"candidates":[{"groundingMetadata":{"webSearchQueries":["query"],"groundingChunks":[{"web":{"uri":"https://example.com/doc","title":"Page title"}}]}}]}`
	const claudeFetchResult = `{"content":[{"type":"server_tool_use","name":"web_fetch","id":"tool","input":{"url":"https://example.com/doc"}},{"type":"web_fetch_tool_result","tool_use_id":"tool","content":{"type":"web_fetch_result","url":"https://example.com/doc","content":{"type":"document","title":"Page title","source":{"type":"text","media_type":"text/plain","data":"Page body"}}}}]}`
	const claudeFetchFailure = `{"content":[{"type":"server_tool_use","name":"web_fetch","id":"tool","input":{"url":"https://example.com/doc"}},{"type":"web_fetch_tool_result","tool_use_id":"tool","content":{"type":"web_fetch_tool_result_error","error_code":"url_not_accessible"}}]}`
	const claudeSearchResult = `{"content":[{"type":"server_tool_use","name":"web_search","id":"tool"},{"type":"web_search_tool_result","tool_use_id":"tool","content":[{"type":"web_search_result","url":"https://example.com/doc","title":"Page title"}]}]}`
	for _, fixture := range []struct {
		name, provider, target, body, title string
		failure                             bool
	}{
		{name: "zhipu reader", provider: "zhipu", target: target, body: `{"reader_result":{"url":"https://example.com/doc","content":"Page body"}}`},
		{name: "zhipu rejects unrelated page", provider: "zhipu", target: target, body: `{"reader_result":{"url":"https://example.com/wrong","content":"Page body"}}`, failure: true},
		{name: "claude fetch", provider: "claude", target: target, body: claudeFetchResult},
		{name: "claude fetch refusal", provider: "claude", target: target, body: claudeFetchFailure, failure: true},
		{name: "claude search", provider: "claude", body: claudeSearchResult, title: "Page title"},
		{name: "gemini", provider: "gemini", body: grounding, title: "Page title"},
		{name: "vertex", provider: "vertex", body: grounding, title: "Page title"},
		{name: "openai", provider: "openai", body: responseSources, title: "Page title"},
		{name: "azure", provider: "azure", body: responseSources, title: "Page title"},
		{name: "ali", provider: "ali", body: responseSources, title: "Page title"},
		{name: "xai", provider: "xai", body: responseSources},
		{name: "unsupported answer-only citations", provider: "openai", body: responseOnlyAnnotation},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// Use an IP literal for the outbound-policy check without public DNS.
			const fixtureOrigin = "https://93.184.215.14"
			body := strings.ReplaceAll(fixture.body, "https://example.com", fixtureOrigin)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" || req.Header.Get("X-Fixture") != "prepared" {
					t.Error("prepared request method or headers lost")
				}
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if fixture.provider == "zhipu" && string(payload["url"]) != `"`+fixtureOrigin+`/doc"` {
					t.Error("missing requested URL")
				}
				_, _ = io.WriteString(writer, body)
			}))
			defer server.Close()
			query := request.Query{Contract: abstract.ContractToolsSearch, Endpoint: server.URL, EndpointProvider: fixture.provider, Model: "fixture-model", Text: "query", Prepare: func(req *http.Request) error { req.Header.Set("X-Fixture", "prepared"); return nil }}
			provider, err := request.For(fixture.provider)
			require.NoError(t, err)
			if fixture.target != "" {
				query.Contract, query.URL = abstract.ContractMoonshotFetch, fixtureOrigin+"/doc"
				answer, err := provider.Fetch(context.Background(), abstract.ChannelContext{}, query)
				if fixture.failure {
					require.Error(t, err)
					require.True(t, request.WasSent(err))
				} else {
					require.NoError(t, err)
					page, err := (hostedParser{}).ParseFetch(answer.Body)
					require.NoError(t, err)
					require.Equal(t, "Page body", page.Content)
				}
			} else {
				answer, err := provider.Search(context.Background(), abstract.ChannelContext{}, query)
				require.NoError(t, err)
				hits, err := (hostedParser{}).ParseSearch(answer.Body)
				if fixture.body == responseOnlyAnnotation {
					require.ErrorIs(t, err, abstract.ErrEmptyResults)
				} else {
					require.NoError(t, err)
					require.Len(t, hits, 1)
					require.Equal(t, fixture.title, hits[0].Title)
					require.Empty(t, hits[0].Snippet)
				}
			}
			require.Equal(t, int32(1), calls.Load())
		})
	}
}
