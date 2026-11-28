package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/gin-gonic/gin"
)

const maxResponseRedactionEvent = redaction.MaxBodyBytes

var errRestoredResponseWrite = errors.New("could not write restored HTTP response")

// StartHTTPResponseRedaction wraps one upstream attempt. Its returned function
// drains the literal tail on confirmed clean EOF and restores the original writer.
func StartHTTPResponseRedaction(ctx *gin.Context, relayFormat types.RelayFormat) (finish func(cleanEOF bool)) {
	format := requestRedactionFormat(ctx)
	switch {
	case relayFormat == types.RelayFormatOpenAI && format == redaction.Chat:
	case relayFormat == types.RelayFormatClaude && format == redaction.Claude:
	case relayFormat == types.RelayFormatOpenAIResponses && format == redaction.Responses && ctx.FullPath() == "/v1/responses":
	case relayFormat == types.RelayFormatGemini && format == redaction.Gemini &&
		!strings.HasSuffix(ctx.Param("path"), ":batchGenerateContent"):
	default:
		return func(bool) {}
	}
	restorer := NewResponseRedaction(ctx, format)
	if restorer == nil {
		return func(bool) {}
	}
	original := ctx.Writer
	writer := &httpResponseRedactionWriter{ResponseWriter: original, restorer: restorer}
	ctx.Writer = writer
	return func(cleanEOF bool) {
		defer func() {
			writer.restorer.Discard()
			ctx.Writer = original
		}()
		if cleanEOF {
			writer.finishSSE(ctx)
		}
	}
}

func (writer *httpResponseRedactionWriter) finishSSE(ctx *gin.Context) {
	if writer.bypass || writer.failed || len(writer.event) != 0 || ctx.Request.Context().Err() != nil ||
		common.MediaTypeOf(writer.Header().Get("Content-Type")) != "text/event-stream" || !writer.identityEncoding() {
		return
	}
	if writer.writeTailFrames(writer.restorer.Drain()) == nil {
		writer.ResponseWriter.Flush()
	}
}

type httpResponseRedactionWriter struct {
	gin.ResponseWriter
	restorer *redaction.ResponseRestorer
	event    []byte
	bypass   bool
	failed   bool
}

func (writer *httpResponseRedactionWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *httpResponseRedactionWriter) WriteString(text string) (int, error) {
	return writer.Write([]byte(text))
}

func (writer *httpResponseRedactionWriter) Write(body []byte) (int, error) {
	if writer.bypass || writer.Status() < 200 || writer.Status() >= 300 || !writer.identityEncoding() {
		return writer.ResponseWriter.Write(body)
	}
	mediaType := common.MediaTypeOf(writer.Header().Get("Content-Type"))
	if mediaType == "text/event-stream" {
		return writer.writeSSE(body)
	}
	// Relay JSON writers send whole documents. Once committed, the response's
	// length and integrity headers cannot be adjusted safely.
	if writer.Written() || (mediaType != "" && !common.IsJSONMediaType(mediaType)) || len(body) > redaction.MaxBodyBytes {
		return writer.ResponseWriter.Write(body)
	}
	restored := writer.restorer.JSON(body)
	if bytes.Equal(restored, body) {
		return writer.ResponseWriter.Write(body)
	}
	writer.invalidateRepresentationHeaders()
	writer.Header().Set("Content-Length", strconv.Itoa(len(restored)))
	if err := writer.writeRestored(restored); err != nil {
		return 0, err
	}
	return len(body), nil
}

func (writer *httpResponseRedactionWriter) identityEncoding() bool {
	for _, header := range writer.Header().Values("Content-Encoding") {
		for _, coding := range strings.Split(header, ",") {
			coding = strings.TrimSpace(coding)
			if coding != "" && !strings.EqualFold(coding, "identity") {
				return false
			}
		}
	}
	return true
}

func (writer *httpResponseRedactionWriter) invalidateRepresentationHeaders() {
	header := writer.Header()
	for _, key := range []string{"Content-Length", "ETag", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest"} {
		header.Del(key)
	}
	header.Set("Cache-Control", "private, no-store")
}

func (writer *httpResponseRedactionWriter) writeRestored(body []byte) error {
	// A downstream writer error must never reflect restored bytes to relay logs.
	if written, err := writer.ResponseWriter.Write(body); err != nil || written != len(body) {
		return errRestoredResponseWrite
	}
	return nil
}

func (writer *httpResponseRedactionWriter) writeSSE(body []byte) (int, error) {
	if !writer.Written() {
		writer.invalidateRepresentationHeaders()
	}
	consumed := 0
	for len(body) != 0 {
		length := len(body)
		if newline := bytes.IndexByte(body, '\n'); newline >= 0 {
			length = newline + 1
		}
		if length > maxResponseRedactionEvent-len(writer.event) {
			if err := writer.disableSSE(); err != nil {
				return consumed, err
			}
			written, err := writer.ResponseWriter.Write(body)
			return consumed + written, err
		}
		writer.event = append(writer.event, body[:length]...)
		consumed += length
		body = body[length:]
		if bytes.HasSuffix(writer.event, []byte("\n\n")) || bytes.HasSuffix(writer.event, []byte("\n\r\n")) ||
			bytes.Equal(writer.event, []byte("\n")) || bytes.Equal(writer.event, []byte("\r\n")) {
			if err := writer.emitSSE(); err != nil {
				return consumed, err
			}
		}
	}
	return consumed, nil
}

func (writer *httpResponseRedactionWriter) emitSSE() error {
	event := writer.event
	writer.event = writer.event[:0]
	start, end, ok := responseRedactionSSEPayload(event)
	if responseRedactionSSEFailure(event) || (ok && redaction.FailedResponseFrame(event[start:end])) {
		writer.failed = true
		writer.restorer.Discard()
	}
	if !ok || writer.failed {
		_, err := writer.ResponseWriter.Write(event)
		return err
	}
	payload := event[start:end]
	if err := writer.writeTailFrames(writer.restorer.BeforeFrame(payload)); err != nil {
		return err
	}
	restored := writer.restorer.Frame(payload)
	if bytes.Equal(payload, restored) {
		_, err := writer.ResponseWriter.Write(event)
		return err
	}
	output := make([]byte, 0, len(event)-len(payload)+len(restored))
	output = append(output, event[:start]...)
	output = append(output, restored...)
	output = append(output, event[end:]...)
	return writer.writeRestored(output)
}

func (writer *httpResponseRedactionWriter) writeTailFrames(frames [][]byte) error {
	for _, frame := range frames {
		var event []byte
		if bytes.Contains(frame, []byte(`"type":"content_block_delta"`)) {
			event = append(event, "event: content_block_delta\n"...)
		}
		event = append(event, "data: "...)
		event = append(event, frame...)
		event = append(event, '\n', '\n')
		if err := writer.writeRestored(event); err != nil {
			return err
		}
	}
	return nil
}

func responseRedactionSSEFailure(event []byte) bool {
	for line := range bytes.SplitSeq(event, []byte("\n")) {
		if value, ok := bytes.CutPrefix(line, []byte("event:")); ok {
			switch string(bytes.TrimSpace(value)) {
			case "error", "response.failed", "response.cancelled", "response.canceled":
				return true
			}
		}
	}
	return false
}

// The relay renders one JSON data line per event. Leave multi-line data and
// error events intact rather than interpreting only a portion of their body.
func responseRedactionSSEPayload(event []byte) (start, end int, eligible bool) {
	for offset := 0; offset < len(event); {
		line, _, found := bytes.Cut(event[offset:], []byte("\n"))
		if !found {
			break
		}
		lineLength := len(line)
		line = bytes.TrimSuffix(line, []byte("\r"))
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			switch string(bytes.TrimSpace(line[len("event:"):])) {
			case "error", "response.failed", "response.cancelled", "response.canceled":
				return 0, 0, false
			}
		case bytes.HasPrefix(line, []byte("data:")):
			if eligible {
				return 0, 0, false
			}
			start, end, eligible = offset+len("data:"), offset+len(line), true
			if start < end && event[start] == ' ' {
				start++
			}
		}
		offset += lineLength + 1
	}
	return
}

func (writer *httpResponseRedactionWriter) disableSSE() error {
	writer.bypass = true
	writer.restorer.Discard()
	pending := writer.event
	writer.event = nil
	if len(pending) == 0 {
		return nil
	}
	written, err := writer.ResponseWriter.Write(pending)
	if err == nil && written != len(pending) {
		return io.ErrShortWrite
	}
	return err
}

func (writer *httpResponseRedactionWriter) Flush() {
	// A flush with unfinished framing is outside the relay's event contract.
	// Preserve the byte stream, and stop carrying restoration state forward.
	if len(writer.event) != 0 {
		_ = writer.disableSSE()
	}
	writer.ResponseWriter.Flush()
}
