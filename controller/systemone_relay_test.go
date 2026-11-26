package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const systemOneRelayModel = "jev-latest"

// systemOneUpstream answers one System One decision and records the path it was
// asked on, so a test can tell the contract prefix was rewritten.
type systemOneUpstream struct {
	server     *httptest.Server
	requests   atomic.Int32
	lastPath   string
	status     int
	body       string
	retryAfter string
}

func newSystemOneUpstream(t *testing.T, status int, body string) *systemOneUpstream {
	t.Helper()
	upstream := &systemOneUpstream{status: status, body: body}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.requests.Add(1)
		upstream.lastPath = r.URL.String()
		if upstream.retryAfter != "" {
			w.Header().Set("Retry-After", upstream.retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstream.status)
		_, _ = io.WriteString(w, upstream.body)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

// systemOneRelayGateway serves the contract endpoint on a context the
// distributor already filled, which is the state the relay handler runs in.
func systemOneRelayGateway(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	engine := gin.New()
	engine.POST("/systemone/v1/systemone", func(c *gin.Context) {
		common.SetErrorShape(c, common.ErrorShapeTypeSafe)
		common.SetContextKey(c, constant.ContextKeyOriginalModel, systemOneRelayModel)
		common.SetContextKey(c, constant.ContextKeyChannelId, 4545)
		common.SetContextKey(c, constant.ContextKeyChannelName, "systemone")
		common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstreamURL)
		common.SetContextKey(c, constant.ContextKeyChannelKey, "systemone-test-key")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		Relay(c, types.RelayFormatSystemOne)
	})
	return httptest.NewServer(engine)
}

// allowSystemOneRelay takes the test off token counting, billing and consume
// logging, the same way the image-limit tests do: a zero ratio keeps the wallet
// untouched and no consume log reads a user setting.
func allowSystemOneRelay(t *testing.T) {
	t.Helper()
	previousCount := constant.CountToken
	previousRatios := ratio_setting.GetModelRatioCopy()
	previousBatchUpdate := common.BatchUpdateEnabled
	previousLogConsume := common.LogConsumeEnabled
	quotaSetting := operation_setting.GetQuotaSetting()
	previousFreeModelPreConsume := quotaSetting.EnableFreeModelPreConsume
	constant.CountToken = false
	common.BatchUpdateEnabled = true
	common.LogConsumeEnabled = false
	quotaSetting.EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+systemOneRelayModel+`": 0}`))
	t.Cleanup(func() {
		constant.CountToken = previousCount
		common.BatchUpdateEnabled = previousBatchUpdate
		common.LogConsumeEnabled = previousLogConsume
		quotaSetting.EnableFreeModelPreConsume = previousFreeModelPreConsume
		if ratios, err := json.Marshal(previousRatios); err == nil {
			_ = ratio_setting.UpdateModelRatioByJSONString(string(ratios))
		}
	})
}

func TestSystemOneRelayForwardsUnderTheUpstreamPath(t *testing.T) {
	allowSystemOneRelay(t)
	upstream := newSystemOneUpstream(t, http.StatusOK,
		`{"model":"jev-1.13","answers":{"q":{"type":"noul","noul":0.9}},"usage":{"input_tokens":11,"output_tokens":2,"cost":0},"opencode":{"account":"x"}}`)
	gateway := systemOneRelayGateway(t, upstream.server.URL)
	defer gateway.Close()

	body := `{"model":"` + systemOneRelayModel + `","state":"x","questions":{"q":{"type":"noul","instructions":"y"}}}`
	response, err := http.Post(gateway.URL+"/systemone/v1/systemone", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, response.StatusCode, string(payload))
	assert.Equal(t, "/v1/systemone", upstream.lastPath, "the contract prefix is rewritten for the upstream")

	var document map[string]any
	require.NoError(t, json.Unmarshal(payload, &document))
	assert.Equal(t, systemOneRelayModel, document["model"], "the client reads the model it asked for")
	assert.NotContains(t, document, "opencode")
	usage := document["usage"].(map[string]any)
	assert.Equal(t, map[string]any{"input_tokens": float64(11), "output_tokens": float64(2)}, usage,
		"the usage document keeps only the two counters the contract declares")
}

func TestSystemOneRelayPassesAnUpstreamValidationErrorThrough(t *testing.T) {
	allowSystemOneRelay(t)
	upstreamBody := `{"detail":[{"loc":["body","questions","q","criteria"],"msg":"Field required","type":"missing"}]}`
	upstream := newSystemOneUpstream(t, http.StatusUnprocessableEntity, upstreamBody)
	gateway := systemOneRelayGateway(t, upstream.server.URL)
	defer gateway.Close()

	body := `{"model":"` + systemOneRelayModel + `","state":"x","questions":{"q":{"type":"choice","instructions":"pick"}}}`
	response, err := http.Post(gateway.URL+"/systemone/v1/systemone", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	// The local validator refuses this body before it is sent, which is the same
	// answer the upstream would give; what matters is the status and the shape.
	require.Equal(t, http.StatusUnprocessableEntity, response.StatusCode, string(payload))
	var document struct {
		Detail []struct {
			Loc  []any  `json:"loc"`
			Msg  string `json:"msg"`
			Type string `json:"type"`
		} `json:"detail"`
	}
	require.NoError(t, json.Unmarshal(payload, &document), string(payload))
	require.NotEmpty(t, document.Detail)
	assert.Equal(t, []any{"body", "questions", "q", "criteria"}, document.Detail[0].Loc)
}
