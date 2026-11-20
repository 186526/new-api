package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/pkg/metrics"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
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
						"Request a smaller batch of images or compact the conversation and retry.",
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
		"Request a smaller batch of images or compact the conversation and retry.", body.Error.Message)
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
			assert.Contains(t, UpstreamModelNames(settings, imageLimitModelMapping(), name), imageLimitMappedModel)
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
	assert.Equal(t, []string{imageLimitMappedAlias, imageLimitMappedModel},
		UpstreamModelNames(settings, imageLimitModelMapping(), imageLimitMappedAlias))
	assert.Equal(t, []string{imageLimitMappedModel},
		UpstreamModelNames(settings, "", imageLimitMappedModel))
	assert.Equal(t, []string{"plain-model"},
		UpstreamModelNames(settings, imageLimitModelMapping(), "plain-model"))
	assert.Equal(t, []string{"plain-model"},
		UpstreamModelNames(settings, "{not json", "plain-model"))
	cycle := `{"a-model":"b-model","b-model":"a-model"}`
	assert.Equal(t, []string{"a-model"}, UpstreamModelNames(settings, cycle, "a-model"),
		"a mapping cycle leaves the requested name in place")
	self := `{"a-model":"a-model"}`
	assert.Equal(t, []string{"a-model"}, UpstreamModelNames(settings, self, "a-model"))
	chain := `{"first-model":"second-model","second-model":"third-model"}`
	assert.Equal(t, []string{"first-model", "third-model"}, UpstreamModelNames(settings, chain, "first-model"),
		"a mapping chain is followed to its end")
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
