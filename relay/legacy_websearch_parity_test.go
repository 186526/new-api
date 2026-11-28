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
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// /v1/tools/search and /v1/tools/fetch are served by service/websearch. The
// DeepSeek channel is a native candidate for both contracts, so its request body
// and its answer are forwarded byte for byte; these tests pin that contract on
// the relay handler, which is what a client actually reads.

const toolsSearchPath = "/v1/tools/search"
const toolsFetchPath = "/v1/tools/fetch"

// richToolsSearchAnswer carries every field the upstream documents, the nulls
// it may send, a duplicated URL and a field the upstream does not document.
const richToolsSearchAnswer = `{"results":[` +
	`{"rerank_score":2.4,"title":"A","url":"https://a.example/","snippet":"sa","summary":"sum",` +
	`"content":"body","site_name":null,"site_icon":"https://a.example/favicon.ico","date":null,` +
	`"authority":3,"vendor_field":"only a raw forward keeps this"},` +
	`{"rerank_score":1.5,"title":"A again","url":"https://a.example/","snippet":"dup"}]}`

const richToolsFetchAnswer = `{"title":"Example","content":"# Example\n\nBody.","vendor_field":"kept"}`

func runDeepSeekToolsHelper(t *testing.T, path, body string, upstreamStatus int, upstreamBody string, channelType int) (*httptest.ResponseRecorder, *recordedSettlement, string, string, *types.NewAPIError) {
	t.Helper()
	var gotPath, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Trace", "trace-1")
		w.WriteHeader(upstreamStatus)
		_, _ = io.WriteString(w, upstreamBody)
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	routingName := map[string]string{toolsSearchPath: constant.ToolsSearchModel, toolsFetchPath: constant.ToolsFetchModel}[path]
	common.SetContextKey(c, constant.ContextKeyOriginalModel, routingName)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")

	request, err := helper.GetAndValidateRequest(c, types.RelayFormatDeepSeekTools)
	require.NoError(t, err)
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatDeepSeekTools, request, nil)
	require.NoError(t, err)
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(body)}
	_, err = helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	require.NoError(t, err, "the routing name is priced out of the box")
	settlement := &recordedSettlement{}
	info.Billing = settlement

	apiErr := DeepSeekToolsHelper(c, info)
	return recorder, settlement, gotPath, gotBody, apiErr
}

func initLegacyWebsearchTest(t *testing.T) {
	t.Helper()
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	batch, logConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() { common.BatchUpdateEnabled, common.LogConsumeEnabled = batch, logConsume })
}

// TestDeepSeekToolsHandlerReturnsTheUpstreamAnswerVerbatim requires the body
// the client reads to be the bytes the upstream wrote: the pipeline picks the
// DeepSeek channel as its native candidate, so null members, a duplicated URL,
// an undocumented field and the field order all survive, and the upstream
// headers come with them. A rendered answer cannot satisfy this.
func TestDeepSeekToolsHandlerReturnsTheUpstreamAnswerVerbatim(t *testing.T) {
	initLegacyWebsearchTest(t)

	tests := []struct {
		name        string
		path        string
		body        string
		answer      string
		billable    bool
		settled     int
		forwardedTo string
	}{
		{
			name:        "search keeps every result field and the duplicated url",
			path:        toolsSearchPath,
			body:        `{"query":"deepseek","count":2}`,
			answer:      richToolsSearchAnswer,
			billable:    true,
			settled:     5000,
			forwardedTo: "/v1/tools/search",
		},
		{
			name:        "fetch keeps the undocumented field",
			path:        toolsFetchPath,
			body:        `{"url":"https://a.example/"}`,
			answer:      richToolsFetchAnswer,
			billable:    true,
			settled:     1000,
			forwardedTo: "/v1/tools/fetch",
		},
		{
			name:        "an empty result array is answered as the upstream wrote it",
			path:        toolsSearchPath,
			body:        `{"query":"site:none.invalid x"}`,
			answer:      `{"results":[]}`,
			forwardedTo: "/v1/tools/search",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder, settlement, gotPath, _, apiErr := runDeepSeekToolsHelper(
				t, tc.path, tc.body, http.StatusOK, tc.answer, constant.ChannelTypeDeepSeek)
			require.Nil(t, apiErr)
			assert.Equal(t, tc.forwardedTo, gotPath)
			assert.Equal(t, tc.answer, recorder.Body.String())
			assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
			assert.Equal(t, "trace-1", recorder.Header().Get("X-Upstream-Trace"))
			if tc.billable {
				assert.Equal(t, []int{tc.settled}, settlement.settled)
				assert.False(t, settlement.refunded)
				return
			}
			assert.Empty(t, settlement.settled)
			assert.True(t, settlement.refunded)
		})
	}
}

// TestDeepSeekToolsHandlerForwardsTheRequestBodyVerbatim requires the body the
// upstream receives to be the client's own JSON: a native candidate forwards
// Raw rather than rebuilding a request from the query, so fields the gateway
// does not know and the DeepSeek search options it never parses reach the
// upstream untouched.
func TestDeepSeekToolsHandlerForwardsTheRequestBodyVerbatim(t *testing.T) {
	initLegacyWebsearchTest(t)

	body := `{"query":"deepseek","count":2,"need_content":true,"ori_question":"q","localization":"CN","future_field":{"nested":true}}`
	_, _, _, gotBody, apiErr := runDeepSeekToolsHelper(
		t, toolsSearchPath, body, http.StatusOK, richToolsSearchAnswer, constant.ChannelTypeDeepSeek)
	require.Nil(t, apiErr)
	assert.Equal(t, body, gotBody, "an unknown field and an option the layer does not model must survive")
}

// TestDeepSeekToolsHandlerKeepsAnUpstreamErrorStatus requires an upstream
// refusal to keep its mapped status without retrying a channel that already
// answered.
func TestDeepSeekToolsHandlerKeepsAnUpstreamErrorStatus(t *testing.T) {
	initLegacyWebsearchTest(t)

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"message":"failed to fetch URL","type":"not_found_error","param":null,"code":"invalid_request_error"}}`)
	}))
	t.Cleanup(upstream.Close)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, toolsFetchPath, strings.NewReader(`{"url":"https://a.example/missing"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(constant.ContextKeyChannelStatusCodeMapping), `{"404":422}`)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, constant.ToolsFetchModel)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeDeepSeek)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")

	request, err := helper.GetAndValidateRequest(c, types.RelayFormatDeepSeekTools)
	require.NoError(t, err)
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatDeepSeekTools, request, nil)
	require.NoError(t, err)
	info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(`{"url":"https://a.example/missing"}`)}
	_, err = helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
	require.NoError(t, err)
	info.Billing = &recordedSettlement{}

	apiErr := DeepSeekToolsHelper(c, info)
	require.NotNil(t, apiErr)
	assert.Equal(t, "/v1/tools/fetch", gotPath)
	assert.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode, "the channel status_code_mapping still applies")
	assert.Equal(t, "failed to fetch URL", apiErr.Error())
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Empty(t, recorder.Body.String())
}

func TestWebsearchHandlersRefundRenderedEmptyAnswers(t *testing.T) {
	initLegacyWebsearchTest(t)
	exa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, []string{"/search", "/contents"}, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	t.Cleanup(exa.Close)
	settings := config.GlobalConfig.Get("websearch_setting").(*websearch_setting.WebSearchSetting)
	previous := *settings
	t.Cleanup(func() {
		*settings = previous
		assert.NoError(t, websearch_setting.UpdateAndSync())
	})
	settings.ExaBaseURL = exa.URL
	settings.ExaAPIKey = "test-key"
	settings.CandidateOrder = "exa"
	settings.NativeFirst = false
	require.NoError(t, websearch_setting.UpdateAndSync())

	for _, tc := range []struct {
		path, body, expected string
	}{
		{toolsSearchPath, `{"query":"empty example"}`, `{"results":[]}`},
		{toolsFetchPath, `{"url":"https://a.example/empty"}`, `{"title":"","content":""}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			recorder, settlement, nativePath, _, apiErr := runDeepSeekToolsHelper(t, tc.path, tc.body,
				http.StatusInternalServerError, `{"error":{"message":"unexpected native request"}}`, constant.ChannelTypeDeepSeek)
			require.Nil(t, apiErr)
			assert.Empty(t, nativePath)
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.JSONEq(t, tc.expected, recorder.Body.String())
			assert.Empty(t, settlement.settled)
			assert.Equal(t, 1, settlement.refundCalls)
		})
	}
}
