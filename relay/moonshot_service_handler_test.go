package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runMoonshotServiceHelper drives one Kimi Code request against a stub upstream
// the way the router does: the channel context the distributor would have
// written, the same validation, the same price lookup, and a settlement stub the
// test reads afterwards.
func runMoonshotServiceHelper(t *testing.T, path, body string, upstreamBody string, channelType int, upstreamStatus ...int) (*httptest.ResponseRecorder, *recordedSettlement, *types.NewAPIError) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if len(upstreamStatus) != 0 {
			w.WriteHeader(upstreamStatus[0])
		}
		_, _ = io.WriteString(w, upstreamBody)
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	routingName := constant.ToolsSearchModel
	if path == relayconstant.MoonshotFetchPath {
		routingName = constant.ToolsFetchModel
	}
	common.SetContextKey(c, constant.ContextKeyOriginalModel, routingName)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")

	info, settlement := prepareMoonshotServiceTest(t, c)
	apiErr := RefundFailedRequestBilling(c, info, MoonshotServiceHelper(c, info))
	return recorder, settlement, apiErr
}

func TestMoonshotServiceRejectsUnsuccessfulAnswersWithoutCharging(t *testing.T) {
	initLegacyWebsearchTest(t)
	for _, tc := range []struct {
		path   string
		body   string
		status int
	}{
		{relayconstant.MoonshotSearchPath, `{"text_query":"rate limit"}`, http.StatusTooManyRequests},
		{relayconstant.MoonshotSearchPath, `{"text_query":"internal error"}`, http.StatusInternalServerError},
		{relayconstant.MoonshotFetchPath, `{"url":"https://a.example/rate-limit"}`, http.StatusTooManyRequests},
		{relayconstant.MoonshotFetchPath, `{"url":"https://a.example/internal-error"}`, http.StatusInternalServerError},
	} {
		t.Run(tc.path+"/"+http.StatusText(tc.status), func(t *testing.T) {
			// A result-shaped error payload must still be classified by its status.
			recorder, settlement, apiErr := runMoonshotServiceHelper(t, tc.path, tc.body,
				`{"search_results":[{"title":"error"}],"error":{"message":"upstream refusal"}}`,
				constant.ChannelTypeDeepSeek, tc.status)
			if tc.path == relayconstant.MoonshotSearchPath {
				require.Nil(t, apiErr)
				assert.Equal(t, http.StatusOK, recorder.Code)
				assert.JSONEq(t, `{"search_results":[]}`, recorder.Body.String())
			} else {
				require.NotNil(t, apiErr)
				assert.Equal(t, tc.status, apiErr.StatusCode)
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.Empty(t, recorder.Body.String())
			}
			assert.Empty(t, settlement.settled)
			assert.Equal(t, 1, settlement.refundCalls)
		})
	}
}

func prepareMoonshotServiceTest(t *testing.T, c *gin.Context) (*relaycommon.RelayInfo, *recordedSettlement) {
	t.Helper()
	request, err := helper.GetAndValidateRequest(c, types.RelayFormatMoonshotService)
	require.NoError(t, err)
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatMoonshotService, request, nil)
	require.NoError(t, err)
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(request.GetTokenCountMeta().CombineText)}
	_, err = helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	require.NoError(t, err, "the routing name is priced out of the box")
	settlement := &recordedSettlement{}
	info.Billing = settlement
	return info, settlement
}

type pendingToolRefund struct{ recordedSettlement }

func (s *pendingToolRefund) NeedsRefund() bool { return !s.refunded }

func TestMoonshotServiceRefundObservation(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{}
	recordWebsearchResult(info, abstract.ContractMoonshotSearch, websearch.Answer{StatusCode: http.StatusOK, Cached: true}, nil)
	info.Billing = &recordedSettlement{}
	moonshotServiceRefund(ctx, info, moonshotServiceOperationSearch, moonshotServiceOutcomeEmpty, true)
	require.Equal(t, &relaycommon.ToolExecutionMeta{Contract: "moonshot/search", Outcome: "empty", CacheHit: true}, info.RequestObservation().Tool)

	settlement := &pendingToolRefund{}
	info.Billing = settlement
	moonshotServiceRefund(ctx, info, moonshotServiceOperationSearch, moonshotServiceOutcomeEmpty, true)
	require.Equal(t, 1, settlement.refundCalls)
	require.Equal(t, "requested", info.RequestObservation().Tool.Refund)
}

func TestMoonshotServiceSearchUsesCodexAlpha(t *testing.T) {
	testToolSearchUsesCodexAlpha(t, relayconstant.MoonshotSearchPath)
}

func TestDeepSeekToolsSearchUsesCodexAlpha(t *testing.T) {
	testToolSearchUsesCodexAlpha(t, toolsSearchPath)
}

func testToolSearchUsesCodexAlpha(t *testing.T, path string) {
	t.Helper()
	initLegacyWebsearchTest(t)

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/backend-api/codex/alpha/search", r.URL.Path)
		assert.Equal(t, "Bearer test-access-token", r.Header.Get("Authorization"))
		assert.Equal(t, "test-account", r.Header.Get("ChatGPT-Account-ID"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		assert.Equal(t, "channel-value", r.Header.Get("X-Channel-Override"))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.JSONEq(t, `{"model":"gpt-5.1","commands":{"search_query":[{"q":"query"}]}}`, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"s_1","results":[
			{"type":"text_result","title":"A","url":"https://a.example/","snippet":"sa"},
			{"type":"text_result","title":"B","url":"https://b.example/","snippet":"sb"}]}`)
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := `{"text_query":"  query  ","num_results":1,"model":"ignored-client-model"}`
	if path == toolsSearchPath {
		body = `{"query":"query","count":1}`
	}
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, constant.ToolsSearchModel)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCodex)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, `{"access_token":"test-access-token","account_id":"test-account"}`)
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, `{"tools-search":"search-alias","search-alias":"gpt-5.1"}`)
	common.SetContextKey(c, constant.ContextKeyChannelHeaderOverride, map[string]any{"X-Channel-Override": "channel-value"})

	if path == toolsSearchPath {
		request, err := helper.GetAndValidateRequest(c, types.RelayFormatDeepSeekTools)
		require.NoError(t, err)
		info, err := relaycommon.GenRelayInfo(c, types.RelayFormatDeepSeekTools, request, nil)
		require.NoError(t, err)
		info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(body)}
		_, err = helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
		require.NoError(t, err)
		settlement := &recordedSettlement{}
		info.Billing = settlement
		require.Nil(t, DeepSeekToolsHelper(c, info))
		assert.Equal(t, &relaycommon.ToolExecutionMeta{Contract: "tools/search", Provider: "codex", Outcome: "success"}, info.RequestObservation().Tool)
		assert.Contains(t, recorder.Body.String(), `"results":[`)
		assert.Contains(t, recorder.Body.String(), `"url":"https://a.example/"`)
		assert.NotContains(t, recorder.Body.String(), `https://b.example/`)
		assert.Equal(t, []int{5000}, settlement.settled)
		assert.False(t, settlement.refunded)
	} else {
		info, settlement := prepareMoonshotServiceTest(t, c)
		require.Nil(t, MoonshotServiceHelper(c, info))
		assert.JSONEq(t, `{"search_results":[{"title":"A","url":"https://a.example/","snippet":"sa","date":null}]}`, recorder.Body.String())
		assert.Equal(t, relayconstant.RelayModeMoonshotSearch, info.RelayMode)
		assert.Equal(t, constant.ToolsSearchModel, info.UpstreamModelName)
		assert.False(t, info.IsModelMapped)
		assert.Equal(t, []int{5000}, settlement.settled)
		assert.False(t, settlement.refunded)
	}
	require.EqualValues(t, 1, calls.Load())
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
}

func TestMoonshotServiceSearchKeepsExaCandidateIndependentOfCodex(t *testing.T) {
	initLegacyWebsearchTest(t)

	var codexCalls atomic.Int32
	codex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		codexCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(codex.Close)

	exa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/search", r.URL.Path)
		assert.Equal(t, "test-exa-key", r.Header.Get("x-api-key"))
		assert.Empty(t, r.Header.Get("ChatGPT-Account-ID"))
		assert.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"title":"Fallback","url":"https://fallback.example/","summary":"fallback snippet","highlights":["fallback snippet"]}]}`)
	}))
	t.Cleanup(exa.Close)

	settings := config.GlobalConfig.Get("websearch_setting").(*websearch_setting.WebSearchSetting)
	previous := *settings
	t.Cleanup(func() {
		*settings = previous
		assert.NoError(t, websearch_setting.UpdateAndSync())
	})
	settings.ExaBaseURL = exa.URL
	settings.ExaAPIKey = "test-exa-key"
	for _, testCase := range []struct{ name, mapping, order string }{
		{name: "missing mapping", order: "codex,exa"},
		{name: "empty mapped model", mapping: `{"tools-search":""}`, order: "codex,exa"},
		{name: "self mapping", mapping: `{"tools-search":"tools-search"}`, order: "codex,exa"},
		{name: "Exa preferred to a mapped Codex channel", mapping: `{"tools-search":"gpt-5.1"}`, order: "exa,codex"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			settings.CandidateOrder = testCase.order
			require.NoError(t, websearch_setting.UpdateAndSync())
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, relayconstant.MoonshotSearchPath,
				strings.NewReader(`{"text_query":"`+testCase.name+`","model":"ignored-client-model"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, constant.ToolsSearchModel)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCodex)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, codex.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, `{"access_token":"test-access-token","account_id":"test-account"}`)
			common.SetContextKey(c, constant.ContextKeyChannelModelMapping, testCase.mapping)

			info, settlement := prepareMoonshotServiceTest(t, c)
			require.Nil(t, MoonshotServiceHelper(c, info))
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), `"title":"Fallback"`)
			assert.Equal(t, &relaycommon.ToolExecutionMeta{Contract: "moonshot/search", Provider: "exa", Outcome: "success"}, info.RequestObservation().Tool)
			assert.Equal(t, []int{5000}, settlement.settled)
		})
	}
	assert.Zero(t, codexCalls.Load())
}

// TestMoonshotServiceSearchSettlesOnlyAHit requires the search endpoint to bill
// one call when the provider returned a hit and to release the reservation when
// it returned an empty list: the upstream charges only a successful answer, so a
// client that found nothing must not pay for the attempt.
func TestMoonshotServiceSearchSettlesOnlyAHit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answer   string
		billable bool
	}{
		{
			name:     "a hit settles one call",
			answer:   `{"results":[{"title":"A","url":"https://a.example/","snippet":"sa"}]}`,
			billable: true,
		},
		{
			name:   "an empty list refunds the reservation",
			answer: `{"results":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initLegacyWebsearchTest(t)

			recorder, settlement, apiErr := runMoonshotServiceHelper(t,
				relayconstant.MoonshotSearchPath,
				`{"text_query":"deepseek"}`,
				tc.answer,
				constant.ChannelTypeDeepSeek,
			)
			require.Nil(t, apiErr)
			assert.Equal(t, http.StatusOK, recorder.Code)
			rendered := recorder.Body.String()
			if tc.billable {
				assert.Contains(t, rendered, `"search_results"`)
				assert.Contains(t, rendered, "https://a.example/")
				assert.Equal(t, []int{5000}, settlement.settled)
				assert.False(t, settlement.refunded)
				return
			}
			assert.JSONEq(t, `{"search_results":[]}`, rendered,
				"the client reads an empty array as nothing found, never as null")
			assert.Empty(t, settlement.settled)
			assert.True(t, settlement.refunded, "an empty answer must release the reservation")
		})
	}
}

// TestMoonshotServiceFetchSettlesOnlyAPage requires the fetch endpoint to bill
// one call when the provider returned a body and to release the reservation when
// the page carried no text. The client falls back to fetching locally after an
// empty answer, so charging for one would charge for work the client did itself.
func TestMoonshotServiceFetchSettlesOnlyAPage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answer   string
		billable bool
	}{
		{name: "a page settles one call", answer: `{"title":"Example","content":"# Example\n\nBody."}`, billable: true},
		{name: "an empty page refunds the reservation", answer: `{"title":"Example","content":""}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initLegacyWebsearchTest(t)

			recorder, settlement, apiErr := runMoonshotServiceHelper(t,
				relayconstant.MoonshotFetchPath,
				`{"url":"https://a.example/"}`,
				tc.answer,
				constant.ChannelTypeDeepSeek,
			)
			require.Nil(t, apiErr)
			assert.Equal(t, http.StatusOK, recorder.Code,
				"a failed fetch is answered 200 so the client can fall back")
			assert.Equal(t, "text/markdown; charset=utf-8", recorder.Header().Get("Content-Type"))

			if tc.billable {
				assert.Equal(t, []int{1000}, settlement.settled)
				assert.False(t, settlement.refunded)
				assert.Contains(t, recorder.Body.String(), "Body.")
				return
			}
			assert.Empty(t, settlement.settled)
			assert.True(t, settlement.refunded, "an empty page must release the reservation")
		})
	}
}

// An empty answer must read exactly like a full one apart from the array: the
// handler used to write it through c.JSON, which appends "; charset=utf-8" and
// so made the two responses disagree on a header the client sees.
func TestMoonshotServiceEmptySearchCarriesTheSameContentType(t *testing.T) {
	initLegacyWebsearchTest(t)

	hit, _, hitErr := runMoonshotServiceHelper(t,
		relayconstant.MoonshotSearchPath,
		`{"text_query":"deepseek"}`,
		`{"results":[{"title":"A","url":"https://a.example/","snippet":"sa"}]}`,
		constant.ChannelTypeDeepSeek,
	)
	require.Nil(t, hitErr)

	empty, _, emptyErr := runMoonshotServiceHelper(t,
		relayconstant.MoonshotSearchPath,
		`{"text_query":"deepseek"}`,
		`{"results":[]}`,
		constant.ChannelTypeDeepSeek,
	)
	require.Nil(t, emptyErr)

	assert.Equal(t, "application/json", hit.Header().Get("Content-Type"))
	assert.Equal(t, hit.Header().Get("Content-Type"), empty.Header().Get("Content-Type"),
		"an empty answer must not add a charset the full answer does not carry")
	assert.JSONEq(t, `{"search_results":[]}`, empty.Body.String())
}

// Surrounding spaces are not part of a query: they must not reach the upstream.
// Trimming also keeps one query on one cache key, which matters because the key
// is the text the provider is sent.
func TestMoonshotServiceSearchTrimsTheQuery(t *testing.T) {
	initLegacyWebsearchTest(t)

	seen := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case seen <- string(body):
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[{"title":"A","url":"https://a.example/","snippet":"sa"}]}`)
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, relayconstant.MoonshotSearchPath,
		strings.NewReader(`{"text_query":"  deepseek  "}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, constant.ToolsSearchModel)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeDeepSeek)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")

	request, err := helper.GetAndValidateRequest(c, types.RelayFormatMoonshotService)
	require.NoError(t, err)
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatMoonshotService, request, nil)
	require.NoError(t, err)
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(`{"text_query":"  deepseek  "}`)}
	_, err = helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	require.NoError(t, err)
	info.Billing = &recordedSettlement{}

	require.Nil(t, MoonshotServiceHelper(c, info))
	sent := <-seen
	assert.Contains(t, sent, `"query":"deepseek"`)
	assert.NotContains(t, sent, `"  deepseek`)
}

// A caller that names a count gets that many results. The upstream decides its
// own count, so the cap is applied to the rendered answer.
func TestMoonshotServiceSearchCapsTheResults(t *testing.T) {
	initLegacyWebsearchTest(t)

	upstreamBody := `{"results":[` +
		`{"title":"A","url":"https://a.example/","snippet":"sa"},` +
		`{"title":"B","url":"https://b.example/","snippet":"sb"},` +
		`{"title":"C","url":"https://c.example/","snippet":"sc"}]}`
	recorder, _, apiErr := runMoonshotServiceHelper(t,
		relayconstant.MoonshotSearchPath,
		`{"text_query":"deepseek","num_results":2}`,
		upstreamBody,
		constant.ChannelTypeDeepSeek,
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, recorder.Code)

	var answer struct {
		SearchResults []struct {
			Title string `json:"title"`
		} `json:"search_results"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &answer))
	require.Len(t, answer.SearchResults, 2, "num_results must cap the rendered answer")
	assert.Equal(t, "A", answer.SearchResults[0].Title, "the cap keeps the best-ranked results")
}

// A page that cannot be read at all is a failure, not an empty page. Answering
// 200 with an empty body would tell the client the page holds no text, which is a
// different fact and hides the failure.
func TestMoonshotServiceFetchReportsAnUnreachablePage(t *testing.T) {
	initLegacyWebsearchTest(t)

	recorder, settlement, apiErr := runMoonshotServiceHelper(t,
		relayconstant.MoonshotFetchPath,
		`{"url":"https://this-domain-does-not-exist-xyz123.invalid/"}`,
		`{"title":"Example","content":"# Example"}`,
		// An Antigravity channel has no fetch upstream, so the fetch falls through
		// to the in-process extractor, which is the path that has to report a
		// page it could not reach.
		constant.ChannelTypeAntigravity,
	)
	require.NotNil(t, apiErr, "an unreachable page must not be answered as a success")
	assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
	assert.True(t, settlement.refunded, "a failed fetch must release the reservation")
	_ = recorder
}

// A target that answered keeps its own status: a mistyped URL reads as 404 rather
// than as the 502 every other fetch failure used to produce, so a caller tells
// "this page does not exist" from "this host could not be reached".
func TestMoonshotServiceFetchKeepsTheUpstreamStatus(t *testing.T) {
	initLegacyWebsearchTest(t)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(target.Close)
	// The target is a loopback address, so the SSRF-protected client refuses it
	// before the request leaves; this test is about the status the target
	// answered, not about the dialer.
	fetch := system_setting.GetFetchSetting()
	previous := fetch.EnableSSRFProtection
	fetch.EnableSSRFProtection = false
	t.Cleanup(func() { fetch.EnableSSRFProtection = previous })

	_, settlement, apiErr := runMoonshotServiceHelper(t,
		relayconstant.MoonshotFetchPath,
		`{"url":"`+target.URL+`/no-such-page"}`,
		`{"title":"Example","content":"# Example"}`,
		constant.ChannelTypeAntigravity,
	)
	require.NotNil(t, apiErr, "a page the target refused must not be answered as a success")
	assert.Equal(t, http.StatusNotFound, apiErr.StatusCode, "the target's own status must reach the client")
	assert.True(t, settlement.refunded, "a failed fetch must release the reservation")
}

// The client documentation used max_results before it used num_results. A caller
// that followed the earlier name is capped rather than silently ignored.
func TestMoonshotServiceSearchAcceptsTheAliasCount(t *testing.T) {
	initLegacyWebsearchTest(t)

	upstreamBody := `{"results":[` +
		`{"title":"A","url":"https://a.example/","snippet":"sa"},` +
		`{"title":"B","url":"https://b.example/","snippet":"sb"},` +
		`{"title":"C","url":"https://c.example/","snippet":"sc"}]}`
	recorder, _, apiErr := runMoonshotServiceHelper(t,
		relayconstant.MoonshotSearchPath,
		`{"text_query":"deepseek","max_results":2}`,
		upstreamBody,
		constant.ChannelTypeDeepSeek,
	)
	require.Nil(t, apiErr)
	require.Equal(t, http.StatusOK, recorder.Code)

	var answer struct {
		SearchResults []struct {
			Title string `json:"title"`
		} `json:"search_results"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &answer))
	require.Len(t, answer.SearchResults, 2, "max_results must cap the rendered answer")
}
