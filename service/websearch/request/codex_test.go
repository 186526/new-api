package request

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/stretchr/testify/require"
)

const codexAlphaSearchPath = "/backend-api/codex/alpha/search"

func TestCodexRebuildsMoonshotSearch(t *testing.T) {
	const answer = `{"results":[{"type":"text_result","title":"A","url":"https://a.example"}]}`
	server, seen := newStubUpstream(t, http.StatusOK, answer)
	channel := testChannel("")
	channel.ChannelType = constant.ChannelTypeCodex
	req := Query{
		Contract: abstract.ContractMoonshotSearch,
		Text:     `birds "song"`,
		Model:    "gpt-5.1",
		Count:    3,
		Endpoint: server.URL + codexAlphaSearchPath,
		Prepare: func(req *http.Request) error {
			req.Header.Set("Authorization", "Bearer adapter-key")
			return nil
		},
	}
	requester, err := For("codex")
	require.NoError(t, err)
	upstream, err := requester.Search(t.Context(), channel, req)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, seen.Method)
	require.Equal(t, codexAlphaSearchPath, seen.Path)
	require.Equal(t, "application/json", seen.Headers.Get("Content-Type"))
	require.Equal(t, "Bearer adapter-key", seen.Headers.Get("Authorization"))
	require.JSONEq(t, `{"model":"gpt-5.1","commands":{"search_query":[{"q":"birds \"song\""}]}}`, string(seen.Body))
	require.Equal(t, "codex", upstream.Family)
	require.Equal(t, http.StatusOK, upstream.StatusCode)
	require.Equal(t, answer, string(upstream.Body))
}

func TestCodexRebuiltSearchIsNotSentWithoutChannelRouting(t *testing.T) {
	server, seen := newStubUpstream(t, http.StatusOK, `{"results":[]}`)
	requester, err := For("codex")
	require.NoError(t, err)

	for _, tt := range []struct {
		name   string
		change func(*abstract.ChannelContext, *Query)
	}{
		{"different channel type", func(channel *abstract.ChannelContext, _ *Query) {
			channel.ChannelType = constant.ChannelTypeDeepSeek
		}},
		{"missing endpoint", func(_ *abstract.ChannelContext, query *Query) { query.Endpoint = "" }},
		{"missing prepare", func(_ *abstract.ChannelContext, query *Query) { query.Prepare = nil }},
		{"failed prepare", func(_ *abstract.ChannelContext, query *Query) {
			query.Prepare = func(*http.Request) error { return errors.New("test signing error") }
		}},
		{"missing model", func(_ *abstract.ChannelContext, query *Query) { query.Model = "" }},
		{"routing capability", func(_ *abstract.ChannelContext, query *Query) {
			query.Model = constant.ToolsSearchModel
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			channel := testChannel(server.URL)
			channel.ChannelType = constant.ChannelTypeCodex
			query := Query{
				Contract: abstract.ContractToolsSearch,
				Text:     "birds",
				Raw:      []byte(`{"query":"birds"}`),
				Model:    "gpt-5.1",
				Endpoint: server.URL + codexAlphaSearchPath,
				Prepare:  func(*http.Request) error { return nil },
			}
			tt.change(&channel, &query)
			_, err := requester.Search(t.Context(), channel, query)
			require.Error(t, err)
			require.False(t, WasSent(err))
			require.Empty(t, seen.Method)
		})
	}
}

func TestCodexRebuiltSearchCacheUsesModelAndQuery(t *testing.T) {
	server, seen := newStubUpstream(t, http.StatusOK, `{"results":[]}`)
	channel := testChannel("")
	channel.ChannelType = constant.ChannelTypeCodex
	channel.Settings.WebSearchCacheTTLSeconds = 60
	requester, err := For("codex")
	require.NoError(t, err)

	const originalPayload = `{"model":"gpt-5.1","commands":{"search_query":[{"q":"birds"}]}}`
	for _, tt := range []struct {
		name         string
		contract     abstract.Contract
		model, text  string
		expectedBody string
		cached       bool
	}{
		{"original", abstract.ContractMoonshotSearch, "gpt-5.1", "birds", originalPayload, false},
		{"repeated", abstract.ContractMoonshotSearch, "gpt-5.1", "birds", originalPayload, true},
		{"different model", abstract.ContractMoonshotSearch, "gpt-5.2", "birds", `{"model":"gpt-5.2","commands":{"search_query":[{"q":"birds"}]}}`, false},
		{"different text", abstract.ContractToolsSearch, "gpt-5.1", "foxes", `{"model":"gpt-5.1","commands":{"search_query":[{"q":"foxes"}]}}`, false},
		{"native body", abstract.ContractAlphaSearch, "gpt-5.2", "foxes", originalPayload, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream, err := requester.Search(t.Context(), channel, Query{
				Contract: tt.contract,
				Model:    tt.model,
				Text:     tt.text,
				Raw:      []byte(originalPayload),
				Endpoint: server.URL + codexAlphaSearchPath,
				Prepare:  func(*http.Request) error { return nil },
			})
			require.NoError(t, err)
			require.Equal(t, tt.cached, upstream.Cached)
			require.Equal(t, tt.expectedBody, string(seen.Body))
		})
	}
}
