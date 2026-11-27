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
