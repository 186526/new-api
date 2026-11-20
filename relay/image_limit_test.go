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

func TestCheckRequestImageLimitCountsDistinctImages(t *testing.T) {
	urls := []string{imageData("a"), imageData("a"), imageData("a")}
	for _, relayFormat := range []types.RelayFormat{
		types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatClaude,
		types.RelayFormatGemini,
	} {
		t.Run(string(relayFormat), func(t *testing.T) {
			request := imageLimitTestRequest(t, relayFormat, urls)
			assert.Nil(t, CheckRequestImageLimit(request, relayFormat, 2, 1, "mistral", imageLimitTestModel),
				"three copies of one image are one image")
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

	require.NotNil(t, CheckRequestImageLimit(request, types.RelayFormatClaude, 20, 7, "", imageLimitTestModel))
	require.NotNil(t, CheckRequestImageLimit(request, types.RelayFormatClaude, 20, 7, "", imageLimitTestModel))

	assert.Equal(t, 2.0, metrics.ImageLimitRejectionsForTest("7", imageLimitTestModel))
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
