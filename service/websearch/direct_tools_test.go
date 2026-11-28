package websearch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	channeladaptor "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/moonshot"
	"github.com/QuantumNous/new-api/relay/channel/perplexity"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDirectToolsProviderReplay(t *testing.T) {
	const moonshotSearch = `{"search_results":[{"authority":"S","date":"2026-06-01","icon":"https://example.test/favicon.ico","mime":"text/html","site_name":"Example Source","snippet":"A Moonshot snippet.","text":"Full page content.","title":"Moonshot result","url":"https://example.test/moonshot"}]}`
	const perplexitySearch = `{"id":"test-id","results":[{"date":null,"last_updated":"2026-11-19","snippet":"A Perplexity snippet.","title":"Perplexity result","url":"https://example.test/perplexity"}]}`
	const moonshotFetch = `{"url":"https://example.test/page","title":"Page title","markdown":"# Page title\n\nFetched content."}`
	for _, fixture := range []struct {
		name                       string
		channelType                int
		provider                   string
		contract                   abstract.Contract
		path, sent, upstream, want string
	}{
		{"Moonshot tools search", constant.ChannelTypeMoonshot, websearch_setting.ProviderMoonshot, abstract.ContractToolsSearch, "/v1/tools/search", `{"text_query":"sample query","limit":20,"include_content":true}`, moonshotSearch, `"results"`},
		{"Moonshot service search", constant.ChannelTypeMoonshot, websearch_setting.ProviderMoonshot, abstract.ContractMoonshotSearch, "/v1/tools/search", `{"text_query":"sample query","limit":20,"include_content":true}`, moonshotSearch, `"site_name":"Example Source"`},
		{"Moonshot tools fetch", constant.ChannelTypeMoonshot, websearch_setting.ProviderMoonshot, abstract.ContractToolsFetch, "/v1/tools/fetch", `{"url":"https://example.test/page"}`, moonshotFetch, `"content":"# Page title\n\nFetched content."`},
		{"Moonshot service fetch", constant.ChannelTypeMoonshot, websearch_setting.ProviderMoonshot, abstract.ContractMoonshotFetch, "/v1/tools/fetch", `{"url":"https://example.test/page"}`, moonshotFetch, "# Page title\n\nFetched content."},
		{"Perplexity Alpha search", constant.ChannelTypePerplexity, websearch_setting.ProviderPerplexity, abstract.ContractAlphaSearch, "/search", `{"query":"sample query","max_results":20}`, perplexitySearch, `"title":"Perplexity result"`},
		{"Perplexity service search", constant.ChannelTypePerplexity, websearch_setting.ProviderPerplexity, abstract.ContractMoonshotSearch, "/search", `{"query":"sample query","max_results":20}`, perplexitySearch, `"date":null`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				wantKey := "Bearer selected-test-key"
				if calls.Load() > 1 {
					wantKey = "Bearer alternate-test-key"
				}
				if req.Method != http.MethodPost || req.URL.Path != fixture.path || req.Header.Get("Authorization") != wantKey || req.Header.Get("Content-Type") != "application/json" {
					t.Errorf("outbound method/path or channel headers did not match")
				}
				var sent json.RawMessage
				if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
					t.Error(err)
				} else if string(sent) != fixture.sent {
					t.Errorf("outbound body = %s, want %s", sent, fixture.sent)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(fixture.upstream))
			}))
			defer upstream.Close()
			channel := abstract.ChannelContext{ChannelType: fixture.channelType, BaseURL: upstream.URL, Key: "selected-test-key", PreferredProviders: []string{fixture.provider}, OnlyProviders: true}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/client/tools", nil)
			var adaptor channeladaptor.WebSearchAdaptor = &moonshot.Adaptor{}
			if fixture.channelType == constant.ChannelTypePerplexity {
				adaptor = &perplexity.Adaptor{}
			}
			channel.Settings.WebSearchCacheTTLSeconds = 60
			channel.Settings.WebSearchFetchCacheTTLSeconds = 60
			query := request.Query{Contract: fixture.contract, Text: "sample query", URL: "https://example.test/page", Count: 100, NeedContent: true, Raw: []byte(`{"model":"must-not-leak"}`)}
			prepare := func() {
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: channel.ChannelType, ChannelBaseUrl: channel.BaseURL, ApiKey: channel.Key}}
				prepared, err := adaptor.PrepareWebSearch(ctx, info, fixture.contract)
				require.NoError(t, err)
				require.Equal(t, fixture.provider, prepared.Provider)
				query.EndpointProvider, query.Endpoint, query.Prepare = prepared.Provider, prepared.Endpoint, prepared.Prepare
			}
			prepare()
			call := websearch.Fetch
			if fixture.contract.IsSearch() {
				call = websearch.Search
				query.URL = ""
			}
			answer, err := call(context.Background(), channel, query)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, answer.StatusCode)
			require.Contains(t, string(answer.Body), fixture.want)
			if fixture.contract == abstract.ContractMoonshotFetch {
				require.Contains(t, answer.ContentType, "text/markdown")
			}
			require.EqualValues(t, 1, calls.Load())
			cached, err := call(context.Background(), channel, query)
			require.NoError(t, err)
			require.True(t, cached.Cached)
			require.EqualValues(t, 1, calls.Load())

			channel.Key = "alternate-test-key"
			prepare()
			fresh, err := call(context.Background(), channel, query)
			require.NoError(t, err)
			require.False(t, fresh.Cached)
			require.EqualValues(t, 2, calls.Load())
		})
	}
}

func TestDirectToolsProviderRefusalAndEmptySearch(t *testing.T) {
	for _, fixture := range []struct {
		status    int
		body      string
		wantEmpty bool
	}{
		{http.StatusTooManyRequests, `{"error":{"message":"busy"}}`, false},
		{http.StatusOK, `{"id":"test-id","results":[]}`, true},
	} {
		var calls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
			calls.Add(1)
			writer.WriteHeader(fixture.status)
			_, _ = writer.Write([]byte(fixture.body))
		}))
		defer upstream.Close()
		channel := abstract.ChannelContext{ChannelType: constant.ChannelTypePerplexity, BaseURL: upstream.URL, Key: "test-key", PreferredProviders: []string{websearch_setting.ProviderPerplexity}, OnlyProviders: true}
		query := request.Query{Contract: abstract.ContractToolsSearch, Text: "test"}
		answer, err := websearch.Search(context.Background(), channel, query)
		if fixture.wantEmpty {
			require.ErrorIs(t, err, abstract.ErrEmptyResults)
		} else {
			require.NoError(t, err)
			require.Equal(t, fixture.status, answer.StatusCode)
			require.JSONEq(t, fixture.body, string(answer.Body))
		}
		require.EqualValues(t, 1, calls.Load())
		query.Text = strings.Repeat("界", 2731)
		_, err = websearch.Search(context.Background(), channel, query)
		require.ErrorContains(t, err, "exceeds 8192 bytes")
		require.False(t, request.WasSent(err))
		require.EqualValues(t, 1, calls.Load())
	}
}
