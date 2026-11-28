package relay

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/systemone"
	"github.com/QuantumNous/new-api/relaykit/types"
)

// systemOnePassThroughStatuses are the upstream statuses that reach the client
// unchanged. They are the deterministic answers a caller can act on: a rejected
// body, an oversized one, an unsupported encoding, a rate limit. Turning any of
// them into a channel failure would report a request mistake as a server fault,
// and the retry loop would send the same unanswerable request to another
// channel.
var systemOnePassThroughStatuses = map[int]struct{}{
	http.StatusBadRequest:            {},
	http.StatusUnauthorized:          {},
	http.StatusForbidden:             {},
	http.StatusNotFound:              {},
	http.StatusRequestEntityTooLarge: {},
	http.StatusUnsupportedMediaType:  {},
	http.StatusUnprocessableEntity:   {},
	http.StatusTooManyRequests:       {},
}

// systemOneUpstreamError turns a non-2xx System One answer into the error the
// client reads. It returns nil when the status is not one the client should see
// unchanged, which leaves the call to service.RelayErrorHandler so the retry
// loop keeps its existing behaviour for an upstream fault.
//
// The body keeps its own shape when the upstream wrote it as the System One
// contract already writes it: a FastAPI validation array under "detail", or an
// object under "detail" with a message. Any other shape is read for a message
// and rebuilt, so a caller always reads "detail".
func systemOneUpstreamError(status int, body []byte, retryAfter string) *types.NewAPIError {
	if _, passThrough := systemOnePassThroughStatuses[status]; !passThrough {
		return nil
	}

	payload := systemOneErrorDocument(status, body)
	apiError := types.NewOpenAIError(
		&systemOneUpstreamRefusal{status: status, body: payload, retryAfter: retryAfter},
		types.ErrorCodeBadResponseStatusCode,
		status,
		types.ErrOptionWithSkipRetry(),
	)
	apiError.Metadata = payload
	return apiError
}

// systemOneUpstreamRefusal carries the document to write verbatim. The relay
// writers read Metadata for the body and this error for the status, so a client
// reads the upstream's own field locations instead of a sentence about a bad
// status code.
type systemOneUpstreamRefusal struct {
	status     int
	body       []byte
	retryAfter string
}

func (e *systemOneUpstreamRefusal) Error() string {
	var detail struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(e.body, &detail); err == nil {
		if message := systemOneDetailMessage(detail.Detail); message != "" {
			return message
		}
	}
	return "upstream answered " + strconv.Itoa(e.status)
}

// systemOneErrorDocument answers the body to write for the status: the upstream's
// own document when it already carries "detail", and a rebuilt one otherwise.
func systemOneErrorDocument(status int, body []byte) []byte {
	trimmed := strings.TrimSpace(string(body))
	if trimmed != "" {
		var document map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &document); err == nil {
			if rawDetail, exists := document["detail"]; exists && len(rawDetail) > 0 && string(rawDetail) != "null" {
				// Already the contract: an array of validation errors or an
				// object with the reason. Written back byte for byte.
				return []byte(trimmed)
			}
			// An OpenAI-shaped error from a gateway in front of the upstream.
			if rawError, exists := document["error"]; exists {
				if message := systemOneDetailMessage(rawError); message != "" {
					return systemOneDetailBytes(status, message)
				}
			}
		}
		if message := systemOneDetailMessage(json.RawMessage(trimmed)); message != "" {
			return systemOneDetailBytes(status, message)
		}
	}
	return systemOneDetailBytes(status, http.StatusText(status))
}

func systemOneDetailBytes(status int, message string) []byte {
	encoded, err := systemone.MarshalDetailDocument(systemone.ErrorTypeForStatus(status), message)
	if err != nil {
		return []byte(`{"detail":{"error_type":"server_error","message":"upstream refused the request"}}`)
	}
	return encoded
}

// systemOneDetailMessage reads the human sentence out of a JSON value: a string,
// an object with message/msg/detail, or an array whose first element carries one.
func systemOneDetailMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var object struct {
		Message string `json:"message"`
		Msg     string `json:"msg"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		switch {
		case object.Message != "":
			return object.Message
		case object.Msg != "":
			return object.Msg
		case object.Detail != "":
			return object.Detail
		}
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
		for _, item := range list {
			if message := systemOneDetailMessage(item); message != "" {
				return message
			}
		}
	}
	return ""
}
