package controller

import (
	"encoding/json"
	"io"
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

const imageLimitRelayChannelId = 4343

// imageLimitUpstream answers every attempt with a fixed chat completion and
// counts the attempts, so a test can tell that the request reached it.
type imageLimitUpstream struct {
	server   *httptest.Server
	attempts atomic.Int32
}

func newImageLimitUpstream(t *testing.T) *imageLimitUpstream {
	t.Helper()
	upstream := &imageLimitUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"img-limit","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

// TestRelayRefusesTheImagePastTheChannelLimit reproduces the production
// request: the distributor already selected the channel, so the relay path
// holds a stub without settings. A request with one image more than the
// channel allows must be refused locally instead of being sent upstream.
func TestRelayRefusesTheImagePastTheChannelLimit(t *testing.T) {
	cleanup := allowImageLimitRelayModel(t)
	defer cleanup()

	upstream := newImageLimitUpstream(t)
	gateway := imageLimitRelayGateway(t, upstream.server.URL, 20)
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
	assert.Equal(t, int32(0), upstream.attempts.Load(), "the request never reaches the upstream")
}

// TestRelaySendsTheImagesWithinTheChannelLimit covers the allowing side of the
// same path: at exactly the limit the request goes out, so the client gets the
// upstream's own answer. A local refusal and an upstream error are told apart
// by the answer body, not only by the status. This test cannot tell the limit
// apart from a zero limit: both let the request out. The limit the check reads
// is asserted in TestRelayRefusesTheImagesPastALowerChannelLimit and in the
// relay package's unit tests.
func TestRelaySendsTheImagesWithinTheChannelLimit(t *testing.T) {
	cleanup := allowImageLimitRelayModel(t)
	defer cleanup()

	upstream := newImageLimitUpstream(t)
	gateway := imageLimitRelayGateway(t, upstream.server.URL, 20)
	defer gateway.Close()

	response, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(imageLimitRelayBody(t, 20)))
	require.NoError(t, err)
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, "response body: %s", payload)
	assert.Equal(t, int32(1), upstream.attempts.Load(), "20 images reach the upstream exactly once")
	var body struct {
		ID      string `json:"id"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	require.NoError(t, json.Unmarshal(payload, &body))
	assert.Equal(t, "img-limit", body.ID, "the client gets the upstream's answer")
	require.Len(t, body.Choices, 1)
	assert.Equal(t, "ok", body.Choices[0].Message.Content)
}

// TestRelayRefusesTheImagesPastALowerChannelLimit proves the configured value
// reaches the check on the real relay path: the same 20-image request is sent,
// the channel limit is 19, and the refusal names 19. The request would pass a
// zero limit and a limit of 20, so this is the case that tells the limit apart.
func TestRelayRefusesTheImagesPastALowerChannelLimit(t *testing.T) {
	cleanup := allowImageLimitRelayModel(t)
	defer cleanup()

	upstream := newImageLimitUpstream(t)
	gateway := imageLimitRelayGateway(t, upstream.server.URL, 19)
	defer gateway.Close()

	response, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(imageLimitRelayBody(t, 20)))
	require.NoError(t, err)
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode, "response body: %s", payload)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(payload, &body))
	assert.Equal(t, "context_length_exceeded", body.Error.Code)
	assert.Contains(t, body.Error.Message, "Total number of images (20) exceeds the maximum allowed of 19")
	assert.Equal(t, int32(0), upstream.attempts.Load(), "the request never reaches the upstream")
}

// allowImageLimitRelayModel takes the test off token counting, billing and
// consume logging: the model has a zero ratio, so the relay reaches the image
// check without a wallet pre-consume on a database user, quota settlement
// accumulates in memory (BatchUpdateEnabled) instead of writing to a database
// the test does not open, and no consume log is recorded, which would read the
// user setting through the user cache. Every setting is restored when the test
// ends.
func allowImageLimitRelayModel(t *testing.T) func() {
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
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"mistral-large-latest": 0}`))
	return func() {
		constant.CountToken = previousCount
		common.BatchUpdateEnabled = previousBatchUpdate
		common.LogConsumeEnabled = previousLogConsume
		quotaSetting.EnableFreeModelPreConsume = previousFreeModelPreConsume
		ratios, err := json.Marshal(previousRatios)
		if err == nil {
			_ = ratio_setting.UpdateModelRatioByJSONString(string(ratios))
		}
	}
}

// imageLimitRelayGateway serves Relay on the context the distributor filled for
// a selected channel, which is the path whose channel object is a stub. The
// channel base URL points at the test upstream, so a request that passes the
// image check really leaves the gateway.
func imageLimitRelayGateway(t *testing.T, upstreamURL string, imageLimit int) *httptest.Server {
	t.Helper()
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		selectDistributorChannel(c, upstreamURL, imageLimit)
		Relay(c, types.RelayFormatOpenAI)
	})
	return httptest.NewServer(engine)
}

func selectDistributorChannel(c *gin.Context, upstreamURL string, imageLimit int) {
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "mistral-large-latest")
	common.SetContextKey(c, constant.ContextKeyChannelId, imageLimitRelayChannelId)
	common.SetContextKey(c, constant.ContextKeyChannelName, "mistral")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMistral)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{MaxImagesPerRequest: imageLimit})
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstreamURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "image-limit-test-key")
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, true)
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
