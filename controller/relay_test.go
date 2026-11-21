package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const (
	relayErrorCommittedAudio = "committed audio"
	relayErrorCommittedJSON  = "committed json"
	relayErrorCommittedSSE   = "committed sse"
	relayErrorUncommitted    = "uncommitted"
)

// relayErrorCommittedBodies is what the client already read when the relay
// path failed. A scenario without one fails before any byte went out.
var relayErrorCommittedBodies = map[string]string{
	relayErrorCommittedAudio: "ID3-audio-bytes",
	relayErrorCommittedJSON:  `{"id":"partial`,
	relayErrorCommittedSSE:   "event: message_start\ndata: {}\n\n",
}

var relayErrorCommittedContentTypes = map[string]string{
	relayErrorCommittedAudio: "audio/mpeg",
	relayErrorCommittedJSON:  "application/json",
	relayErrorCommittedSSE:   "text/event-stream",
}

const relayErrorMessage = "upstream closed the body after 200"

var relayErrorFormats = []types.RelayFormat{
	types.RelayFormatOpenAI,
	types.RelayFormatClaude,
	types.RelayFormatOpenAIResponses,
	types.RelayFormatGemini,
}

// sentinelWriter takes the response back at the first write the relay path
// makes. gin's WriteHeaderNow is held back until then, so the relay path is the
// one that commits the stream headers, exactly as it is in production; the
// error frame that follows goes to the recorder.
type sentinelWriter struct {
	gin.ResponseWriter
	once    *sync.Once
	restore func(committed bool)
}

func (writer *sentinelWriter) WriteHeaderNow() {}

func (writer *sentinelWriter) Write(data []byte) (int, error) {
	writer.once.Do(func() { writer.restore(true) })
	return writer.ResponseWriter.Write(data)
}

// relayErrorFrameResult is what the client reads after a failed relay request.
type relayErrorFrameResult struct {
	status      int
	contentType string
	// committedContentType is the header the client received with its first
	// byte, before any frame the error path writes.
	committedContentType string
	body                 string
	committed            bool
}

// relayErrorFrameThrough runs one failed relay path up to the error frame. The
// path is the probe passed as committed; the frame is writeRelayErrorFrame,
// the same call Relay's defer makes.
func relayErrorFrameThrough(t *testing.T, format types.RelayFormat, scenario string, stream bool) relayErrorFrameResult {
	t.Helper()
	body := `{"model":"mistral-large-latest","messages":[{"role":"user","content":"hi"}]}`
	if stream {
		body = `{"model":"mistral-large-latest","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorded := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorded)
	c.Request = request

	result := relayErrorFrameResult{}
	once := &sync.Once{}
	sentinel := &sentinelWriter{ResponseWriter: c.Writer, once: once}
	client := c.Writer
	sentinel.restore = func(committed bool) {
		result.committed = committed
		// The header the client already received, read before the frame.
		result.committedContentType = sentinel.ResponseWriter.Header().Get("Content-Type")
		c.Writer = client
	}
	c.Writer = sentinel
	if partial, ok := relayErrorCommittedBodies[scenario]; ok {
		c.Header("Content-Type", relayErrorCommittedContentTypes[scenario])
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte(partial))
	}
	once.Do(func() { sentinel.restore(false) })

	writeRelayErrorFrame(c, nil, format, types.NewErrorWithStatusCode(errors.New(relayErrorMessage), types.ErrorCodeBadResponse, http.StatusBadGateway))

	result.status = recorded.Code
	result.contentType = recorded.Header().Get("Content-Type")
	result.body = recorded.Body.String()
	return result
}

func TestRelayErrorFrameAfterCommittedBytes(t *testing.T) {
	for _, format := range relayErrorFormats {
		for _, scenario := range []string{relayErrorCommittedAudio, relayErrorCommittedJSON, relayErrorCommittedSSE} {
			t.Run(string(format)+"/"+scenario, func(t *testing.T) {
				result := relayErrorFrameThrough(t, format, scenario, true)
				require.True(t, result.committed)
				require.Equal(t, relayErrorCommittedContentTypes[scenario], result.committedContentType, "the client received this Content-Type with its first byte")
				require.Contains(t, result.body, relayErrorCommittedBodies[scenario], "the bytes the client already read stay first")
				assertOneErrorFrame(t, format, result.body)
				if scenario != relayErrorCommittedSSE {
					// A recorder answers what it cannot detect with
					// text/plain, so the committed type itself cannot be read
					// back; the frame excludes that answer.
					require.NotEqual(t, "text/plain", result.contentType, "the error frame is not read as plain text: %q", result.body)
				}
			})
		}
	}
}

func TestRelayErrorFrameWithoutCommittedBytes(t *testing.T) {
	for _, format := range relayErrorFormats {
		t.Run(string(format), func(t *testing.T) {
			result := relayErrorFrameThrough(t, format, relayErrorUncommitted, false)
			require.False(t, result.committed)
			require.Equal(t, http.StatusBadGateway, result.status)
			require.Contains(t, result.contentType, "application/json")
			require.NotContains(t, result.body, "text/event-stream")
			require.Contains(t, result.body, relayErrorMessage)
			assertOneJSONErrorBody(t, format, result.body)
		})
	}
}

// assertOneErrorFrame requires the frame the format's stream sends and nothing
// else: no bare JSON document appended to the body.
func assertOneErrorFrame(t *testing.T, format types.RelayFormat, body string) {
	t.Helper()
	require.Contains(t, body, relayErrorMessage)
	switch format {
	case types.RelayFormatClaude:
		require.Contains(t, body, "event: error\n")
		require.NotContains(t, body, "data: [DONE]")
	case types.RelayFormatOpenAIResponses:
		require.Contains(t, body, `data: {"response":{"error":`)
		require.Contains(t, body, `"type":"response.failed"`)
		require.NotContains(t, body, "data: [DONE]")
	default:
		require.Contains(t, body, `data: {"error":`)
		require.Contains(t, body, "data: [DONE]")
	}
	requireNoBareJSON(t, body)
}

// assertOneJSONErrorBody requires the whole body to be the format's error
// document and nothing more.
func assertOneJSONErrorBody(t *testing.T, format types.RelayFormat, body string) {
	t.Helper()
	require.True(t, strings.HasPrefix(body, "{"), "the body starts with the error document: %q", body)
	require.True(t, strings.HasSuffix(body, "}"), "the body ends with the error document: %q", body)
	switch format {
	case types.RelayFormatClaude:
		require.Contains(t, body, `"type":"error"`)
		require.Contains(t, body, `"error":`)
	default:
		require.Contains(t, body, `{"error":`)
	}
	require.Equal(t, 1, strings.Count(body, relayErrorMessage), "the error is told once: %q", body)
}

// requireNoBareJSON requires that the body does not end on a JSON object the
// client would have to read outside a frame.
func requireNoBareJSON(t *testing.T, body string) {
	t.Helper()
	require.False(t, strings.HasSuffix(strings.TrimSpace(body), "}") && !strings.Contains(body, "data: "),
		"the error never arrives as a bare JSON body: %q", body)
	require.NotContains(t, body, "\n{", "no unwrapped JSON object follows a frame: %q", body)
}
