package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	prometheusmodel "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const imageLimitTestModel = "mistral-large-latest"

func imageData(seed string) string {
	return "data:image/png;base64," + seed
}

func imageUrls(count int) []string {
	urls := make([]string, 0, count)
	for i := 0; i < count; i++ {
		urls = append(urls, imageData(strconv.Itoa(i)))
	}
	return urls
}

func imageLimitTestRequest(t *testing.T, relayFormat types.RelayFormat, urls []string) dto.Request {
	t.Helper()
	content := make([]any, 0, len(urls)+1)
	content = append(content, map[string]any{"type": "input_text", "text": "look"})
	for _, url := range urls {
		content = append(content, map[string]any{"type": "input_image", "image_url": url})
	}
	switch relayFormat {
	case types.RelayFormatOpenAI:
		parts := make([]any, 0, len(urls)+1)
		parts = append(parts, map[string]any{"type": "text", "text": "look"})
		for _, url := range urls {
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": url},
			})
		}
		return &dto.GeneralOpenAIRequest{
			Model:    imageLimitTestModel,
			Messages: []dto.Message{{Role: "user", Content: parts}},
		}
	case types.RelayFormatOpenAIResponses:
		raw, err := json.Marshal([]any{map[string]any{"type": "message", "role": "user", "content": content}})
		require.NoError(t, err)
		return &dto.OpenAIResponsesRequest{Model: imageLimitTestModel, Input: raw}
	case types.RelayFormatClaude:
		blocks := make([]any, 0, len(urls)+1)
		blocks = append(blocks, map[string]any{"type": "text", "text": "look"})
		for _, url := range urls {
			blocks = append(blocks, map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "url", "url": url},
			})
		}
		return &dto.ClaudeRequest{
			Model:    imageLimitTestModel,
			Messages: []dto.ClaudeMessage{{Role: "user", Content: blocks}},
		}
	case types.RelayFormatGemini:
		parts := make([]dto.GeminiPart, 0, len(urls)+1)
		parts = append(parts, dto.GeminiPart{Text: "look"})
		for _, url := range urls {
			parts = append(parts, dto.GeminiPart{
				InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: url},
			})
		}
		return &dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{{Role: "user", Parts: parts}}}
	default:
		t.Fatalf("unsupported relay format %s", relayFormat)
		return nil
	}
}

func TestCheckRequestImageLimitAcceptsBelowAndAtTheLimit(t *testing.T) {
	for _, relayFormat := range []types.RelayFormat{
		types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatClaude,
		types.RelayFormatGemini,
	} {
		t.Run(string(relayFormat), func(t *testing.T) {
			for _, images := range []int{0, 1, 5, 19, 20} {
				request := imageLimitTestRequest(t, relayFormat, imageUrls(images))
				assert.Nil(t, CheckRequestImageLimit(request, relayFormat, 20, 1, "mistral", imageLimitTestModel),
					"%d images stay within the limit of 20", images)
			}
		})
	}
}

func TestCheckRequestImageLimitRejectsPastTheLimit(t *testing.T) {
	for _, relayFormat := range []types.RelayFormat{
		types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatClaude,
		types.RelayFormatGemini,
	} {
		t.Run(string(relayFormat), func(t *testing.T) {
			for _, images := range []int{21, 22, 40} {
				request := imageLimitTestRequest(t, relayFormat, imageUrls(images))
				apiErr := CheckRequestImageLimit(request, relayFormat, 20, 1, "mistral", imageLimitTestModel)
				require.NotNil(t, apiErr, "%d images are past the limit of 20", images)
				assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
				assert.Equal(t, imageLimitErrorCode, apiErr.GetErrorCode())
				assert.True(t, types.IsSkipRetryError(apiErr))
				assert.False(t, types.IsChannelError(apiErr))
				assert.Equal(t, apiErr.GetErrorCode(), apiErr.ToOpenAIError().Code)
				assert.Equal(t,
					"Total number of images ("+strconv.Itoa(images)+") exceeds the maximum allowed of 20. "+
						"Request a smaller batch of images or compact the conversation and retry. "+
						"Request exceeds the context length limit; compact the conversation before retrying it.",
					apiErr.Error())
			}
		})
	}
}

// The upstream refuses a request whose image parts repeat, so a limit of 20
// refuses 21 copies of one image.
func TestCheckRequestImageLimitCountsRepeatedImages(t *testing.T) {
	for _, relayFormat := range []types.RelayFormat{
		types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatClaude,
		types.RelayFormatGemini,
	} {
		t.Run(string(relayFormat), func(t *testing.T) {
			repeated := make([]string, 21)
			for i := range repeated {
				repeated[i] = imageData("same")
			}
			request := imageLimitTestRequest(t, relayFormat, repeated)
			apiErr := CheckRequestImageLimit(request, relayFormat, 20, 1, "mistral", imageLimitTestModel)
			require.NotNil(t, apiErr, "21 copies of one image are past the limit of 20")
			assert.Contains(t, apiErr.Error(), "Total number of images (21)")

			within := imageLimitTestRequest(t, relayFormat, repeated[:20])
			assert.Nil(t, CheckRequestImageLimit(within, relayFormat, 20, 1, "mistral", imageLimitTestModel),
				"20 copies of one image stay within the limit of 20")
		})
	}
}

func TestCheckRequestImageLimitWithoutLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		request := imageLimitTestRequest(t, types.RelayFormatClaude, imageUrls(50))
		assert.Nil(t, CheckRequestImageLimit(request, types.RelayFormatClaude, limit, 1, "mistral", imageLimitTestModel),
			"limit %d leaves the channel unlimited", limit)
	}
}

func TestCheckRequestImageLimitIgnoresFormatsWithoutImages(t *testing.T) {
	request := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	assert.Nil(t, CheckRequestImageLimit(request, types.RelayFormatEmbedding, 20, 1, "mistral", imageLimitTestModel))
}

func TestCheckRequestImageLimitCountsRejections(t *testing.T) {
	request := imageLimitTestRequest(t, types.RelayFormatClaude, imageUrls(21))
	channel, model := "image-limit-metric-channel", "image-limit-metric-model"
	other := imageLimitTestRequest(t, types.RelayFormatClaude, imageUrls(5))
	assert.Nil(t, CheckRequestImageLimit(other, types.RelayFormatClaude, 20, 7, channel, model))
	before := imageLimitRejections(t, channel, model)

	require.NotNil(t, CheckRequestImageLimit(request, types.RelayFormatClaude, 20, 7, channel, model))
	require.NotNil(t, CheckRequestImageLimit(request, types.RelayFormatClaude, 20, 7, channel, model))

	assert.Equal(t, before+2, imageLimitRejections(t, channel, model),
		"the two rejections above are counted, an accepted request is not")
}

// imageLimitRejections reads one series of the rejection counter from the
// package registry, so it asserts what the endpoint would report.
func imageLimitRejections(t *testing.T, channel, model string) float64 {
	t.Helper()
	families, err := metrics.Registry().Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "newapi_image_limit_rejections_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricHasLabels(metric, map[string]string{"channel": channel, "model": model}) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func metricHasLabels(metric *prometheusmodel.Metric, want map[string]string) bool {
	labels := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	for name, value := range want {
		if labels[name] != value {
			return false
		}
	}
	return true
}

func TestCheckRequestImageLimitAnswersWithTopLevelErrorCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	request := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	apiErr := CheckRequestImageLimit(request, types.RelayFormatOpenAI, 20, 1, "mistral", imageLimitTestModel)
	require.NotNil(t, apiErr)
	c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "context_length_exceeded", body.Error.Code)
	assert.Equal(t, "invalid_request_error", body.Error.Type)
	assert.Equal(t, "Total number of images (21) exceeds the maximum allowed of 20. "+
		"Request a smaller batch of images or compact the conversation and retry. "+
		"Request exceeds the context length limit; compact the conversation before retrying it.", body.Error.Message)
}

func TestCheckRequestImageLimitForModelsPrefersTheModelLimit(t *testing.T) {
	settings := imageLimitModelSettings()
	for _, relayFormat := range []types.RelayFormat{
		types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatClaude,
		types.RelayFormatGemini,
	} {
		t.Run(string(relayFormat), func(t *testing.T) {
			nine := imageLimitTestRequest(t, relayFormat, imageUrls(9))
			apiErr := CheckRequestImageLimitForModels(nine, relayFormat, settings, imageLimitModelMapping(), 1, "mistral", imageLimitMappedAlias)
			require.NotNil(t, apiErr, "the model limit of 8 refuses the ninth image, the channel limit of 20 would not")
			assert.Contains(t, apiErr.Error(), "exceeds the maximum allowed of 8")

			eight := imageLimitTestRequest(t, relayFormat, imageUrls(8))
			assert.Nil(t, CheckRequestImageLimitForModels(eight, relayFormat, settings, imageLimitModelMapping(), 1, "mistral", imageLimitMappedAlias))
		})
	}
}

func TestUpstreamModelNamesIncludeTheMappingTarget(t *testing.T) {
	settings := imageLimitModelSettings()
	for _, name := range []string{imageLimitMappedAlias, imageLimitMappedModel} {
		t.Run(name, func(t *testing.T) {
			names, err := UpstreamModelNames(settings, imageLimitModelMapping(), name)
			require.NoError(t, err)
			assert.Contains(t, names, imageLimitMappedModel)
			nine := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(9))
			apiErr := CheckRequestImageLimitForModels(nine, types.RelayFormatOpenAI, settings, imageLimitModelMapping(), 1, "mistral", name)
			require.NotNil(t, apiErr, "the limit configured for %s applies to a request for %s", imageLimitMappedModel, name)
			assert.Contains(t, apiErr.Error(), "exceeds the maximum allowed of 8")
		})
	}
}

func TestCheckRequestImageLimitForModelsFallsBackToTheChannelLimit(t *testing.T) {
	settings := imageLimitModelSettings()
	request := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(15))
	assert.Nil(t, CheckRequestImageLimitForModels(request, types.RelayFormatOpenAI, settings, "", 1, "mistral", "qwen-vl-max"),
		"a model without its own limit keeps the channel limit of 20")

	withoutModels := dto.ChannelSettings{MaxImagesPerRequest: 8}
	over := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(10))
	apiErr := CheckRequestImageLimitForModels(over, types.RelayFormatOpenAI, withoutModels, imageLimitModelMapping(), 1, "mistral", imageLimitMappedAlias)
	require.NotNil(t, apiErr, "without per-model limits the channel limit applies to the mapped model too")
	assert.Contains(t, apiErr.Error(), "exceeds the maximum allowed of 8")
}

func TestUpstreamModelNames(t *testing.T) {
	settings := imageLimitModelSettings()
	for _, tc := range []struct {
		name         string
		modelMapping string
		model        string
		want         []string
	}{
		{name: "mapping target", modelMapping: imageLimitModelMapping(), model: imageLimitMappedAlias,
			want: []string{imageLimitMappedAlias, imageLimitMappedModel}},
		{name: "no mapping", model: imageLimitMappedModel, want: []string{imageLimitMappedModel}},
		{name: "unmapped model", modelMapping: imageLimitModelMapping(), model: "plain-model", want: []string{"plain-model"}},
		{name: "unreadable mapping", modelMapping: "{not json", model: "plain-model", want: []string{"plain-model"}},
		{name: "self mapping", modelMapping: `{"a-model":"a-model"}`, model: "a-model", want: []string{"a-model"}},
		{name: "chain", modelMapping: `{"first-model":"second-model","second-model":"third-model"}`, model: "first-model",
			want: []string{"first-model", "third-model"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			names, err := UpstreamModelNames(settings, tc.modelMapping, tc.model)
			require.NoError(t, err)
			assert.Equal(t, tc.want, names)
		})
	}

	cycle := `{"a-model":"b-model","b-model":"a-model"}`
	_, err := UpstreamModelNames(settings, cycle, "a-model")
	require.Error(t, err, "a mapping cycle has no model to send the request under")
	assert.Contains(t, err.Error(), "model_mapping_contains_cycle")
	var cycleErr *modelMappingCycleError
	assert.ErrorAs(t, err, &cycleErr)

	// A model that merely points into a cycle still reports it: the chain it
	// entered never ends.
	_, err = UpstreamModelNames(settings, `{"entry-model":"a-model","a-model":"b-model","b-model":"a-model"}`, "entry-model")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model_mapping_contains_cycle")
}

// A channel whose model_mapping forms a cycle never delivers the request: the
// relay fails it with model_mapping_contains_cycle. The image check refuses it
// the same way instead of comparing its images against the channel-wide 20,
// which would let 9 to 20 images of a model limited to 8 through to that
// failure.
func TestCheckRequestImageLimitForModelsRefusesAMappingCycle(t *testing.T) {
	settings := imageLimitModelSettings()
	cycle := `{"glm-5.3-flash":"zai-glm-5-3-flash","zai-glm-5-3-flash":"glm-5.3-flash"}`
	// Nine images of the model limited to 8 would pass the channel-wide 20 and
	// reach the upstream under a mapping the relay then rejects.
	for _, tc := range []struct {
		name         string
		modelMapping string
		model        string
	}{
		{name: "nine images of the limited model", modelMapping: cycle, model: imageLimitMappedAlias},
		{name: "cycle entered from another model",
			modelMapping: `{"entry-model":"glm-5.3-flash","glm-5.3-flash":"zai-glm-5-3-flash","zai-glm-5-3-flash":"glm-5.3-flash"}`,
			model:        "entry-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, relayFormat := range []types.RelayFormat{
				types.RelayFormatOpenAI,
				types.RelayFormatOpenAIResponses,
				types.RelayFormatClaude,
				types.RelayFormatGemini,
			} {
				t.Run(string(relayFormat), func(t *testing.T) {
					nine := imageLimitTestRequest(t, relayFormat, imageUrls(9))
					apiErr := CheckRequestImageLimitForModels(nine, relayFormat, settings, tc.modelMapping, 1, "mistral", tc.model)
					require.NotNil(t, apiErr, "a mapping cycle refuses the request before its images are compared")
					assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
					assert.Contains(t, apiErr.Error(), "model_mapping_contains_cycle")
					assert.True(t, types.IsSkipRetryError(apiErr))
					assert.False(t, types.IsChannelError(apiErr), "the channel is not at fault for its mapping")
				})
			}
		})
	}

	// The same channel keeps working for a model outside the cycle: nine
	// images of the model limited to 20 pass, and the model limited to 8 is
	// still refused by its own limit.
	uncycled := `{"glm-5.3-flash":"zai-glm-5-3-flash","zai-glm-5-3-flash":"glm-5.3-flash","deepseek-v4.1-flash":"deepseek-v4-1-flash-1017"}`
	nine := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(9))
	assert.Nil(t, CheckRequestImageLimitForModels(nine, types.RelayFormatOpenAI, settings, uncycled, 1, "mistral", "deepseek-v4.1-flash"))
	twentyOne := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	apiErr := CheckRequestImageLimitForModels(twentyOne, types.RelayFormatOpenAI, settings, uncycled, 1, "mistral", "deepseek-v4.1-flash")
	require.NotNil(t, apiErr, "the model limited to 20 is still judged through its mapping outside the cycle")
	assert.Contains(t, apiErr.Error(), "exceeds the maximum allowed of 20")
}

// The measured upstream limits of the models channel 14 and 15 serve: one
// channel carries two models whose limits differ.
const (
	imageLimitMappedAlias = "glm-5.3-flash"
	imageLimitMappedModel = "zai-glm-5-3-flash"
)

func imageLimitModelMapping() string {
	return `{"` + imageLimitMappedAlias + `":"` + imageLimitMappedModel + `"}`
}

// imageLimitModelSettings is what a Mistral channel looks like once the
// measured limits are configured: the channel-wide limit of 20 covers the
// models measured at 20, and the models measured at 8 carry their own limit.
func imageLimitModelSettings() dto.ChannelSettings {
	return dto.ChannelSettings{
		MaxImagesPerRequest: 20,
		MaxImagesPerModel: map[string]int{
			imageLimitMappedModel:            8,
			imageLimitMeasuredWideLimitModel: 20,
		},
	}
}

const imageLimitMeasuredWideLimitModel = "deepseek-v4-1-flash-1017"

// TestCheckRequestImageLimitForModelsKeepsTheWiderModelLimit covers the model
// measured at 20 on the same channel: its limit is the channel-wide 20, so a
// request of 20 images passes and the 21st is refused.
func TestCheckRequestImageLimitForModelsKeepsTheWiderModelLimit(t *testing.T) {
	settings := imageLimitModelSettings()
	atLimit := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(20))
	assert.Nil(t, CheckRequestImageLimitForModels(atLimit, types.RelayFormatOpenAI, settings, imageLimitModelMapping(), 1, "mistral", imageLimitMeasuredWideLimitModel))
	over := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(21))
	apiErr := CheckRequestImageLimitForModels(over, types.RelayFormatOpenAI, settings, imageLimitModelMapping(), 1, "mistral", imageLimitMeasuredWideLimitModel)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "exceeds the maximum allowed of 20")
}

// upstreamImageLimitError is the error the upstream's 400 becomes on the relay
// path: a Message and a Code the upstream chose, wrapped as an OpenAI relay
// error carrying the upstream status.
func upstreamImageLimitError(statusCode int, message string, code any) *types.NewAPIError {
	return types.WithOpenAIError(types.OpenAIError{
		Message: message,
		Type:    "invalid_request_invalid_args",
		Code:    code,
	}, statusCode)
}

// commandCodeTurnCeilingMessage is the message measured on Command Code, the
// model deepseek-v4.1-flash-dedicated behind channels 3 and 4, when a request
// carries 20 images of which the last assistant message leaves 20 in the
// current turn. The ceiling is per turn, not per request.
const commandCodeTurnCeilingMessage = "This turn adds 20 images (the images after the last assistant message); " +
	"deepseek-v4.1-flash-dedicated takes at most 8 new images per turn. " +
	"Images earlier in the conversation do not count."

// imageLimitWordingCases lists the wordings the translation must rewrite, with
// the phrase the client has to find in the translated message. The Mistral
// wordings are read on api.mistral.ai, the Command Code ones on the model
// deepseek-v4.1-flash-dedicated behind channels 3 and 4.
var imageLimitWordingCases = []struct {
	name    string
	code    any
	message string
	ceiling string
}{
	{name: "mistral measured reply", code: "3051", message: "Total number of images exceeds the maximum allowed of 8.", ceiling: "maximum allowed of"},
	{name: "mistral wide model", code: "3051", message: "Total number of images exceeds the maximum allowed of 20.", ceiling: "maximum allowed of"},
	{name: "mistral lower case", code: nil, message: "total number of images exceeds the maximum allowed of 8", ceiling: "maximum allowed of"},
	{name: "mistral singular", code: "3051", message: "Total number of image exceeds the maximum allowed of 8.", ceiling: "maximum allowed of"},
	{name: "mistral count in the sentence", code: "3051", message: "Total number of images (9) exceeds the maximum allowed of 8.", ceiling: "maximum allowed of"},
	{name: "mistral second sentence", code: "3051", message: "Total number of images exceeds the maximum allowed of 8. Split the request.", ceiling: "maximum allowed of"},
	{name: "mistral plural verb", code: "3051", message: "Total number of images exceed the maximum allowed of 8.", ceiling: "maximum allowed of"},
	{name: "command code per-turn", code: "bad_request", message: commandCodeTurnCeilingMessage, ceiling: "at most 8 new images per turn"},
	{name: "command code per-turn in a wider sentence", code: "bad_request", message: "This turn adds 12 images. The model takes at most 8 new images per turn, so send fewer.", ceiling: "at most 8 new images per turn"},
}

// TestTranslateUpstreamImageLimitErrorRewritesTheUpstreamRefusal covers the
// replies measured on the upstreams: each names the ceiling of its own model,
// per request on api.mistral.ai and per turn on Command Code, and the client
// reads all of them as a context overflow it can compact away.
func TestTranslateUpstreamImageLimitErrorRewritesTheUpstreamRefusal(t *testing.T) {
	for _, tc := range imageLimitWordingCases {
		t.Run(tc.name, func(t *testing.T) {
			apiErr := TranslateUpstreamImageLimitError(upstreamImageLimitError(http.StatusBadRequest, tc.message, tc.code))
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, imageLimitErrorCode, apiErr.GetErrorCode())
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.False(t, types.IsChannelError(apiErr), "the channel is not at fault for the request's image count")
			assert.Equal(t, imageLimitMessage(tc.message), apiErr.Error())
			assert.Contains(t, apiErr.Error(), tc.ceiling, "the ceiling the upstream named stays in the message")
			openAIError := apiErr.ToOpenAIError()
			assert.Equal(t, "invalid_request_error", openAIError.Type)
			assert.Equal(t, openAIError.Code, apiErr.GetErrorCode(), "the top-level error code is the one a client reads")
			assert.Equal(t, string(imageLimitErrorCode), apiErr.ToClaudeError().Type,
				"the Claude envelope carries the same code it reads from the local refusal")
		})
	}
}

// TestTranslateUpstreamImageLimitErrorReadsTheUpstreamJSONBody covers the body
// measured on Command Code behind channels 3 and 4: a 400 whose error object is
// the per-turn ceiling. The body is read the way the relay reads every upstream
// error, and the client gets the context overflow with the ceiling the upstream
// named.
func TestTranslateUpstreamImageLimitErrorReadsTheUpstreamJSONBody(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": commandCodeTurnCeilingMessage,
			"type":    "invalid_request_error",
			"code":    "bad_request",
			"param":   "messages",
		},
	})
	require.NoError(t, err)
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	upstreamError := service.RelayErrorHandler(context.Background(), resp, false)
	require.NotNil(t, upstreamError)
	require.Equal(t, commandCodeTurnCeilingMessage, upstreamError.Error(), "the relay reads the message of the error object")

	translated := TranslateUpstreamImageLimitError(upstreamError)
	require.NotSame(t, upstreamError, translated, "the per-turn ceiling is recognized in the body one reads from the upstream")
	assert.Equal(t, imageLimitErrorCode, translated.GetErrorCode(), "the top-level error code is the one a client reads")
	assert.Contains(t, translated.Error(), "deepseek-v4.1-flash-dedicated takes at most 8 new images per turn",
		"the ceiling the upstream named stays in the message")
	assert.Contains(t, translated.Error(), imageLimitRetryHint)
	assert.Equal(t, commandCodeTurnCeilingMessage+" "+imageLimitRetryHint+" "+imageLimitContextOverflowHint, translated.Error())
	assert.True(t, types.IsSkipRetryError(translated))
}

// TestTranslateUpstreamImageLimitErrorKeepsOtherErrors covers the replies the
// translation must leave alone: a 400 of another kind, another status, and an
// error new-api raised itself.
func TestTranslateUpstreamImageLimitErrorKeepsOtherErrors(t *testing.T) {
	measured := "Total number of images exceeds the maximum allowed of 8."
	turnWording := "deepseek-v4.1-flash-dedicated takes at most 8 new images per turn."
	for _, tc := range []struct {
		name string
		err  *types.NewAPIError
	}{
		{name: "nil", err: nil},
		{name: "other 400", err: upstreamImageLimitError(http.StatusBadRequest, "Invalid model: mistral-tiny-1", "1002")},
		{name: "message naming images without a ceiling", err: upstreamImageLimitError(http.StatusBadRequest, "Image input is not supported", "1002")},
		{name: "image ceiling under another status", err: upstreamImageLimitError(http.StatusUnprocessableEntity, measured, "3051")},
		{
			name: "per-turn count without the upstream's ceiling",
			err:  upstreamImageLimitError(http.StatusBadRequest, "This turn adds 20 images (the images after the last assistant message); split them.", "bad_request"),
		},
		{
			name: "per-turn ceiling under another status",
			err:  upstreamImageLimitError(http.StatusForbidden, commandCodeTurnCeilingMessage, "bad_request"),
		},
		{
			name: "ceiling without per turn",
			err:  upstreamImageLimitError(http.StatusBadRequest, "deepseek-v4.1-flash-dedicated takes at most 8 new images.", "bad_request"),
		},
		{
			name: "ceiling without a number",
			err:  upstreamImageLimitError(http.StatusBadRequest, "deepseek-v4.1-flash-dedicated takes at most a few new images per turn.", "bad_request"),
		},
		{
			name: "other per-turn sentence",
			err:  upstreamImageLimitError(http.StatusBadRequest, "deepseek-v4.1-flash-dedicated takes at most 8 new messages per turn.", "bad_request"),
		},
		{
			name: "ready to send 8 new images",
			err:  upstreamImageLimitError(http.StatusBadRequest, "deepseek-v4.1-flash-dedicated takes up to 8 new images per turn.", "bad_request"),
		},
		{
			name: "wording raised by new-api",
			err: types.NewErrorWithStatusCode(fmt.Errorf("image download failed: %s", measured),
				types.ErrorCodeInvalidRequest, http.StatusBadRequest),
		},
		{
			name: "per-turn wording raised by new-api",
			err: types.NewErrorWithStatusCode(fmt.Errorf("image download failed: %s", turnWording),
				types.ErrorCodeInvalidRequest, http.StatusBadRequest),
		},
		{
			name: "wording from a channel's own failure",
			err: types.NewErrorWithStatusCode(fmt.Errorf("channel config: %s", measured),
				types.ErrorCodeChannelParamOverrideInvalid, http.StatusBadRequest),
		},
		{
			name: "per-turn wording from a channel's own failure",
			err: types.NewErrorWithStatusCode(fmt.Errorf("channel config: %s", turnWording),
				types.ErrorCodeChannelParamOverrideInvalid, http.StatusBadRequest),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translated := TranslateUpstreamImageLimitError(tc.err)
			assert.Same(t, tc.err, translated, "an error of another kind is returned unchanged")
		})
	}
}

// TestTranslateUpstreamImageLimitErrorCompletesAContextOverflow covers a
// reply the upstream already coded as a context overflow: the client needs no
// new code, but the action stays missing from the upstream's sentence, so the
// translation adds it and makes the error skip the retry. The client still
// reads the context overflow from the message, whatever the code says.
func TestTranslateUpstreamImageLimitErrorCompletesAContextOverflow(t *testing.T) {
	upstreamMessage := "Total number of images (9) exceeds the maximum allowed of 8."
	apiErr := upstreamImageLimitError(http.StatusBadRequest, upstreamMessage, imageLimitErrorCode)
	translated := TranslateUpstreamImageLimitError(apiErr)
	require.NotNil(t, translated)
	assert.Equal(t, imageLimitErrorCode, translated.GetErrorCode())
	assert.Equal(t, imageLimitMessage(upstreamMessage), translated.Error())
	assert.True(t, types.IsSkipRetryError(translated))
}

// TestTranslateUpstreamImageLimitErrorKeepsChannelFaults covers a reply that
// names the image ceiling and also reports a channel fault. The translation
// leaves it alone, so the channel code keeps its meaning and the relay can
// still disable the channel. The source check alone decides this: the test
// fails if a rewrite ever drops the channel code.
func TestTranslateUpstreamImageLimitErrorKeepsChannelFaults(t *testing.T) {
	apiErr := types.WithOpenAIError(types.OpenAIError{
		Message: "Total number of images exceeds the maximum allowed of 8.",
		Type:    "invalid_request_invalid_args",
		Code:    types.ErrorCodeChannelParamOverrideInvalid,
	}, http.StatusBadRequest)
	assert.True(t, types.IsChannelError(apiErr))
	translated := TranslateUpstreamImageLimitError(apiErr)
	assert.Same(t, apiErr, translated, "a channel fault is not rewritten into the client's image count")
	assert.True(t, types.IsChannelError(translated))
	assert.Equal(t, "channel", service.RelayErrorSource(translated), "the source check turns it away, no separate guard is needed")
}

// TestTranslateUpstreamImageLimitErrorMasksTheKeyItRepeats covers an upstream
// that echoes a credential in the same sentence as the ceiling. The client
// reads exactly what the untranslated error would have been masked to, so the
// rebuild cannot hand out a key the envelope would have hidden.
func TestTranslateUpstreamImageLimitErrorMasksTheKeyItRepeats(t *testing.T) {
	upstreamURL := "https://api.mistral.ai"
	upstreamMessage := "Invalid base url: https://user:KEY@host. Total number of images (9) exceeds the maximum allowed of 8."
	apiErr := upstreamImageLimitError(http.StatusBadRequest, upstreamMessage, "3051")

	translated := TranslateUpstreamImageLimitError(apiErr)
	require.NotSame(t, apiErr, translated, "the ceiling sentence is still recognized")

	untranslated := apiErr.ToOpenAIError(upstreamURL).Message
	assert.NotContains(t, untranslated, "KEY", "the envelope masks the credential without the translation")
	assert.NotContains(t, translated.Error(), "KEY", "the translated message is masked before it is rebuilt")
	assert.Equal(t, strings.TrimSpace(untranslated)+" "+imageLimitRetryHint+" "+imageLimitContextOverflowHint, translated.Error(),
		"the translated message is the masked text the client would have read, plus the hint")
	assert.Equal(t, translated.Error(), translated.ToOpenAIError(upstreamURL).Message,
		"masking the translated message again changes nothing")
	assert.Equal(t, imageLimitErrorCode, translated.GetErrorCode())
}

// A channel without any image limit refuses nothing, including through the
// per-model entry: a zero limit means unlimited, not a ceiling of zero.
func TestCheckRequestImageLimitForModelsLeavesAnUnlimitedChannel(t *testing.T) {
	request := imageLimitTestRequest(t, types.RelayFormatOpenAI, imageUrls(9))
	assert.Nil(t, CheckRequestImageLimitForModels(request, types.RelayFormatOpenAI, dto.ChannelSettings{}, "", 1, "mistral", imageLimitMappedAlias))
	zeroed := dto.ChannelSettings{MaxImagesPerModel: map[string]int{imageLimitMappedAlias: 0}}
	assert.Nil(t, CheckRequestImageLimitForModels(request, types.RelayFormatOpenAI, zeroed, "", 1, "mistral", imageLimitMappedAlias),
		"a model configured with a zero limit stays unlimited")
}

// contextOverflowWording is the pattern Kimi Code 2.4.1 compacts a 400 on: a
// Chat Completions reply enters its context overflow path only when the message
// matches one of its patterns, among them this one, and the reply's
// error.code is not read on that path.
var contextOverflowWording = regexp.MustCompile(`(?i)context[ _-]?length`)

// previousImageLimitMessage is the refusal as it read before the wording was
// added: the ceiling, the action, and the context-length code on the envelope.
// Kimi Code 2.4.1 answered it with `provider.api_error: 400 ...` and exit 1
// against a mock upstream, and the same body carrying contextOverflowWording
// compacted, retried and exited 0, so the tests below fail if a refusal ever
// reads like this again.
func previousImageLimitMessage(ceiling string) string {
	return ceiling + " " + imageLimitRetryHint
}

// TestImageLimitRefusalReadsAsAContextOverflow covers both refusals: the one
// new-api makes before the upstream sees the request, and the one it writes from
// the upstream's own reply. Each keeps its ceiling and the action, reads the
// same HTTP status and code, and carries the context length limit the client
// scans for.
func TestImageLimitRefusalReadsAsAContextOverflow(t *testing.T) {
	localCeiling := "Total number of images (21) exceeds the maximum allowed of 20."
	local := refuseImagesPastLimit(21, 20, 1, "mistral", imageLimitTestModel)
	translated := TranslateUpstreamImageLimitError(upstreamImageLimitError(
		http.StatusBadRequest, commandCodeTurnCeilingMessage, "bad_request"))

	for name, apiErr := range map[string]*types.NewAPIError{"local refusal": local, "translated upstream refusal": translated} {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, apiErr)
			message := apiErr.Error()
			assert.Regexp(t, contextOverflowWording, message, "the client finds the context length limit in the message")
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.Equal(t, imageLimitErrorCode, apiErr.GetErrorCode())
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.Contains(t, message, imageLimitRetryHint)
		})
	}
	assert.True(t, strings.HasPrefix(local.Error(), localCeiling),
		"the ceiling opens the message, so the context length limit shares its sentence")
	assert.Contains(t, translated.Error(), "takes at most 8 new images per turn",
		"the upstream's own per-turn ceiling stays in the translated message")
}

// TestPreviousImageLimitWordingDoesNotReadAsAContextOverflow is the
// counter-case: the message as it read before, with the same ceiling and hint
// and the same context-length code on the envelope, is not matched by the
// client's pattern. A refusal that reads this way keeps the behavior measured
// against Kimi Code 2.4.1: the client fails on the 400 and never compacts.
func TestPreviousImageLimitWordingDoesNotReadAsAContextOverflow(t *testing.T) {
	previous := upstreamImageLimitError(http.StatusBadRequest, previousImageLimitMessage("Total number of images (21) exceeds the maximum allowed of 20."), imageLimitErrorCode)
	assert.Equal(t, imageLimitErrorCode, previous.GetErrorCode(), "the counter-case carries the code the old refusal carried")
	assert.NotRegexp(t, contextOverflowWording, previous.Error(), "the old wording is what the client failed on")
	assert.NotRegexp(t, contextOverflowWording, imageLimitRetryHint,
		"the sentence carrying the wording is the added one, not the hint both refusals already carried")
}
