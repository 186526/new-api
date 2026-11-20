package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRelayRefusesTheImagePastTheChannelLimit reproduces the production
// request: the distributor already selected the channel, so the relay path
// holds a stub without settings, and the channel metadata carries the limit.
// A request with one image more than the channel allows must be refused
// locally instead of being sent upstream.
func TestRelayRefusesTheImagePastTheChannelLimit(t *testing.T) {
	cleanup := allowImageLimitRelayModel(t)
	defer cleanup()

	var upstreamAttempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAttempts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	gateway := imageLimitRelayGateway()
	defer gateway.Close()

	response, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(imageLimitRelayBody(t, 21)))
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
	assert.Equal(t, "context_length_exceeded", body.Error.Code)
	assert.Contains(t, body.Error.Message, "Total number of images (21) exceeds the maximum allowed of 20")
	assert.Equal(t, int32(0), upstreamAttempts.Load(), "the request never reaches the upstream")
}

// TestRelaySendsTheImagesWithinTheChannelLimit covers the other side of the
// same path: at exactly the limit the local check lets the request through.
func TestRelaySendsTheImagesWithinTheChannelLimit(t *testing.T) {
	cleanup := allowImageLimitRelayModel(t)
	defer cleanup()

	gateway := imageLimitRelayGateway()
	defer gateway.Close()

	response, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(imageLimitRelayBody(t, 20)))
	require.NoError(t, err)
	defer response.Body.Close()
	require.NotEqual(t, http.StatusBadRequest, response.StatusCode)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(response.Body).Decode(&body)
	assert.NotEqual(t, "context_length_exceeded", body.Error.Code, "20 images stay within the limit of 20")
}

// allowImageLimitRelayModel takes the test off token counting and billing: the
// model has a zero ratio, so the relay reaches the image check without a wallet
// pre-consume on a database user. Both settings are restored when the test ends.
func allowImageLimitRelayModel(t *testing.T) func() {
	t.Helper()
	previousCount := constant.CountToken
	previousRatios := ratio_setting.GetModelRatioCopy()
	quotaSetting := operation_setting.GetQuotaSetting()
	previousFreeModelPreConsume := quotaSetting.EnableFreeModelPreConsume
	constant.CountToken = false
	quotaSetting.EnableFreeModelPreConsume = false
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"mistral-large-latest": 0}`))
	return func() {
		constant.CountToken = previousCount
		quotaSetting.EnableFreeModelPreConsume = previousFreeModelPreConsume
		ratios, err := json.Marshal(previousRatios)
		if err == nil {
			_ = ratio_setting.UpdateModelRatioByJSONString(string(ratios))
		}
	}
}

// imageLimitRelayGateway serves Relay on the context the distributor filled for
// a selected channel, which is the path that holds a stub channel.
func imageLimitRelayGateway() *httptest.Server {
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		selectDistributorChannel(c)
		Relay(c, types.RelayFormatOpenAI)
	})
	return httptest.NewServer(engine)
}

func selectDistributorChannel(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "mistral-large-latest")
	common.SetContextKey(c, constant.ContextKeyChannelId, 4343)
	common.SetContextKey(c, constant.ContextKeyChannelName, "mistral")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMistral)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{MaxImagesPerRequest: 20})
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
}

func imageLimitRelayBody(t *testing.T, images int) string {
	t.Helper()
	parts := make([]map[string]any, 0, images+1)
	parts = append(parts, map[string]any{"type": "text", "text": "look"})
	for i := 0; i < images; i++ {
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": "data:image/png;base64," + strconv.Itoa(i)},
		})
	}
	body, err := json.Marshal(map[string]any{
		"model":    "mistral-large-latest",
		"messages": []map[string]any{{"role": "user", "content": parts}},
	})
	require.NoError(t, err)
	return string(body)
}
