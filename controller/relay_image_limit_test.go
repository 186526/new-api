package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
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
	assertImageOverflowWording(t, body.Error.Message)
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
	assertImageOverflowWording(t, body.Error.Message)
	assert.Equal(t, int32(0), upstream.attempts.Load(), "the request never reaches the upstream")
}

// TestRelayRefusesTheImagePastTheModelLimit covers the measured Mistral
// channel: one channel-wide limit of 20 serves two upstream models, one of
// them limiting a request to 8. The client asks for the alias and the channel
// maps it to the upstream name, so the per-model limit must be found through
// the mapping the distributor stored.
func TestRelayRefusesTheImagePastTheModelLimit(t *testing.T) {

	cleanup := allowImageLimitRelayModel(t)
	defer cleanup()

	upstream := newImageLimitUpstream(t)
	channel := imageLimitRelayChannel{Limit: 20, ModelLimits: map[string]int{imageLimitMappedUpstreamModel: 8}}
	gateway := imageLimitRelayGatewayWithModel(t, upstream.server.URL, channel, imageLimitMappedModelAlias, imageLimitModelMapping())
	defer gateway.Close()

	refused, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(imageLimitRelayBodyForModel(t, 9, imageLimitMappedModelAlias)))
	require.NoError(t, err)
	defer refused.Body.Close()

	payload, err := io.ReadAll(refused.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, refused.StatusCode, "response body: %s", payload)
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(payload, &body))
	assert.Equal(t, "context_length_exceeded", body.Error.Code)
	assert.Contains(t, body.Error.Message, "Total number of images (9) exceeds the maximum allowed of 8")
	assertImageOverflowWording(t, body.Error.Message)
	assert.Equal(t, int32(0), upstream.attempts.Load(), "the channel-wide 20 never lets the request out")

	// 21 images pass the model limit and the channel limit both, so the
	// channel-wide value is still read where no model limit applies.
	sent, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(imageLimitRelayBodyForModel(t, 21, "unlisted-model")))
	require.NoError(t, err)
	defer sent.Body.Close()
	payload, err = io.ReadAll(sent.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, sent.StatusCode, "response body: %s", payload)
	require.NoError(t, json.Unmarshal(payload, &body))
	assert.Contains(t, body.Error.Message, "Total number of images (21) exceeds the maximum allowed of 20")
	assertImageOverflowWording(t, body.Error.Message)
	assert.Equal(t, int32(0), upstream.attempts.Load())
}

// TestRelayRefusesARequestTheModelMappingCannotResolve covers a channel whose
// model_mapping forms a cycle. The relay would fail such a request with
// model_mapping_contains_cycle after its images passed the channel-wide 20, so
// the image check must refuse it first and never let it leave the gateway.
func TestRelayRefusesARequestTheModelMappingCannotResolve(t *testing.T) {
	cleanup := allowImageLimitRelayModel(t)
	defer cleanup()

	upstream := newImageLimitUpstream(t)
	channel := imageLimitRelayChannel{Limit: 20, ModelLimits: map[string]int{imageLimitMappedUpstreamModel: 8}}
	cycle := `{"` + imageLimitMappedModelAlias + `":"` + imageLimitMappedUpstreamModel + `","` + imageLimitMappedUpstreamModel + `":"` + imageLimitMappedModelAlias + `"}`
	gateway := imageLimitRelayGatewayWithModel(t, upstream.server.URL, channel, imageLimitMappedModelAlias, cycle)
	defer gateway.Close()

	response, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(imageLimitRelayBodyForModel(t, 9, imageLimitMappedModelAlias)))
	require.NoError(t, err)
	defer response.Body.Close()

	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode, "response body: %s", payload)
	assert.Contains(t, string(payload), "model_mapping_contains_cycle")
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
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(
		`{"mistral-large-latest": 0, "`+imageLimitMappedModelAlias+`": 0, "unlisted-model": 0}`))
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
	selectDistributorChannelOfType(c, upstreamURL, imageLimit, constant.ChannelTypeMistral)
}

func selectDistributorChannelOfType(c *gin.Context, upstreamURL string, imageLimit int, channelType int) {
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "mistral-large-latest")
	common.SetContextKey(c, constant.ContextKeyChannelId, imageLimitRelayChannelId)
	common.SetContextKey(c, constant.ContextKeyChannelName, "mistral")
	common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{MaxImagesPerRequest: imageLimit})
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstreamURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "image-limit-test-key")
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, true)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
}

const (
	imageLimitMappedModelAlias    = "glm-5.3-flash"
	imageLimitMappedUpstreamModel = "zai-glm-5-3-flash"
)

func imageLimitModelMapping() string {
	return `{"` + imageLimitMappedModelAlias + `":"` + imageLimitMappedUpstreamModel + `"}`
}

// imageLimitRelayChannel is the channel settings the distributor stores for
// the selected channel.
type imageLimitRelayChannel struct {
	Limit       int
	ModelLimits map[string]int
}

func imageLimitRelayGatewayWithModel(t *testing.T, upstreamURL string, channel imageLimitRelayChannel, firstModel string, modelMapping string) *httptest.Server {
	t.Helper()
	engine := gin.New()
	engine.POST("/v1/chat/completions", func(c *gin.Context) {
		model := firstModel
		if requested := parseBodyModel(c); requested != "" {
			model = requested
		}
		selectDistributorChannelWithModel(c, upstreamURL, channel, model, modelMapping)
		Relay(c, types.RelayFormatOpenAI)
	})
	return httptest.NewServer(engine)
}

// parseBodyModel reads the model of the request the way the distributor's
// own model lookup sees it, which is what the client asked for.
func parseBodyModel(c *gin.Context) string {
	if c.Request == nil || c.Request.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	return body.Model
}

func selectDistributorChannelWithModel(c *gin.Context, upstreamURL string, channel imageLimitRelayChannel, model string, modelMapping string) {
	common.SetContextKey(c, constant.ContextKeyOriginalModel, model)
	common.SetContextKey(c, constant.ContextKeyChannelId, imageLimitRelayChannelId)
	common.SetContextKey(c, constant.ContextKeyChannelName, "mistral")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeMistral)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{
		MaxImagesPerRequest: channel.Limit,
		MaxImagesPerModel:   channel.ModelLimits,
	})
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, modelMapping)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstreamURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "image-limit-test-key")
	common.SetContextKey(c, constant.ContextKeyChannelAutoBan, true)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
}

func imageLimitRelayBodyForModel(t *testing.T, images int, model string) string {
	t.Helper()
	return imageLimitRelayBodyWithModel(t, images, model)
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
	return imageLimitRelayBodyWithModel(t, images, "mistral-large-latest")
}

func imageLimitRelayBodyWithModel(t *testing.T, images int, model string) string {
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
		"model":    model,
		"messages": []map[string]any{{"role": "user", "content": parts}},
	})
	require.NoError(t, err)
	return string(body)
}

// imageCeilingReply is the 400 measured on api.mistral.ai for the ninth image
// of a model that allows 8.
const imageCeilingReply = `{"object":"error","message":"Total number of images exceeds the maximum allowed of 8.","type":"invalid_request_invalid_args","code":"3051"}`

// commandCodeTurnCeilingMessage is the message measured on Command Code, the
// model deepseek-v4.1-flash-dedicated behind channels 3 and 4, for a request
// whose current turn holds 20 images. The ceiling counts the images after the
// last assistant message, so it is per turn rather than per request.
const commandCodeTurnCeilingMessage = "This turn adds 20 images (the images after the last assistant message); " +
	"deepseek-v4.1-flash-dedicated takes at most 8 new images per turn. " +
	"Images earlier in the conversation do not count."

// commandCodeTurnCeilingReply is that refusal as the upstream sends it: a 400
// whose error object carries the message, the type, the code and the param.
const commandCodeTurnCeilingReply = `{"error":{"message":"This turn adds 20 images (the images after the last assistant message); deepseek-v4.1-flash-dedicated takes at most 8 new images per turn. Images earlier in the conversation do not count.","type":"invalid_request_error","code":"bad_request","param":"messages"}}`

// imageCeilingHint is what every image refusal tells the client to do, whether
// new-api refused the request itself or translated the upstream's refusal.
const imageCeilingHint = "Request a smaller batch of images or compact the conversation and retry."

// imageCeilingOverflowHint is the closing sentence of a refusal, appended by
// imageLimitMessage. It is checked for its count so a refusal translated twice
// is caught.
const imageCeilingOverflowHint = "Request exceeds the context length limit; compact the conversation before retrying it."

// requireImageCeilingMessageToldOnce requires the message of a refusal to carry
// each hint exactly once and the upstream envelope nowhere: an adaptor and the
// retry loop both translate a refusal, and a translation that is not idempotent
// appends both hints a second time.
func requireImageCeilingMessageToldOnce(t *testing.T, message string) {
	t.Helper()
	assert.Equal(t, 1, strings.Count(message, imageCeilingHint), "the retry hint is told once: %s", message)
	assert.Equal(t, 1, strings.Count(message, imageCeilingOverflowHint), "the overflow hint is told once: %s", message)
	assert.NotContains(t, message, `"object":"error"`, "the upstream envelope stays out of the message: %s", message)
}

// imageCeilingWording is the phrase that makes a client read a 400 as a context
// overflow. Kimi Code 2.4.1 on Chat Completions matches its message against
// /context[ _-]?length/i and never reads error.code, so every image refusal has
// to carry this phrase next to the code.
var imageCeilingWording = regexp.MustCompile(`(?i)context[ _-]?length`)

// assertImageOverflowWording checks the body a client reads carries the phrase
// it classifies a context overflow by.
func assertImageOverflowWording(t *testing.T, message string) {
	t.Helper()
	assert.Regexp(t, imageCeilingWording, message, "the client finds the context length limit in the message it reads")
}

// imageCeilingUpstream answers every attempt with a fixed status and body and
// counts the attempts, so a test can tell how many times the upstream was
// called and whether a retry happened.
type imageCeilingUpstream struct {
	server   *httptest.Server
	attempts atomic.Int32
}

func newImageCeilingUpstream(t *testing.T, statusCode int, body string) *imageCeilingUpstream {
	t.Helper()
	upstream := &imageCeilingUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

// imageCeilingGateway serves Relay on the context the distributor filled for a
// selected channel, with the channel base URL pointing at the test upstream, so
// the request really leaves the gateway. The channel allows no local image
// limit, which is how a 400 for the image count reaches the client today.
func imageCeilingGateway(t *testing.T, upstreamURL string, relayFormat types.RelayFormat) *httptest.Server {
	t.Helper()
	path := "/v1/chat/completions"
	channelType := constant.ChannelTypeMistral
	if relayFormat == types.RelayFormatClaude {
		// The Mistral adaptor of this fork has no Claude request conversion, so
		// the Claude envelope is exercised on a channel that does.
		path, channelType = "/v1/messages", constant.ChannelTypeAnthropic
	}
	engine := gin.New()
	engine.POST(path, func(c *gin.Context) {
		selectDistributorChannel(c, upstreamURL, 0)
		common.SetContextKey(c, constant.ContextKeyChannelType, channelType)
		Relay(c, relayFormat)
	})
	return httptest.NewServer(engine)
}

func postImageCeiling(t *testing.T, gatewayURL string, relayFormat types.RelayFormat, body string) (int, []byte) {
	t.Helper()
	path := "/v1/chat/completions"
	if relayFormat == types.RelayFormatClaude {
		path = "/v1/messages"
	}
	response, err := http.Post(gatewayURL+path, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, payload
}

// imageCeilingClaudeBody asks for a Claude reply with the given number of
// image blocks, which is the request the relay counts for the image limit.
func imageCeilingClaudeBody(t *testing.T, images int) string {
	t.Helper()
	blocks := make([]map[string]any, 0, images+1)
	blocks = append(blocks, map[string]any{"type": "text", "text": "look"})
	for i := 0; i < images; i++ {
		blocks = append(blocks, map[string]any{
			"type":   "image",
			"source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGVsbG8="},
		})
	}
	body, err := json.Marshal(map[string]any{
		"model":      "mistral-large-latest",
		"max_tokens": 16,
		"messages":   []map[string]any{{"role": "user", "content": blocks}},
	})
	require.NoError(t, err)
	return string(body)
}

// allowImageCeilingRelay takes the test off token counting, billing and consume
// logging the same way allowImageLimitRelayModel does, and gives the relay a
// retry budget: common.RetryTimes defaults to zero, so without it a retry could
// never happen and the tests could not tell a translated error from a retried
// one. The channel health setting is the production one, so the disable
// decision is read as the relay makes it.
func allowImageCeilingRelay(t *testing.T) func() {
	t.Helper()
	cleanup := allowImageLimitRelayModel(t)
	previousRetryTimes := common.RetryTimes
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.RetryTimes = 2
	common.AutomaticDisableChannelEnabled = true
	return func() {
		common.RetryTimes = previousRetryTimes
		common.AutomaticDisableChannelEnabled = previousAutoDisable
		cleanup()
	}
}

// TestRelayTranslatesAnUpstreamImageCeiling covers a channel whose local limit
// lets the request out and whose upstream refuses it for the image count. The
// client must read the reply as a context overflow, the attempt must be
// counted once, the request must not be retried on another channel, and the
// channel must be judged by the translated error. The second case is the
// per-turn ceiling measured on Command Code behind channels 3 and 4.
func TestRelayTranslatesAnUpstreamImageCeiling(t *testing.T) {
	for _, tc := range []struct {
		name        string
		relayFormat types.RelayFormat
		reply       string
		ceiling     string
		body        func(t *testing.T) string
	}{
		{
			name: "openai", relayFormat: types.RelayFormatOpenAI, reply: imageCeilingReply,
			ceiling: "Total number of images exceeds the maximum allowed of 8.",
			body:    func(t *testing.T) string { return imageLimitRelayBody(t, 9) },
		},
		{
			name: "claude", relayFormat: types.RelayFormatClaude, reply: imageCeilingReply,
			ceiling: "Total number of images exceeds the maximum allowed of 8.",
			body:    func(t *testing.T) string { return imageCeilingClaudeBody(t, 9) },
		},
		{
			name: "command code per turn", relayFormat: types.RelayFormatOpenAI,
			reply: commandCodeTurnCeilingReply, ceiling: "takes at most 8 new images per turn",
			body: func(t *testing.T) string { return imageLimitRelayBodyForModel(t, 20, "deepseek-v4.1-flash-dedicated") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := allowImageCeilingRelay(t)
			defer cleanup()

			upstream := newImageCeilingUpstream(t, http.StatusBadRequest, tc.reply)
			gateway := imageCeilingGateway(t, upstream.server.URL, tc.relayFormat)
			defer gateway.Close()

			statusCode, payload := postImageCeiling(t, gateway.URL, tc.relayFormat, tc.body(t))
			code, message := imageCeilingEnvelope(t, tc.relayFormat, statusCode, payload)

			assert.Equal(t, http.StatusBadRequest, statusCode, "response body: %s", payload)
			assert.Equal(t, "context_length_exceeded", code)
			assert.Contains(t, message, tc.ceiling, "the ceiling the upstream named reaches the client")
			assert.Contains(t, message, imageCeilingHint, "the client is told what to do about the ceiling")
			assertImageOverflowWording(t, message)
			assert.Equal(t, int32(1), upstream.attempts.Load(), "the upstream is attempted once and never retried")
		})
	}
}

// imageCeilingGeminiChunk is the native Gemini event the upstream of the
// last case delivers before it refuses the request inside the same stream.
const imageCeilingGeminiChunk = `{"candidates":[{"content":{"role":"model","parts":[{"text":"look"}]}}]}`

// geminiImageCeilingPaths names the ways a native Gemini upstream can answer an
// image refusal: a JSON 400, and a 200 event stream that opens with the refusal
// or delivers one event before it. The last two commit the response headers
// before the refusal is readable, which is the case the stream cannot report a
// status code on.
var geminiImageCeilingPaths = []struct {
	name string
	path string
	// reply is how the upstream answers: a JSON body of the given status, an
	// event stream that opens with the refusal, or an event stream that
	// carries an event before it.
	reply geminiRefusalReply
}{
	{name: "generateContent", path: "/v1beta/models/gemini-ceiling-test:generateContent", reply: geminiRefusalJSON},
	{name: "streamGenerateContent refused before the first event", path: "/v1beta/models/gemini-ceiling-test:streamGenerateContent?alt=sse", reply: geminiRefusalAsFirstEvent},
	{name: "streamGenerateContent refused after an event", path: "/v1beta/models/gemini-ceiling-test:streamGenerateContent?alt=sse", reply: geminiRefusalAfterAnEvent},
}

// geminiRefusalReply is how a test upstream answers a Gemini request it
// refuses for the image count.
type geminiRefusalReply int

const (
	// geminiRefusalJSON is a 400 whose body is the refusal, the answer of an
	// upstream that refuses before the stream starts.
	geminiRefusalJSON geminiRefusalReply = iota
	// geminiRefusalAsFirstEvent is a 200 event stream whose first event is the
	// refusal.
	geminiRefusalAsFirstEvent
	// geminiRefusalAfterAnEvent is a 200 event stream that delivers one event
	// and then the refusal.
	geminiRefusalAfterAnEvent
)

// TestRelayTranslatesAnUpstreamImageCeilingForGemini covers a Gemini channel on
// every path an image refusal arrives on. The client reads the context
// overflow the other formats read, on the JSON 400 and on the refusal a stream
// carries before its first chunk and after it, and the upstream is attempted
// once on every path.
func TestRelayTranslatesAnUpstreamImageCeilingForGemini(t *testing.T) {
	for _, tc := range geminiImageCeilingPaths {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := allowImageCeilingRelay(t)
			defer cleanup()
			previousTimeout := constant.StreamingTimeout
			constant.StreamingTimeout = 30
			defer func() { constant.StreamingTimeout = previousTimeout }()

			var upstream *imageCeilingUpstream
			switch tc.reply {
			case geminiRefusalAsFirstEvent:
				upstream = newGeminiStreamUpstream(t, func(w http.ResponseWriter, flush func()) {
					_, _ = io.WriteString(w, geminiStreamEvent(imageCeilingReply))
					flush()
				})
			case geminiRefusalAfterAnEvent:
				upstream = newStreamImageCeilingUpstream(t, imageCeilingReply)
			default:
				upstream = newImageCeilingUpstream(t, http.StatusBadRequest, imageCeilingReply)
			}
			defer upstream.server.Close()
			gateway := imageCeilingGeminiGateway(t, upstream.server.URL)
			defer gateway.Close()

			statusCode, payload := postGeminiImageCeiling(t, gateway.URL, tc.path)

			// A stream that already delivered an event keeps its 200: the
			// refusal follows that event as the error event of the relay. A
			// stream whose first event is the refusal never reached the client,
			// so the refusal keeps the 400 it was refused with.
			expectedStatus := http.StatusBadRequest
			if tc.reply == geminiRefusalAfterAnEvent {
				expectedStatus = http.StatusOK
			}
			require.Equal(t, expectedStatus, statusCode, "response body: %s", payload)
			code, message := geminiImageCeilingError(t, tc.reply, payload)
			assert.Equal(t, "context_length_exceeded", code)
			assert.Contains(t, message, "Total number of images exceeds the maximum allowed of 8.")
			assert.Contains(t, message, imageCeilingHint, "the client is told what to do about the ceiling")
			assertImageOverflowWording(t, message)
			requireImageCeilingMessageToldOnce(t, message)
			assert.Equal(t, int32(1), upstream.attempts.Load(), "the upstream is attempted once and never retried")
		})
	}
}

// newStreamImageCeilingUpstream answers with a 200 event stream that delivers
// one event and then the refusal, which is how an upstream that already
// committed the stream refuses a request.
func newStreamImageCeilingUpstream(t *testing.T, refusal string) *imageCeilingUpstream {
	t.Helper()
	upstream := &imageCeilingUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+imageCeilingGeminiChunk+"\n\n")
		_, _ = io.WriteString(w, "data: "+refusal+"\n\n")
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

// imageCeilingGeminiGateway serves Relay on a Gemini channel whose base URL is
// the test upstream.
func imageCeilingGeminiGateway(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	engine := gin.New()
	engine.POST("/v1beta/models/:model", func(c *gin.Context) {
		selectDistributorChannelOfType(c, upstreamURL, 0, constant.ChannelTypeGemini)
		Relay(c, types.RelayFormatGemini)
	})
	return httptest.NewServer(engine)
}

func postGeminiImageCeiling(t *testing.T, gatewayURL string, path string) (int, []byte) {
	t.Helper()
	response, err := http.Post(gatewayURL+path, "application/json", strings.NewReader(imageCeilingGeminiBody))
	require.NoError(t, err)
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, payload
}

// TestRelayKeepsANonImageGeminiStreamError is the counter-case next to the
// image ceiling: a stream that fails for another reason keeps the error it was
// raised as, so the rewrite only reaches the refusal it recognizes.
func TestRelayKeepsANonImageGeminiStreamError(t *testing.T) {
	cleanup := allowImageCeilingRelay(t)
	defer cleanup()

	upstream := newImageCeilingUpstream(t, http.StatusBadRequest,
		`{"error":{"message":"Invalid model: gemini-unknown","type":"invalid_request_error","code":"1002"}}`)
	defer upstream.server.Close()
	gateway := imageCeilingGeminiGateway(t, upstream.server.URL)
	defer gateway.Close()

	statusCode, payload := postGeminiImageCeiling(t, gateway.URL,
		"/v1beta/models/gemini-ceiling-test:streamGenerateContent?alt=sse")

	code, message := imageCeilingEnvelope(t, types.RelayFormatOpenAI, statusCode, payload)
	assert.NotEqual(t, "context_length_exceeded", code, "an error of another kind keeps its own code: %s", payload)
	assert.Equal(t, "1002", code)
	assert.Equal(t, "Invalid model: gemini-unknown", message)
	assert.Equal(t, int32(1), upstream.attempts.Load(), "the upstream is attempted once and never retried")
}

// TestRelayKeepsAGeminiStreamThatOpensWithAChunk covers the stream the
// detection reads ahead of: an upstream that answers with events instead of a
// refusal is streamed as before, every event reaching the client.
func TestRelayKeepsAGeminiStreamThatOpensWithAChunk(t *testing.T) {
	cleanup := allowImageCeilingRelay(t)
	defer cleanup()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = previousTimeout }()

	upstream := newImageCeilingUpstream(t, http.StatusOK,
		"data: "+imageCeilingGeminiChunk+"\n\n"+
			"data: "+`{"candidates":[{"content":{"role":"model","parts":[{"text":" done"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2,"totalTokenCount":5}}`+"\n\n")
	defer upstream.server.Close()
	gateway := imageCeilingGeminiGateway(t, upstream.server.URL)
	defer gateway.Close()

	statusCode, payload := postGeminiImageCeiling(t, gateway.URL,
		"/v1beta/models/gemini-ceiling-test:streamGenerateContent?alt=sse")

	require.Equal(t, http.StatusOK, statusCode, "response body: %s", payload)
	assert.NotContains(t, string(payload), `"error"`, "the events are streamed, not refused")
	assert.Contains(t, string(payload), `"look"`, "the event delivered before the detection still reaches the client")
	assert.Contains(t, string(payload), `" done"`)
}

// geminiStreamEvent is one native Gemini event as the upstream writes it.
func geminiStreamEvent(payload string) string {
	return "data: " + payload + "\n\n"
}

// geminiTextChunk is a native Gemini event carrying the given text.
func geminiTextChunk(text string) string {
	return `{"candidates":[{"content":{"role":"model","parts":[{"text":"` + text + `"}]}}]}`
}

// geminiStreamUpstream answers with an event stream written by handle and
// counts the attempts. handle receives a flusher, so a case can control when
// each event reaches the gateway.
func newGeminiStreamUpstream(t *testing.T, handle func(w http.ResponseWriter, flush func())) *imageCeilingUpstream {
	t.Helper()
	upstream := &imageCeilingUpstream{}
	upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstream.attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flush := func() {
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		flush()
		handle(w, flush)
	}))
	t.Cleanup(upstream.server.Close)
	return upstream
}

// TestRelayStreamsGeminiEventsAsTheyArrive is the case the look-ahead must not
// break: an upstream that delivers its events one at a time, with a gap
// between them. The first event reaches the client before the upstream writes
// the second, so the look-ahead stops at the first complete event instead of
// waiting for the stream to end.
func TestRelayStreamsGeminiEventsAsTheyArrive(t *testing.T) {
	cleanup := allowImageCeilingRelay(t)
	defer cleanup()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = previousTimeout }()

	release := make(chan struct{})
	upstream := newGeminiStreamUpstream(t, func(w http.ResponseWriter, flush func()) {
		_, _ = io.WriteString(w, geminiStreamEvent(geminiTextChunk("first")))
		flush()
		<-release
		_, _ = io.WriteString(w, geminiStreamEvent(geminiTextChunk("second")))
		flush()
	})
	defer upstream.server.Close()
	gateway := imageCeilingGeminiGateway(t, upstream.server.URL)
	defer gateway.Close()
	defer close(release)

	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Post(gateway.URL+"/v1beta/models/gemini-ceiling-test:streamGenerateContent?alt=sse",
		"application/json", strings.NewReader(imageCeilingGeminiBody))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	firstEvent := make(chan string, 1)
	go func() {
		buffer := make([]byte, 4096)
		n, _ := response.Body.Read(buffer)
		firstEvent <- string(buffer[:n])
	}()
	select {
	case event := <-firstEvent:
		assert.Contains(t, event, "first", "the first event reaches the client before the upstream writes the second")
		assert.NotContains(t, event, "second", "the look-ahead did not wait for the rest of the stream")
	case <-time.After(2 * time.Second):
		t.Fatal("the first event did not reach the client while the upstream held the stream open")
	}
}

// TestRelayKeepsTheGeminiEventBeforeAnImageCeiling covers the refusal a stream
// delivers after an event: the client keeps the event the upstream already
// delivered and then reads the refusal, so reading ahead does not drop what
// arrived before it.
func TestRelayKeepsTheGeminiEventBeforeAnImageCeiling(t *testing.T) {
	cleanup := allowImageCeilingRelay(t)
	defer cleanup()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	defer func() { constant.StreamingTimeout = previousTimeout }()

	upstream := newGeminiStreamUpstream(t, func(w http.ResponseWriter, flush func()) {
		_, _ = io.WriteString(w, geminiStreamEvent(geminiTextChunk("before-the-ceiling")))
		flush()
		_, _ = io.WriteString(w, geminiStreamEvent(imageCeilingReply))
		flush()
	})
	defer upstream.server.Close()
	gateway := imageCeilingGeminiGateway(t, upstream.server.URL)
	defer gateway.Close()

	statusCode, payload := postGeminiImageCeiling(t, gateway.URL,
		"/v1beta/models/gemini-ceiling-test:streamGenerateContent?alt=sse")

	assert.Equal(t, http.StatusOK, statusCode, "the stream was committed before the refusal: %s", payload)
	assert.Contains(t, string(payload), "before-the-ceiling", "the event delivered before the refusal reaches the client")
	code, message := streamErrorEnvelope(t, payload)
	assert.Equal(t, "context_length_exceeded", code)
	assert.Contains(t, message, "Total number of images exceeds the maximum allowed of 8.")
	assertImageOverflowWording(t, message)
	requireImageCeilingMessageToldOnce(t, message)
	assert.Equal(t, int32(1), upstream.attempts.Load(), "the upstream is attempted once and never retried")
}

// geminiImageCeilingError reads the error the client receives for a Gemini
// image ceiling: the JSON 400 of a refusal the client reads before any stream
// byte, or the error event the relay writes into a stream it already started.
func geminiImageCeilingError(t *testing.T, reply geminiRefusalReply, payload []byte) (string, string) {
	t.Helper()
	if reply == geminiRefusalAfterAnEvent {
		return streamErrorEnvelope(t, payload)
	}
	return imageCeilingEnvelope(t, types.RelayFormatOpenAI, http.StatusBadRequest, payload)
}

// streamErrorEnvelope reads the error code and message of the last error event
// of a committed event stream.
func streamErrorEnvelope(t *testing.T, payload []byte) (string, string) {
	t.Helper()
	var code, message string
	for _, line := range strings.Split(string(payload), "\n") {
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
		if !ok {
			continue
		}
		var event struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &event) != nil || event.Error == nil {
			continue
		}
		code, message = event.Error.Code, event.Error.Message
	}
	require.NotEmpty(t, code, "the stream carries an error event: %s", payload)
	return code, message
}

// imageCeilingGeminiBody is a native Gemini request carrying a text turn.
const imageCeilingGeminiBody = `{"contents":[{"role":"user","parts":[{"text":"look"}]}]}`

// TestRelayKeepsAnUpstreamTurnImageCountWithoutACeiling is the counter-case
// next to the Command Code wording: a 400 that counts the images of the turn
// but names no ceiling keeps its own code, so only the sentence with the
// ceiling is rewritten.
func TestRelayKeepsAnUpstreamTurnImageCountWithoutACeiling(t *testing.T) {
	cleanup := allowImageCeilingRelay(t)
	defer cleanup()

	upstream := newImageCeilingUpstream(t, http.StatusBadRequest,
		`{"error":{"message":"This turn adds 20 images (the images after the last assistant message); split them.","type":"invalid_request_error","code":"bad_request","param":"messages"}}`)
	gateway := imageCeilingGateway(t, upstream.server.URL, types.RelayFormatOpenAI)
	defer gateway.Close()

	statusCode, payload := postImageCeiling(t, gateway.URL, types.RelayFormatOpenAI,
		imageLimitRelayBodyForModel(t, 20, "deepseek-v4.1-flash-dedicated"))

	code, message := imageCeilingEnvelope(t, types.RelayFormatOpenAI, statusCode, payload)
	assert.NotEqual(t, "context_length_exceeded", code, "a 400 naming no ceiling keeps the error it was raised as: %s", payload)
	assert.Equal(t, "This turn adds 20 images (the images after the last assistant message); split them.", message)
}

// TestRelayKeepsAnUpstreamImageCeilingTheTranslationMisses is the counter-case:
// an upstream 400 that does not name an image ceiling keeps its own code, so
// the translation only rewrites the refusal it recognizes.
func TestRelayKeepsAnUpstreamImageCeilingTheTranslationMisses(t *testing.T) {
	cleanup := allowImageCeilingRelay(t)
	defer cleanup()

	upstream := newImageCeilingUpstream(t, http.StatusBadRequest,
		`{"object":"error","message":"Invalid model: mistral-tiny-1","type":"invalid_request_invalid_args","code":"1002"}`)
	gateway := imageCeilingGateway(t, upstream.server.URL, types.RelayFormatOpenAI)
	defer gateway.Close()

	statusCode, payload := postImageCeiling(t, gateway.URL, types.RelayFormatOpenAI, imageLimitRelayBody(t, 9))

	code, message := imageCeilingEnvelope(t, types.RelayFormatOpenAI, statusCode, payload)
	assert.Equal(t, http.StatusBadRequest, statusCode, "response body: %s", payload)
	assert.NotEqual(t, "context_length_exceeded", code, "an unrelated 400 keeps the error it was raised as")
	assert.Equal(t, "Invalid model: mistral-tiny-1", message)
}

// imageCeilingEnvelope reads the error code and message from the reply of the
// relay format: the OpenAI envelope carries them under error, the Claude one
// under error.type and error.message.
func imageCeilingEnvelope(t *testing.T, relayFormat types.RelayFormat, statusCode int, payload []byte) (string, string) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, statusCode, "response body: %s", payload)
	if relayFormat == types.RelayFormatClaude {
		var body struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(payload, &body))
		require.Equal(t, "error", body.Type)
		return body.Error.Type, body.Error.Message
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(payload, &body))
	return body.Error.Code, body.Error.Message
}

// TestUpstreamImageCeilingKeepsTheChannelEnabled reads the health decision on
// the error the relay lets through: the translated refusal names no channel
// fault, so a channel asked to disable on its errors keeps serving. The
// decision is read with the channel health setting on, which the relay's own
// path cannot make observable without a database. The Command Code per-turn
// wording is read next to the Mistral one.
func TestUpstreamImageCeilingKeepsTheChannelEnabled(t *testing.T) {
	previousAutoDisable := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() { common.AutomaticDisableChannelEnabled = previousAutoDisable }()

	for _, tc := range []struct {
		name    string
		message string
		code    string
	}{
		{name: "mistral wording", message: "Total number of images exceeds the maximum allowed of 8.", code: "3051"},
		{name: "command code per-turn wording", message: commandCodeTurnCeilingMessage, code: "bad_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstreamError := types.WithOpenAIError(types.OpenAIError{
				Message: tc.message,
				Type:    "invalid_request_error",
				Code:    tc.code,
			}, http.StatusBadRequest)

			translated := relay.TranslateUpstreamImageLimitError(upstreamError)
			require.NotSame(t, upstreamError, translated, "the reply is the image ceiling the translation recognizes")
			assert.False(t, types.IsChannelError(translated))
			assert.False(t, service.ShouldDisableChannel(translated), "a context overflow keeps the channel enabled")
		})
	}
}
