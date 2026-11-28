package websearch_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProviderIsolationUsesOneCredentialSource(t *testing.T) {
	for _, tc := range []struct {
		name, key, binding, provider, path, auth, exaKey, preparedHeader string
		wantProxy                                                        bool
	}{
		{"globalExa", "channel-key", "", "exa", "/search", "", "global-key", "", false},
		{"bindingWithoutKey", "", "exa", "exa", "/search", "", "global-key", "", false},
		{"bindingWithKey", "channel-key", "exa", "exa", "/search", "", "channel-key", "", true},
		{"native", "channel-key", "", "deepseek", "/v1/tools/search", "Bearer channel-key", "", "native-only", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var globalHits, proxyHits, directChannelHits atomic.Int32
			serve := func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, tc.auth, r.Header.Get("Authorization"))
				assert.Equal(t, tc.exaKey, r.Header.Get("X-Api-Key"))
				assert.Equal(t, tc.preparedHeader, r.Header.Get("X-Channel-Header"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":[{"title":"Fixture","url":"https://example.test/","highlights":["Fixture snippet"]}]}`))
			}
			global := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				globalHits.Add(1)
				serve(w, r)
			}))
			defer global.Close()
			channelUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				directChannelHits.Add(1)
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer channelUpstream.Close()
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxyHits.Add(1)
				assert.Equal(t, channelUpstream.URL+tc.path, r.URL.String())
				serve(w, r)
			}))
			defer proxy.Close()

			settings := websearch_setting.Get()
			settings.ExaBaseURL, settings.ExaAPIKey = global.URL, "global-key"
			settings.GroupPolicies = "{}"
			settings.CandidateOrder = tc.provider
			ctx := abstract.WithRoutingPolicy(t.Context(), abstract.RoutingPolicy{Settings: settings})
			channel := abstract.ChannelContext{ChannelType: constant.ChannelTypeDeepSeek, BaseURL: channelUpstream.URL, Key: tc.key}
			channel.Settings.Proxy = proxy.URL
			channel.Settings.WebSearchProvider = tc.binding
			query := request.Query{
				Contract: abstract.ContractToolsSearch, Text: t.Name(), Raw: []byte(`{"query":"fixture"}`),
				EndpointProvider: "deepseek", Endpoint: channelUpstream.URL + "/v1/tools/search",
				Prepare: func(r *http.Request) error {
					r.Header.Set("X-Channel-Header", "native-only")
					return nil
				},
			}
			answer, err := websearch.Search(ctx, channel, query)
			require.NoError(t, err)
			require.Equal(t, tc.provider, answer.Provider)
			require.EqualValues(t, 0, directChannelHits.Load())
			if tc.wantProxy {
				require.EqualValues(t, 1, proxyHits.Load())
				require.EqualValues(t, 0, globalHits.Load())
			} else {
				require.EqualValues(t, 0, proxyHits.Load())
				require.EqualValues(t, 1, globalHits.Load())
			}
		})
	}
}
