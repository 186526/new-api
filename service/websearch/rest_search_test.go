package websearch_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	channeladaptor "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/baidu_v2"
	"github.com/QuantumNous/new-api/relay/channel/zhipu_4v"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRESTSearchReplay(t *testing.T) {
	for _, fixture := range []struct {
		channelType                               int
		provider, path, sent, hit, empty, refusal string
		adaptor                                   channeladaptor.WebSearchAdaptor
	}{
		{constant.ChannelTypeZhipu_v4, "zhipu", "/api/paas/v4/web_search", `{"search_query":"weather","search_engine":"search_std","search_intent":false,"count":3}`, `{"search_result":[{"title":"Weather","link":"https://example.test/weather","content":"Weather excerpt","media":"Weather site","publish_date":"2025-05-23"}]}`, `{"search_result":[]}`, `{"error":{"code":"1701","message":"busy"}}`, &zhipu_4v.Adaptor{}},
		{constant.ChannelTypeBaiduV2, "baidu_v2", "/v2/ai_search/web_search", `{"messages":[{"role":"user","content":"weather"}],"search_source":"baidu_search_v2","resource_type_filter":[{"type":"web","top_k":3}]}`, `{"request_id":"fixture","references":[{"id":1,"title":"Weather","url":"https://example.test/weather","content":"Weather excerpt","website":"Weather site","date":"2025-05-23 00:00:00","type":"web","icon":null}]}`, `{"request_id":"fixture","references":[]}`, `{"request_id":"fixture","code":"InternalError","message":"busy"}`, &baidu_v2.Adaptor{}},
	} {
		t.Run(fixture.provider, func(t *testing.T) {
			for _, replay := range []struct{ name, body string }{{"hit", fixture.hit}, {"empty", fixture.empty}, {"refusal", fixture.refusal}} {
				t.Run(replay.name, func(t *testing.T) {
					server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
						if req.Method != http.MethodPost || req.URL.Path != fixture.path || req.Header.Get("Authorization") != "Bearer fixture-key" || req.Header.Get("Content-Type") != "application/json" {
							t.Error("incorrect method, path or headers")
						}
						sent, err := io.ReadAll(req.Body)
						if err != nil || string(sent) != fixture.sent {
							t.Errorf("request body %q, error %v", sent, err)
						}
						_, _ = io.WriteString(writer, replay.body)
					}))
					defer server.Close()
					selected := abstract.ChannelContext{ChannelType: fixture.channelType, BaseURL: server.URL, Key: "fixture-key", PreferredProviders: []string{fixture.provider}, OnlyProviders: true}
					selected.Settings.WebSearchCacheTTLSeconds = -1
					ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
					ginCtx.Request = httptest.NewRequest(http.MethodPost, "/moonshot.service/v1/search", nil)
					info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: selected.ChannelType, ChannelBaseUrl: selected.BaseURL, ApiKey: selected.Key}}
					prepared, err := fixture.adaptor.PrepareWebSearch(ginCtx, info, abstract.ContractMoonshotSearch)
					require.NoError(t, err)
					query := request.Query{Contract: abstract.ContractMoonshotSearch, Text: "weather", Count: 3, Model: "do-not-send", EndpointProvider: prepared.Provider, Endpoint: prepared.Endpoint, Prepare: prepared.Prepare}
					answer, err := websearch.Search(context.Background(), selected, query)
					switch replay.name {
					case "hit":
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, answer.StatusCode)
						require.JSONEq(t, `{"search_results":[{"title":"Weather","url":"https://example.test/weather","snippet":"Weather excerpt","date":"2025-05-23","site_name":"Weather site"}]}`, string(answer.Body))
					case "empty":
						require.ErrorIs(t, err, abstract.ErrEmptyResults)
					case "refusal":
						require.ErrorContains(t, err, "search refused")
					}
				})
			}
		})
	}
}
