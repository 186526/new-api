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
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alphaSearchAnswer is the Codex-shaped document /v1/alpha/search forwards. A
// Codex channel is the native candidate for this contract, so the answer the
// client reads is the upstream's own bytes and not a document rendered from the
// abstract answer.
const alphaSearchAnswer = `{"id":"s_1","results":[{"type":"text_result","title":"t","url":"https://a.example/","snippet":"s"}],"future_field":{"nested":true}}`

func TestAlphaSearchHandlerReturnsTheUpstreamAnswerVerbatim(t *testing.T) {
	assertAlphaSearchNativeAnswer(t, alphaSearchAnswer, true)
}

func TestAlphaSearchHandlerRefundsAnEmptyNativeAnswer(t *testing.T) {
	assertAlphaSearchNativeAnswer(t, `{"id":"s_1","results":[],"future_field":true}`, false)
}

func TestAlphaSearchHandlerRefundsAnEmptyRenderedAnswer(t *testing.T) {
	assertAlphaSearchAnswer(t, `{"results":[]}`, false, false)
}

func assertAlphaSearchNativeAnswer(t *testing.T, upstreamBody string, billable bool) {
	t.Helper()
	assertAlphaSearchAnswer(t, upstreamBody, billable, true)
}

func assertAlphaSearchAnswer(t *testing.T, upstreamBody string, billable, native bool) {
	t.Helper()
	initLegacyWebsearchTest(t)

	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, upstreamBody)
	}))
	t.Cleanup(upstream.Close)
	if !native {
		settings := config.GlobalConfig.Get("websearch_setting").(*websearch_setting.WebSearchSetting)
		previous := *settings
		t.Cleanup(func() {
			*settings = previous
			assert.NoError(t, websearch_setting.UpdateAndSync())
		})
		settings.ExaBaseURL = upstream.URL
		settings.ExaAPIKey = "test-key"
		settings.CandidateOrder = "exa"
		settings.NativeFirst = false
		require.NoError(t, websearch_setting.UpdateAndSync())
	}

	body := `{"id":"req_1","model":"gpt-5.1","commands":{"search_query":[{"q":"weather"}]},"future_field":true}`
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-5.1")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCodex)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, `{"access_token":"at","account_id":"acct"}`)

	request, err := helper.GetAndValidateRequest(c, types.RelayFormatOpenAIAlphaSearch)
	require.NoError(t, err)
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAIAlphaSearch, request, nil)
	require.NoError(t, err)
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(body)}
	info.PriceData = hosttypes.PriceData{UsePrice: true, ModelPrice: 0.04, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
	settlement := &recordedSettlement{}
	info.Billing = settlement

	apiErr := AlphaSearchHelper(c, info)
	require.Nil(t, apiErr)
	if native {
		assert.Equal(t, "/backend-api/codex/alpha/search", gotPath)
		assert.Equal(t, body, gotBody, "the Codex request body reaches the upstream untouched")
		assert.Equal(t, upstreamBody, recorder.Body.String(), "the Codex answer reaches the client untouched")
	} else {
		assert.Equal(t, "/search", gotPath)
		assert.JSONEq(t, `{"id":"","results":[]}`, recorder.Body.String())
	}
	if !billable {
		assert.Empty(t, settlement.settled)
		assert.Equal(t, 1, settlement.refundCalls)
		assert.Zero(t, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount)
		return
	}
	require.Len(t, settlement.settled, 1, "the call settles once at the routing name's request price")
	assert.Greater(t, settlement.settled[0], 0)
	assert.False(t, settlement.refunded)
	assert.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount)
}
