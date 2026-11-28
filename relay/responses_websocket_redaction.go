package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/middleware"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func restoreResponsesWSClientFrame(restorer *redaction.ResponseRestorer, message responsesWSMessage, eventType string) responsesWSMessage {
	if restorer == nil || message.kind != websocket.TextMessage {
		return message
	}
	switch eventType {
	case "error", "response.failed", "response.cancelled", "response.canceled":
		restorer.Discard()
	case "response.output_text.delta", "response.output_text.done", "response.content_part.done",
		"response.output_item.done", "response.completed", "response.done", "response.incomplete":
		message.body = restorer.Frame(message.body)
	}
	return message
}

func observeResponsesWSReasoning(meta *relaycommon.AutoReasoningMeta, payload []byte) {
	if meta == nil || meta.Reason == "" {
		return
	}
	// Reuse the bounded allowlist without changing the bytes sent on the socket.
	request, _ := http.NewRequest(http.MethodPost, "", bytes.NewReader(payload))
	meta.ObserveHTTPBody(request)
}

func protectedResponsesWSCancel(body []byte, streamID, responseID string, checkIDs func(...string) error) ([]byte, error) {
	invalid := errors.New("protected response.cancel requires known fields and an active stream or response ID")
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return nil, invalid
	}
	var control struct {
		Type       string `json:"type"`
		StreamID   string `json:"stream_id,omitempty"`
		ResponseID string `json:"response_id,omitempty"`
		EventID    string `json:"event_id,omitempty"`
	}
	for key, raw := range fields {
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" {
			return nil, invalid
		}
		switch key {
		case "type":
			if value != "response.cancel" {
				return nil, invalid
			}
			control.Type = value
		case "stream_id":
			if value != streamID {
				return nil, invalid
			}
			control.StreamID = value
		case "response_id":
			if value != responseID {
				return nil, invalid
			}
			control.ResponseID = value
		case "event_id":
			if !validProtectedResponsesWSControlID(value) {
				return nil, invalid
			}
			control.EventID = value
		default:
			return nil, invalid
		}
	}
	if control.Type == "" || (control.StreamID == "" && control.ResponseID == "") {
		return nil, invalid
	}
	if checkIDs(control.StreamID, control.ResponseID, control.EventID) != nil {
		return nil, errors.New("response.cancel identifiers cannot be sent under the request protection policy")
	}
	return json.Marshal(control)
}

func responsesWSControlIDCheck(ctx *gin.Context) func(...string) error {
	return func(values ...string) error {
		return middleware.CheckProtectedControlIdentifiers(ctx, values...)
	}
}

func protectedResponsesWSCreateIDs(body []byte, checkIDs func(...string) error) error {
	invalid := errors.New("response.create identifiers cannot be sent under the request protection policy")
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return invalid
	}
	var identifiers []string
	for field, raw := range fields {
		switch strings.ToLower(field) {
		case "stream_id", "previous_response_id", "event_id", "response_id":
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return invalid
			}
			identifiers = append(identifiers, value)
		}
	}
	if checkIDs(identifiers...) != nil {
		return invalid
	}
	return nil
}

func validProtectedResponsesWSControlID(value string) bool {
	if len(value) > 256 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			return false
		}
	}
	return true
}
