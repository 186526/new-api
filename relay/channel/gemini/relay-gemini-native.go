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

// streamRefusalNamesCeiling tells whether a text names the image ceiling. It is
// registered by the relay package, which holds the wording and imports this
// package: the gemini package cannot import it back. A nil predicate leaves the
// stream to the scanner.
var streamRefusalNamesCeiling func(string) bool

// streamRefusalError builds the translated error of an image refusal a stream
// carried, or nil when the text it is given names no ceiling. It is registered
// by the relay package for the same reason, and the error it builds is returned
// to the controller, which writes it into the committed stream the way it
// writes every other relay failure.
var streamRefusalError func(string) *types.NewAPIError

// SetStreamRefusalNamesCeiling registers the predicate and returns the
// previous one for tests to restore.
func SetStreamRefusalNamesCeiling(namesCeiling func(string) bool) func(string) bool {
	previous := streamRefusalNamesCeiling
	streamRefusalNamesCeiling = namesCeiling
	return previous
}

// SetStreamRefusalError registers the builder and returns the previous one for
// tests to restore.
func SetStreamRefusalError(buildRefusal func(string) *types.NewAPIError) func(string) *types.NewAPIError {
	previous := streamRefusalError
	streamRefusalError = buildRefusal
	return previous
}

func GeminiTextGenerationStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	// An upstream can accept the request, commit its headers and only then
	// refuse it for its image count. A streaming request starts its keepalive
	// with the upstream's 2xx, so the stream is usually committed by now and
	// the refusal cannot carry a status code. The refusal is returned anyway:
	// the controller writes it into the committed stream as the one error event
	// of the relay, so the client reads the context overflow instead of a
	// second error event raised by the scanner on its way out.
	if refusal := pendingStreamRefusal(c, resp); refusal != nil {
		return nil, refusal
	}

	helper.SetEventStreamHeaders(c)

	// The refusal a stream delivers after a chunk of the answer is returned the
	// same way, which keeps the chunk: only the error event of the relay follows.
	var deliveredRefusal *types.NewAPIError
	usage, err := geminiStreamHandler(c, info, resp, func(data string, geminiResponse *dto.GeminiChatResponse) bool {
		if refusal := streamRefusalErrorFor(data); refusal != nil {
			deliveredRefusal = refusal
			return false
		}
		err := helper.StringData(c, data)
		if err != nil {
			logger.LogError(c, common.LogText("failed to write stream data: %s", err.Error()))
			return false
		}
		info.SendResponseCount++
		return true
	})
	if deliveredRefusal != nil {
		return usage, deliveredRefusal
	}
	return usage, err
}

// streamRefusalErrorFor returns the error of the image refusal an event carries,
// or nil for every other event.
func streamRefusalErrorFor(data string) *types.NewAPIError {
	if streamRefusalError == nil || streamRefusalNamesCeiling == nil {
		return nil
	}
	if !errorObjectNamesImageCeiling(data, streamRefusalNamesCeiling) {
		return nil
	}
	return streamRefusalError(data)
}

// pendingStreamRefusal reads the first event of a stream ahead of the client
// and returns the error of an upstream that refuses the request for its image
// count in it. Only a 200 is examined, because a refusal carried by another
// status is read from the response body as the non-200 handlers do.
//
// The read stops at the first complete event, so a stream that delivers events
// one at a time is held back by one event and never to its end. The stream is
// left for the scanner: the bytes the read took are played back ahead of it, so
// the refusal event and every event after it still reach the client.
func pendingStreamRefusal(c *gin.Context, resp *http.Response) *types.NewAPIError {
	if resp == nil || resp.StatusCode != http.StatusOK || streamRefusalNamesCeiling == nil {
		return nil
	}
	reader := bufio.NewReader(resp.Body)
	lookAhead := new(strings.Builder)
	event := new(strings.Builder)
	for {
		line, err := reader.ReadSlice('\n')
		lookAhead.Write(line)
		if err != nil {
			// The stream ended or failed inside its first event: what was read
			// is played back and the scanner holds the first line for it.
			break
		}
		text := strings.TrimSpace(string(line))
		if text == "" {
			// The blank line closes the event, which is all the look-ahead may
			// hold back.
			break
		}
		data, isEvent := strings.CutPrefix(text, "data:")
		if !isEvent || event.Len() != 0 {
			// A comment, a field of another kind, and a second data line that
			// continues an event are left to the scanner: only one event is
			// read, and only one line of it carries the payload.
			continue
		}
		event.WriteString(strings.TrimSpace(data))
	}
	read := lookAhead.String()
	restoreStreamBody(resp, read, reader)
	if event.Len() == 0 || !errorObjectNamesImageCeiling(event.String(), streamRefusalNamesCeiling) {
		return nil
	}
	// The refusal of a stream carries no status of its own: the stream is a
	// 200, so the event is read as the 400 an upstream refuses a request with.
	return streamRefusalError(event.String())
}

// restoreStreamBody puts the bytes a look-ahead read back at the head of the
// response body. The reader the look-ahead filled holds the rest of the stream,
// so the two are read in turn; the upstream body is closed by the caller that
// closes the restored one.
func restoreStreamBody(resp *http.Response, read string, rest io.Reader) {
	resp.Body = &restoredStreamBody{
		read: strings.NewReader(read),
		rest: rest,
		body: resp.Body,
	}
}

// restoredStreamBody serves the bytes a look-ahead took before the stream they
// were taken from.
type restoredStreamBody struct {
	read io.Reader
	rest io.Reader
	body io.ReadCloser
}

func (b *restoredStreamBody) Read(p []byte) (int, error) {
	if n, err := b.read.Read(p); n > 0 || err != io.EOF {
		return n, err
	}
	return b.rest.Read(p)
}

func (b *restoredStreamBody) Close() error {
	return b.body.Close()
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
