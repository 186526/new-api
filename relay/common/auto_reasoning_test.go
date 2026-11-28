package common

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObserveHTTPBodyPreservesBodyAndRecordsOnlyReasoningFields(t *testing.T) {
	const raw = `{"thinking":{"type":"disabled","budget_tokens":0},"reasoning_effort":"private","enable_thinking":false,"input":"private"}`
	request, err := http.NewRequest(http.MethodPost, "http://localhost", strings.NewReader(raw))
	require.NoError(t, err)
	meta := &AutoReasoningMeta{Reason: "applied"}
	meta.ObserveHTTPBody(request)
	require.Equal(t, map[string]any{"thinking.type": "disabled", "thinking.budget_tokens": float64(0), "enable_thinking": false}, meta.OutboundFields)
	body, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Equal(t, raw, string(body))
	require.Equal(t, "observed", meta.OutboundStatus)
	meta.ResetAttempt()
	require.Nil(t, meta.OutboundFields)
	require.Empty(t, meta.OutboundStatus)
}

func TestObserveOutboundJSONDistinguishesMissingFromUnknown(t *testing.T) {
	meta := &AutoReasoningMeta{Reason: "applied"}
	meta.ObserveOutboundJSON([]byte(`{"input":"example"}`))
	require.Equal(t, "observed", meta.OutboundStatus)
	require.NotNil(t, meta.OutboundFields)
	require.Empty(t, meta.OutboundFields)
	meta.ObserveOutboundJSON([]byte(`{"input":`))
	require.Equal(t, "invalid_json", meta.OutboundStatus)
	require.Nil(t, meta.OutboundFields)
	meta.ObserveOutboundJSON(make([]byte, maxObservedReasoningBodyBytes+1))
	require.Equal(t, "body_too_large", meta.OutboundStatus)
}
