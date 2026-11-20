package gemini

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func GeminiTextGenerationHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// 读取响应体
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	logger.LogDebug(c, "Gemini native response body: %s", responseBody)

	// 解析为 Gemini 原生响应格式
	var geminiResponse dto.GeminiChatResponse
	err = common.Unmarshal(responseBody, &geminiResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	info.ObserveResponseModel(gjson.GetBytes(responseBody, "modelVersion").Str)
	var grounding geminiGroundingCounter
	grounding.observe(&geminiResponse)
	grounding.commit(info)
	countGeminiBillableFunctionCalls(info, &geminiResponse)

	if len(geminiResponse.Candidates) == 0 && geminiResponse.PromptFeedback != nil && geminiResponse.PromptFeedback.BlockReason != nil {
		info.PerformanceBusinessRejection = true
		common.SetContextKey(c, constant.ContextKeyAdminRejectReason, fmt.Sprintf("gemini_block_reason=%s", *geminiResponse.PromptFeedback.BlockReason))
	}

	// 计算使用量（优先上游 UsageMetadata，缺失时本地估算并保留 Gemini 计费语义）
	usage := buildUsageFromGeminiResponse(c, info, &geminiResponse)

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return &usage, nil
}

func NativeGeminiEmbeddingHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}

	logger.LogDebug(c, "Gemini native embedding response body: %s", responseBody)

	var metadata *dto.GeminiEmbeddingUsageMetadata
	if info.IsGeminiBatchEmbedding {
		var geminiResponse dto.GeminiBatchEmbeddingResponse
		err = common.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		metadata = geminiResponse.UsageMetadata
	} else {
		var geminiResponse dto.GeminiEmbeddingResponse
		err = common.Unmarshal(responseBody, &geminiResponse)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		metadata = geminiResponse.UsageMetadata
	}

	var usage *dto.Usage
	if upstream := metadata.GeminiUsageMetadata(); upstream != nil {
		upstreamUsage := buildUsageFromGeminiMetadata(upstream, info.GetEstimatePromptTokens())
		usage = &upstreamUsage
	} else {
		usage = service.ResponseText2Usage(c, "", info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	service.IOCopyBytesGracefully(c, resp, responseBody)

	return usage, nil
}

// streamRefusalNamesCeiling is what the relay package registers at start-up:
// the predicate telling whether a text names an image ceiling. The gemini
// package cannot import the relay package, which imports every adaptor, so the
// wording is handed to it instead of being read here.
var streamRefusalNamesCeiling func(string) bool

// SetStreamRefusalNamesCeiling registers the predicate and returns the
// previous one for tests to restore.
func SetStreamRefusalNamesCeiling(namesCeiling func(string) bool) func(string) bool {
	previous := streamRefusalNamesCeiling
	streamRefusalNamesCeiling = namesCeiling
	return previous
}

func GeminiTextGenerationStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	// An upstream can accept the request, commit its headers and only then
	// refuse it: the event stream opens with the refusal, or delivers an event
	// before it. Once the stream is committed the refusal can no longer carry
	// its status code, so the first event is read before the headers go out.
	if streamRefusalNamesCeiling != nil {
		if refusal := pendingStreamRefusal(c, resp, streamRefusalNamesCeiling); refusal != nil {
			return nil, refusal
		}
	}

	helper.SetEventStreamHeaders(c)

	return geminiStreamHandler(c, info, resp, func(data string, geminiResponse *dto.GeminiChatResponse) bool {
		err := helper.StringData(c, data)
		if err != nil {
			logger.LogError(c, common.LogText("failed to write stream data: %s", err.Error()))
			return false
		}
		info.SendResponseCount++
		return true
	})
}

// pendingStreamRefusal reads the event stream ahead of the first write and
// returns the error of an upstream that refuses the request for its image
// count inside it: the event stream opens with the ceiling, or delivers an
// event before it. Only a 200 is examined, because a refusal carried by
// another status is read from the response body as the non-200 handlers do.
//
// Only the image ceiling is recognized, because an event carries no marker
// telling a refusal from a chunk: a refusal the translation does not match is
// streamed as before. A body that opens with an error object instead of an
// event is the refusal too.
//
// What the look-ahead read is put back before the caller streams on, so every
// byte of a stream it does not refuse reaches the client.
func pendingStreamRefusal(c *gin.Context, resp *http.Response, namesCeiling func(string) bool) *types.NewAPIError {
	if resp == nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	reader := bufio.NewReader(resp.Body)
	lookAhead := new(strings.Builder)
	for {
		line, err := reader.ReadString('\n')
		lookAhead.WriteString(line)
		if err != nil {
			// A stream that ends or stalls inside its first line is left to
			// the scanner, which holds the line once the rest arrives.
			break
		}
		text := strings.TrimSpace(line)
		data, isEvent := strings.CutPrefix(text, "data:")
		if !isEvent && !strings.HasPrefix(text, "{") {
			continue
		}
		if isEvent {
			data = strings.TrimSpace(data)
			if data == "" || data == "[DONE]" {
				continue
			}
		} else {
			data = text
		}
		if !errorObjectNamesImageCeiling(data, namesCeiling) {
			continue
		}
		// The refusal of a stream carries no status of its own: the stream is
		// a 200, so the event is read as the 400 an upstream refuses a request
		// with. The event is the body RelayErrorHandler reads and closes.
		resp.StatusCode = http.StatusBadRequest
		resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(data), reader))
		return service.RelayErrorHandler(c.Request.Context(), resp, false)
	}
	resp.Body = io.NopCloser(io.MultiReader(strings.NewReader(lookAhead.String()), reader))
	return nil
}

// errorObjectNamesImageCeiling reports whether an event is a JSON object naming
// the image ceiling. The ceiling is read in a top-level message and in the
// error object, the two shapes of a refusal.
func errorObjectNamesImageCeiling(data string, namesCeiling func(string) bool) bool {
	if !json.Valid([]byte(data)) {
		return false
	}
	var event struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return false
	}
	if len(event.Error) == 0 {
		return namesCeiling(event.Message)
	}
	return namesCeiling(event.Message) || namesCeiling(string(event.Error))
}
