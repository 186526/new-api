package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeImageCeilingMessage is the refusal a stubbed event stream carries after
// the headers are committed, in the wording measured on api.mistral.ai.
const claudeImageCeilingMessage = "Total number of images exceeds the maximum allowed of 8."

// claudeImageCeilingRefusal stands in for the translation the relay package
// registers in init: the test package alone does not import the relay package,
// so the hook starts out unregistered and the test registers the shape it
// checks.
func claudeImageCeilingRefusal(t *testing.T) {
	t.Helper()
	realTranslation := SetImageCeilingRefusal(nil)
	SetImageCeilingRefusal(func(relayFormat types.RelayFormat, event string) *types.NewAPIError {
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(event), &body) != nil || !strings.Contains(body.Error.Message, "maximum allowed") {
			return nil
		}
		code := "context_length_exceeded"
		if relayFormat == types.RelayFormatOpenAI {
			code = "upstream_error"
		}
		return types.WithOpenAIError(types.OpenAIError{
			Message: body.Error.Message + " Request exceeds the context length limit; compact the conversation before retrying it.",
			Type:    "invalid_request_error",
			Code:    code,
		}, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	})
	// The real translation goes back rather than the hook found here: other
	// tests of this package import the relay package, whose init registers it
	// once, and a stub left behind would outlive this test.
	t.Cleanup(func() { SetImageCeilingRefusal(realTranslation) })
}

// newClaudeStreamHandlerContext runs ClaudeStreamHandler on an event stream
// written by the test upstream, with the same relay state the retry loop hands
// the handler.
func newClaudeStreamHandlerContext(t *testing.T, relayFormat types.RelayFormat, stream string) (*httptest.ResponseRecorder, *types.NewAPIError) {
	t.Helper()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}
	info := &common.RelayInfo{
		RelayFormat: relayFormat,
		IsStream:    true,
		ChannelMeta: &common.ChannelMeta{UpstreamModelName: "claude-opus-4-8"},
	}
	info.SetEstimatePromptTokens(10)
	_, apiErr := ClaudeStreamHandler(c, resp, info)
	return recorder, apiErr
}

// claudeEvent is one Claude event as the upstream writes it.
func claudeEvent(name string, payload string) string {
	return "event: " + name + "\ndata: " + payload + "\n\n"
}

// claudeTextChunk is the event that carries answer text before a refusal.
func claudeTextChunk(text string) string {
	return claudeEvent("content_block_delta",
		fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%q}}`, text))
}

// claudeErrorEvent is the error event an upstream refuses a committed stream with.
func claudeErrorEvent(errorType string, message string) string {
	return claudeEvent("error", fmt.Sprintf(`{"type":"error","error":{"type":"%s","message":%q}}`, errorType, message))
}

// claudeErrorEvents lists the error events a written stream carries.
func claudeErrorEvents(t *testing.T, stream string) []dto.ClaudeResponse {
	t.Helper()
	var events []dto.ClaudeResponse
	for _, line := range strings.Split(stream, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var event dto.ClaudeResponse
		if json.Unmarshal([]byte(data), &event) != nil || event.Type != "error" {
			continue
		}
		events = append(events, event)
	}
	return events
}

// TestClaudeStreamHandlerRefusesAnImageCeilingAfterTheHeaders covers a 200
// event stream that delivers a chunk and then refuses the request for its image
// count. The handler returns the translated refusal instead of writing an error
// event of its own, so the controller writes the one error event of the relay
// after the chunk it already streamed.
func TestClaudeStreamHandlerRefusesAnImageCeilingAfterTheHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claudeImageCeilingRefusal(t)
	stream := claudeTextChunk("before-the-ceiling") + claudeErrorEvent("invalid_request_error", claudeImageCeilingMessage)

	recorder, apiErr := newClaudeStreamHandlerContext(t, types.RelayFormatClaude, stream)

	require.NotNil(t, apiErr, "the refusal reaches the controller: %s", recorder.Body.String())
	assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	assert.Equal(t, types.ErrorCode("context_length_exceeded"), apiErr.GetErrorCode())
	assert.True(t, types.IsSkipRetryError(apiErr), "the retry loop neither resends nor disables the channel")
	assert.Contains(t, apiErr.Error(), claudeImageCeilingMessage, "the ceiling the upstream named stays")
	assert.Contains(t, recorder.Body.String(), "before-the-ceiling", "the chunk before the refusal stays")
	assert.Empty(t, claudeErrorEvents(t, recorder.Body.String()), "the handler writes no error event of its own")
}

// TestClaudeStreamHandlerKeepsANonImageStreamError is the counter-case: an
// error event that names no ceiling keeps the error the handler raised from it,
// and it is written where it always was.
func TestClaudeStreamHandlerKeepsANonImageStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	claudeImageCeilingRefusal(t)
	stream := claudeErrorEvent("api_error", "Overloaded")

	_, apiErr := newClaudeStreamHandlerContext(t, types.RelayFormatClaude, stream)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorTypeClaudeError, apiErr.GetErrorType())
	assert.Equal(t, types.ErrorCode("api_error"), apiErr.GetErrorCode())
	assert.Equal(t, "Overloaded", apiErr.Error())
}

// TestClaudeStreamHandlerLeavesTheStreamAloneWithoutATranslation covers the
// handler on its own, where the relay package has registered nothing: the
// refusal keeps the error the handler raises for it, and the stream of events
// before it is streamed as always.
func TestClaudeStreamHandlerLeavesTheStreamAloneWithoutATranslation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	realTranslation := SetImageCeilingRefusal(nil)
	t.Cleanup(func() { SetImageCeilingRefusal(realTranslation) })
	stream := claudeTextChunk("before-the-ceiling") + claudeErrorEvent("invalid_request_error", claudeImageCeilingMessage)

	recorder, apiErr := newClaudeStreamHandlerContext(t, types.RelayFormatClaude, stream)

	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorTypeClaudeError, apiErr.GetErrorType())
	assert.Equal(t, types.ErrorCode("invalid_request_error"), apiErr.GetErrorCode())
	assert.Contains(t, recorder.Body.String(), "before-the-ceiling")
}
