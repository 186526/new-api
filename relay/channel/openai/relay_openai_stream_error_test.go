package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openAIImageCeilingMessage is the refusal a stubbed event stream carries after
// the headers are committed, in the wording measured on api.mistral.ai.
const openAIImageCeilingMessage = "Total number of images exceeds the maximum allowed of 8."

// openAIImageCeilingRefusal stands in for the translation the relay package
// registers in init: the test package alone does not import the relay package,
// so the hook starts out unregistered and the test registers the shape it
// checks.
func openAIImageCeilingRefusal(t *testing.T) {
	t.Helper()
	realTranslation := SetImageCeilingRefusal(nil)
	SetImageCeilingRefusal(func(event string) *types.NewAPIError {
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(event), &body) != nil || !strings.Contains(body.Error.Message, "maximum allowed") {
			return nil
		}
		return types.WithOpenAIError(types.OpenAIError{
			Message: body.Error.Message + " Request exceeds the context length limit; compact the conversation before retrying it.",
			Type:    "invalid_request_error",
			Code:    "context_length_exceeded",
		}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	})
	// The real translation goes back rather than the hook found here: other
	// tests of this package import the relay package, whose init registers it
	// once, and a stub left behind would outlive this test.
	t.Cleanup(func() { SetImageCeilingRefusal(realTranslation) })
}

// openAIEvent is one chunk of an OpenAI event stream as the upstream writes it.
func openAIEvent(payload string) string {
	return "data: " + payload + "\n\n"
}

// openAITextChunk is the chunk that carries answer text before a refusal.
func openAITextChunk(text string) string {
	return openAIEvent(fmt.Sprintf(`{"id":"chunk-1","object":"chat.completion.chunk","model":"deepseek-v4.1-flash-dedicated","choices":[{"index":0,"delta":{"content":%q}}]}`, text))
}

// openAIErrorEvent is the chunk an upstream refuses a committed stream with.
func openAIErrorEvent(message string, code string) string {
	return openAIEvent(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":%q}}`, message, code))
}

// newOaiStreamHandlerContext runs OaiStreamHandler on an event stream written
// by the test upstream, with the same relay state the retry loop hands the
// handler.
func newOaiStreamHandlerContext(t *testing.T, relayFormat types.RelayFormat, stream string) (*httptest.ResponseRecorder, *types.NewAPIError) {
	t.Helper()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}
	info := &relaycommon.RelayInfo{
		RelayFormat: relayFormat,
		IsStream:    true,
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "deepseek-v4.1-flash-dedicated"},
	}
	info.SetEstimatePromptTokens(10)
	_, apiErr := OaiStreamHandler(c, info, resp)
	return recorder, apiErr
}

// TestOaiStreamHandlerRefusesAnImageCeilingAfterTheHeaders covers a 200 event
// stream that delivers a chunk and then refuses the request for its image
// count. The refusal is returned to the controller, which writes the one error
// event of the relay; the chunk before it stays and no refusal chunk is written
// as content.
func TestOaiStreamHandlerRefusesAnImageCeilingAfterTheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	openAIImageCeilingRefusal(t)
	stream := openAITextChunk("before-the-ceiling") + openAIErrorEvent(openAIImageCeilingMessage, "bad_request")

	recorder, apiErr := newOaiStreamHandlerContext(t, types.RelayFormatOpenAI, stream)

	require.NotNil(t, apiErr, "the refusal reaches the controller: %s", recorder.Body.String())
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCode("context_length_exceeded"), apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr), "the retry loop neither resends nor disables the channel")
	assert.Contains(t, apiErr.Error(), openAIImageCeilingMessage, "the ceiling the upstream named stays")
	assert.Contains(t, recorder.Body.String(), "before-the-ceiling", "the chunk before the refusal stays")
	assert.NotContains(t, recorder.Body.String(), "maximum allowed of 8", "the refusal is not forwarded as a content chunk")
	assert.NotContains(t, recorder.Body.String(), "[DONE]", "the stream ends on the refusal event the controller writes")
}

// TestOaiStreamHandlerKeepsEveryChunkBeforeTheRefusal covers the chunks the
// handler already gave the client when the refusal arrives. The handler
// forwards each chunk one event late, so the chunk before the refusal is still
// held back when the refusal is read; every format the handler serves has to
// write it, not only the OpenAI one, and that no chunk of the refusal is
// forwarded in any of them.
func TestOaiStreamHandlerKeepsEveryChunkBeforeTheRefusal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	openAIImageCeilingRefusal(t)
	stream := openAITextChunk("chunk-one") + openAITextChunk("chunk-two-before-ceiling") + openAIErrorEvent(openAIImageCeilingMessage, "bad_request")

	for _, relayFormat := range []types.RelayFormat{types.RelayFormatOpenAI, types.RelayFormatClaude, types.RelayFormatGemini} {
		t.Run(string(relayFormat), func(t *testing.T) {
			recorder, apiErr := newOaiStreamHandlerContext(t, relayFormat, stream)
			require.NotNil(t, apiErr, "the refusal reaches the controller: %s", recorder.Body.String())
			body := recorder.Body.String()
			assert.Contains(t, body, "chunk-one", "the first chunk before the refusal stays")
			assert.Contains(t, body, "chunk-two-before-ceiling", "the last chunk held back is written on the refusal")
			assert.Less(t, strings.Index(body, "chunk-one"), strings.Index(body, "chunk-two-before-ceiling"), "the chunks keep their order")
			assert.NotContains(t, body, "maximum allowed of 8", "the refusal is not forwarded as a content chunk")
			assert.True(t, streamsCloseTheWayAnAnswerEnds(t, relayFormat, body), "every chunk of the answer reaches the client once, and no frame follows the refusal")
		})
	}
}

// TestOaiStreamHandlerKeepsANonImageStreamError is the counter-case next to the
// ceiling: an error chunk of another kind is streamed as the upstream wrote it,
// as before, so this change only reaches the refusal it recognizes.
func TestOaiStreamHandlerKeepsANonImageStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	openAIImageCeilingRefusal(t)
	stream := openAITextChunk("before-the-error") + openAIErrorEvent("Invalid model: mistral-tiny-1", "1002")

	recorder, apiErr := newOaiStreamHandlerContext(t, types.RelayFormatOpenAI, stream)

	assert.Nil(t, apiErr, "an error of another kind keeps the path it always took")
	body := recorder.Body.String()
	assert.Contains(t, body, "before-the-error")
	assert.Contains(t, body, `"message":"Invalid model: mistral-tiny-1"`, "the error chunk reaches the client with the sentence the upstream wrote")
	assert.Contains(t, body, `"code":"1002"`, "and with the upstream's code")
	assert.NotContains(t, body, "context length limit", "it is not rewritten as a refusal")
}

// TestOaiStreamHandlerLeavesTheStreamAloneWithoutATranslation covers the
// handler on its own, where the relay package has registered nothing: the
// refusal keeps the path it always took, and the chunks before it are streamed
// as always.
func TestOaiStreamHandlerLeavesTheStreamAloneWithoutATranslation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	realTranslation := SetImageCeilingRefusal(nil)
	t.Cleanup(func() { SetImageCeilingRefusal(realTranslation) })
	stream := openAITextChunk("before-the-ceiling") + openAIErrorEvent(openAIImageCeilingMessage, "bad_request")

	recorder, apiErr := newOaiStreamHandlerContext(t, types.RelayFormatOpenAI, stream)

	assert.Nil(t, apiErr)
	assert.Contains(t, recorder.Body.String(), "before-the-ceiling")
	assert.Contains(t, recorder.Body.String(), "[DONE]")
}

// streamsCloseTheWayAnAnswerEnds reports whether the body a handler wrote holds
// every chunk of the answer once, in the order the upstream wrote them, and no
// frame that would follow a refusal.
func streamsCloseTheWayAnAnswerEnds(t *testing.T, relayFormat types.RelayFormat, body string) bool {
	t.Helper()
	return strings.Count(body, "chunk-two-before-ceiling") == 1 &&
		!strings.Contains(body, "[DONE]") &&
		strings.Index(body, "chunk-one") < strings.Index(body, "chunk-two-before-ceiling")
}
