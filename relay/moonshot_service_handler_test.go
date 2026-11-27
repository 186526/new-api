package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runMoonshotServiceHelper drives one Kimi Code request against a stub upstream
// the way the router does: the channel context the distributor would have
// written, the same validation, the same price lookup, and a settlement stub the
// test reads afterwards.
func runMoonshotServiceHelper(t *testing.T, path, body string, upstreamBody string, channelType int) (*httptest.ResponseRecorder, *recordedSettlement, *types.NewAPIError) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
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

	request, err := helper.GetAndValidateRequest(c, types.RelayFormatMoonshotService)
	require.NoError(t, err)
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatMoonshotService, request, nil)
	require.NoError(t, err)
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(body)}
	_, err = helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	require.NoError(t, err, "the routing name is priced out of the box")
	settlement := &recordedSettlement{}
	info.Billing = settlement

	apiErr := MoonshotServiceHelper(c, info)
	return recorder, settlement, apiErr
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
