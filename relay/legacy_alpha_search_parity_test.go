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
	"github.com/QuantumNous/new-api/service"
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
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	batch, logConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() { common.BatchUpdateEnabled, common.LogConsumeEnabled = batch, logConsume })

	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, alphaSearchAnswer)
	}))
	t.Cleanup(upstream.Close)

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
	assert.Equal(t, "/backend-api/codex/alpha/search", gotPath)
	assert.Equal(t, body, gotBody, "the Codex request body reaches the upstream untouched")
	assert.Equal(t, alphaSearchAnswer, recorder.Body.String(), "the Codex answer reaches the client untouched")
	assert.Len(t, settlement.settled, 1, "the call settles once at the routing name's request price")
	assert.Greater(t, settlement.settled[0], 0)
	assert.False(t, settlement.refunded)
	assert.Equal(t, 1, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount)
}
