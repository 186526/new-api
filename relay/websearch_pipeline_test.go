package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/setting/websearch_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// These tests drive service/websearch directly, which is the seam every relay
// handler now goes through. They describe what the pipeline itself guarantees —
// a native candidate's bytes survive, a non-native one is rendered, a candidate
// that never sent the request is skipped, one that may have been billed stops
// the walk, and the cache follows the channel's TTL — so a handler test only has
// to check that it writes the returned answer.

// pipelineChannel builds the channel context a relay handler would pass. The
// cache is off unless a test turns it on, because the forward cache lives for
// the whole test process and a leftover entry would answer a later test's stub.
func pipelineChannel(channelType int, baseURL string) abstract.ChannelContext {
	return abstract.ChannelContext{
		ChannelID:   9000 + channelType,
		ChannelType: channelType,
		BaseURL:     baseURL,
		Key:         "channel-key",
		Settings: dto.ChannelSettings{
			WebSearchCacheTTLSeconds:      -1,
			WebSearchFetchCacheTTLSeconds: -1,
		},
	}
}

// uniqueText keeps one test's cache entry away from another's, including a
// second run inside the same process.
func uniqueText(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
}

// TestPipelineForwardsANativeAnswerByteForByte requires the answer of a native
// candidate to be the upstream's own bytes. The body carries a null member, a
// duplicated URL and an undocumented field, all of which the abstraction drops;
// seeing them come back proves the client is not reading a rendered guess.
func TestPipelineForwardsANativeAnswerByteForByte(t *testing.T) {
	initLegacyWebsearchTest(t)

	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Trace", "trace-1")
		_, _ = strings.NewReader(richToolsSearchAnswer).WriteTo(w)
	}))
	t.Cleanup(upstream.Close)

	channel := pipelineChannel(constant.ChannelTypeDeepSeek, upstream.URL)
	query := request.Query{
		Contract: abstract.ContractToolsSearch,
		Text:     uniqueText(t),
		Raw:      []byte(`{"query":"deepseek","count":2}`),
		// The handler injects the adaptor's URL; a native candidate forwards to
		// it rather than to a URL this package builds.
		Endpoint: upstream.URL + "/v1/tools/search",
	}

	answer, err := websearch.Search(t.Context(), channel, query)
	require.NoError(t, err)

	assert.Equal(t, richToolsSearchAnswer, string(answer.Body),
		"a native candidate's answer must reach the client unchanged, nulls and undocumented fields included")
	assert.False(t, answer.Cached, "the first call reads the upstream")
	assert.Equal(t, http.StatusOK, answer.StatusCode)
	assert.Equal(t, "application/json", answer.ContentType)
	assert.Equal(t, "trace-1", answer.Header.Get("X-Upstream-Trace"),
		"the upstream headers travel with a native answer")
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
}

// TestPipelineRendersANonNativeAnswer requires a provider that does not speak
// the contract to still serve it. Exa publishes no client document, so the only
// way its answer reaches a tools/search client is the renderer; the rendered
// document must keep the null members the client distinguishes from empty ones.
func TestPipelineRendersANonNativeAnswer(t *testing.T) {
	initLegacyWebsearchTest(t)

	exaAnswer := `{"results":[{"title":"A","url":"https://a.example/","highlights":["sa"],"summary":"sum"}]}`
	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		assert.Equal(t, "/search", r.URL.Path, "the exa requester owns its own path")
		assert.Equal(t, "channel-key", r.Header.Get("x-api-key"), "the channel key is what signs an exa call")
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(exaAnswer).WriteTo(w)
	}))
	t.Cleanup(upstream.Close)

	// Codex speaks alpha/search only, so binding this channel to exa leaves exa
	// as the one provider that can answer tools/search here.
	channel := pipelineChannel(constant.ChannelTypeCodex, upstream.URL)
	channel.Settings.WebSearchProvider = websearch_setting.ProviderExa

	candidates, err := websearch.Candidates(channel, abstract.ContractToolsSearch)
	require.NoError(t, err)
	require.NotEmpty(t, candidates)
	assert.Equal(t, websearch_setting.ProviderExa, candidates[0].Provider,
		"the operator binding is the candidate that answers this contract")

	query := request.Query{
		Contract: abstract.ContractToolsSearch,
		Text:     uniqueText(t),
		// Exa reads its base URL from the endpoint the caller injects.
		Endpoint: upstream.URL,
	}

	answer, err := websearch.Search(t.Context(), channel, query)
	require.NoError(t, err)

	body := string(answer.Body)
	assert.NotEqual(t, exaAnswer, body, "a non-native answer is rendered, not forwarded")
	assert.Equal(t, "application/json", answer.ContentType)
	assert.Equal(t, "A", gjson.Get(body, "results.0.title").String())
	assert.Equal(t, "https://a.example/", gjson.Get(body, "results.0.url").String())
	assert.Equal(t, gjson.Null, gjson.Get(body, "results.0.rerank_score").Type,
		"a member exa never publishes must read as null, not disappear")
	assert.Equal(t, gjson.Null, gjson.Get(body, "results.0.content").Type)
	assert.Equal(t, gjson.Null, gjson.Get(body, "results.0.date").Type)
	assert.True(t, gjson.Get(body, "results.0.site_name").Exists(),
		"a member the renderer can derive is written")
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))
}

func TestPipelineExcludesProvidersWithoutFetchCapability(t *testing.T) {
	initLegacyWebsearchTest(t)

	// The fetcher refuses a loopback address through the SSRF guard, and a test
	// server only listens there.
	fetchSetting := system_setting.GetFetchSetting()
	previousSSRF := fetchSetting.EnableSSRFProtection
	fetchSetting.EnableSSRFProtection = false
	t.Cleanup(func() { fetchSetting.EnableSSRFProtection = previousSSRF })

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = strings.NewReader(`<html><head><title>Local Page</title></head>` +
			`<body><article><p>Fetched body.</p></article></body></html>`).WriteTo(w)
	}))
	t.Cleanup(page.Close)

	channel := pipelineChannel(constant.ChannelTypeAntigravity, "")
	// A key that is not an OAuth document fails while the credential is parsed,
	// which is before any fetch endpoint is touched.
	channel.Key = "not-an-oauth-key"

	candidates, err := websearch.Candidates(channel, abstract.ContractToolsFetch)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	assert.Equal(t, websearch_setting.ProviderLocal, candidates[0].Provider)

	answer, err := websearch.Fetch(t.Context(), channel, request.Query{
		Contract: abstract.ContractToolsFetch,
		URL:      page.URL,
	})
	require.NoError(t, err)

	body := string(answer.Body)
	assert.Contains(t, body, "Fetched body.")
	assert.Equal(t, "application/json", answer.ContentType)
}

// TestPipelineStopsAfterACandidateThatMayHaveBeenBilled requires the walk to end
// at the first candidate whose request reached its upstream, even when that
// candidate fails. Trying another provider would pay for one client request
// twice and could hand back a differently shaped answer.
func TestPipelineStopsAfterACandidateThatMayHaveBeenBilled(t *testing.T) {
	initLegacyWebsearchTest(t)

	var searchHits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1internal:generateContent" {
			// A 200 the requester cannot unwrap: the request was answered, so the
			// failure is reported as sent.
			w.Header().Set("Content-Type", "application/json")
			_, _ = strings.NewReader("not json").WriteTo(w)
			return
		}
		atomic.AddInt32(&searchHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(richToolsSearchAnswer).WriteTo(w)
	}))
	t.Cleanup(upstream.Close)

	channel := pipelineChannel(constant.ChannelTypeDeepSeek, upstream.URL)
	channel.Settings.WebSearchProvider = websearch_setting.ProviderAntigravity
	// A usable credential keeps the requester from refreshing a token over the
	// network, so the stub is the only upstream this test talks to.
	channel.Key = fmt.Sprintf(
		`{"access_token":"at","refresh_token":"rt","project_id":"proj","expired":%q}`,
		time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	)

	candidates, err := websearch.Candidates(channel, abstract.ContractToolsSearch)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	assert.Equal(t, websearch_setting.ProviderAntigravity, candidates[0].Provider,
		"the bound provider is tried first")
	assert.Equal(t, websearch_setting.ProviderDeepSeek, candidates[1].Provider,
		"the channel's own upstream is the candidate that must not be reached")

	_, err = websearch.Search(t.Context(), channel, request.Query{
		Contract: abstract.ContractToolsSearch,
		Text:     uniqueText(t),
		Raw:      []byte(`{"query":"billed"}`),
		Endpoint: upstream.URL + "/v1/tools/search",
	})
	require.Error(t, err)
	assert.True(t, request.WasSent(err), "the failure must report that the request may have been billed")
	assert.Equal(t, int32(0), atomic.LoadInt32(&searchHits),
		"the second candidate must not be asked after one request may already have been charged")
}

// TestPipelineCachesARepeatedQuery requires the forward cache to answer the
// second identical query without a second upstream call, and to report that the
// answer came from the cache so a caller can count the hit.
func TestPipelineCachesARepeatedQuery(t *testing.T) {
	initLegacyWebsearchTest(t)

	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(richToolsSearchAnswer).WriteTo(w)
	}))
	t.Cleanup(upstream.Close)

	channel := pipelineChannel(constant.ChannelTypeDeepSeek, upstream.URL)
	channel.Settings.WebSearchCacheTTLSeconds = 60
	query := request.Query{
		Contract: abstract.ContractToolsSearch,
		Text:     uniqueText(t),
		Raw:      []byte(`{"query":"cached"}`),
		Endpoint: upstream.URL + "/v1/tools/search",
	}

	first, err := websearch.Search(t.Context(), channel, query)
	require.NoError(t, err)
	second, err := websearch.Search(t.Context(), channel, query)
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&hits),
		"the second query must be answered from the cache, not from the upstream")
	assert.False(t, first.Cached, "the call that reached the upstream is not a cache hit")
	assert.True(t, second.Cached, "the caller must be able to count the hit")
	assert.Equal(t, websearch_setting.ProviderDeepSeek, first.Provider)
	assert.Equal(t, first.Provider, second.Provider)
	assert.Equal(t, string(first.Body), string(second.Body))
}

// TestPipelineDisablesTheCacheOnANegativeTTL requires a channel that sets -1 to
// turn the cache off: a repeated query must reach the upstream again, which is
// what an operator sets when an upstream's answers change too fast to reuse.
func TestPipelineDisablesTheCacheOnANegativeTTL(t *testing.T) {
	initLegacyWebsearchTest(t)

	var hits int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(richToolsSearchAnswer).WriteTo(w)
	}))
	t.Cleanup(upstream.Close)

	channel := pipelineChannel(constant.ChannelTypeDeepSeek, upstream.URL)
	channel.Settings.WebSearchCacheTTLSeconds = -1
	query := request.Query{
		Contract: abstract.ContractToolsSearch,
		Text:     uniqueText(t),
		Raw:      []byte(`{"query":"uncached"}`),
		Endpoint: upstream.URL + "/v1/tools/search",
	}

	for range 2 {
		answer, err := websearch.Search(t.Context(), channel, query)
		require.NoError(t, err)
		assert.False(t, answer.Cached)
	}

	assert.Equal(t, int32(2), atomic.LoadInt32(&hits))
}

func TestPipelineReturnsUpstreamRefusalWithoutRetry(t *testing.T) {
	initLegacyWebsearchTest(t)

	const refusal = `{"results":[{"title":"refused","url":"https://a.example/"}]}`
	var attempts int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = strings.NewReader(refusal).WriteTo(w)
	}))
	t.Cleanup(upstream.Close)

	channel := pipelineChannel(constant.ChannelTypeDeepSeek, upstream.URL)
	answer, err := websearch.Search(t.Context(), channel, request.Query{
		Contract: abstract.ContractToolsSearch,
		Text:     uniqueText(t),
		Raw:      []byte(`{"query":"rate limited"}`),
		Endpoint: upstream.URL + "/v1/tools/search",
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusTooManyRequests, answer.StatusCode)
	assert.Equal(t, websearch_setting.ProviderDeepSeek, answer.Provider)
	assert.Equal(t, refusal, string(answer.Body))
	assert.Equal(t, int32(1), atomic.LoadInt32(&attempts))
}

// TestAlphaSearchRoutesThroughThePipelineToItsNativeUpstream requires the Codex
// channel, which is the native candidate for alpha/search, to keep the path the
// adaptor routes to and both bodies byte for byte now that the handler delegates.
func TestAlphaSearchRoutesThroughThePipelineToItsNativeUpstream(t *testing.T) {
	initLegacyWebsearchTest(t)

	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = strings.NewReader(alphaSearchAnswer).WriteTo(w)
	}))
	t.Cleanup(upstream.Close)

	channel := pipelineChannel(constant.ChannelTypeCodex, upstream.URL)
	raw := `{"id":"req_1","model":"gpt-5.1","commands":{"search_query":[{"q":"weather"}]},"future_field":true}`

	answer, err := websearch.Search(t.Context(), channel, request.Query{
		Contract: abstract.ContractAlphaSearch,
		Text:     "weather",
		Raw:      []byte(raw),
		Endpoint: upstream.URL + "/backend-api/codex/alpha/search",
	})
	require.NoError(t, err)

	assert.Equal(t, "/backend-api/codex/alpha/search", gotPath)
	assert.Equal(t, raw, gotBody, "the client's Codex body must reach the upstream untouched")
	assert.Equal(t, alphaSearchAnswer, string(answer.Body), "and the upstream's document must reach the client untouched")
}

func TestPipelinePreferenceReordersOnlyReachableProviders(t *testing.T) {
	channel := pipelineChannel(constant.ChannelTypeDeepSeek, "")
	channel.PreferredProviders = []string{"local", "codex", "deepseek"}
	candidates, err := websearch.Candidates(channel, abstract.ContractToolsFetch)
	require.NoError(t, err)
	require.Len(t, candidates, 2)
	assert.Equal(t, "local", candidates[0].Provider)
	assert.Equal(t, "deepseek", candidates[1].Provider)
}

func TestPipelinePreferredGlobalProviderDoesNotReceiveChannelCredentials(t *testing.T) {
	initLegacyWebsearchTest(t)
	previous := websearch_setting.Get()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
			"websearch_setting.exa_base_url": previous.ExaBaseURL, "websearch_setting.exa_api_key": previous.ExaAPIKey,
		}))
		require.NoError(t, websearch_setting.UpdateAndSync())
	})
	var exaHits, channelHits atomic.Int32
	exa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		exaHits.Add(1)
		assert.Equal(t, "/search", req.URL.Path)
		assert.Equal(t, "global-test-key", req.Header.Get("X-API-Key"))
		assert.Empty(t, req.Header.Get("Authorization"))
		// A refusal proves that the pipeline must not switch to the channel once
		// the preferred provider has received the request.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"rejected"}`)
	}))
	t.Cleanup(exa.Close)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		channelHits.Add(1)
		_, _ = io.WriteString(w, richToolsSearchAnswer)
	}))
	t.Cleanup(upstream.Close)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"websearch_setting.exa_base_url": exa.URL, "websearch_setting.exa_api_key": "global-test-key",
	}))
	require.NoError(t, websearch_setting.UpdateAndSync())
	channel := pipelineChannel(constant.ChannelTypeDeepSeek, upstream.URL)
	channel.PreferredProviders = []string{"exa"}
	answer, err := websearch.Search(t.Context(), channel, request.Query{
		Contract: abstract.ContractToolsSearch,
		Text:     uniqueText(t),
		Raw:      []byte(`{"query":"test"}`),
		Endpoint: upstream.URL + "/v1/tools/search",
		Prepare: func(req *http.Request) error {
			req.Header.Set("Authorization", "Bearer channel-key")
			return nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, answer.StatusCode)
	assert.EqualValues(t, 1, exaHits.Load())
	assert.Zero(t, channelHits.Load())
}
