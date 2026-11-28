package common

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/image_compress"
	"github.com/stretchr/testify/require"
)

func TestImageObservationConfirmsCompressionInConvertedOutboundBody(t *testing.T) {
	meta := &ImageCompressionMeta{Reason: "compressed", Processed: 1, SavedBytes: 24}
	meta.TrackOutboundImages(
		[]byte(`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW5wdXQ="}}]}]}`),
		[]byte(`{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,b3V0"}}]}]}`),
		types.RelayFormatOpenAI,
	)
	const converted = `{"messages":[{"content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"b3V0"}}]}]}`
	request, err := http.NewRequest(http.MethodPost, "http://localhost", strings.NewReader(converted))
	require.NoError(t, err)
	meta.ObserveHTTPBody(request, types.RelayFormatClaude)
	require.Equal(t, "observed", meta.OutboundStatus)
	require.NotNil(t, meta.OutboundCompressed)
	require.Equal(t, 1, *meta.OutboundCompressed)
	require.NotNil(t, meta.OutboundSavedBytes)
	require.Equal(t, 24, *meta.OutboundSavedBytes)
	read, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	require.Equal(t, converted, string(read))
}

func TestImageObservationDoesNotCreditDroppedCompressedOccurrences(t *testing.T) {
	meta := &ImageCompressionMeta{Reason: "compressed", Processed: 1, SavedBytes: 24}
	const existing = `{"type":"input_image","image_url":"data:image/jpeg;base64,b3V0"}`
	const original = `{"type":"input_image","image_url":"data:image/png;base64,aW5wdXQ="}`
	meta.TrackOutboundImages(
		[]byte(`{"input":[`+existing+`,`+original+`]}`),
		[]byte(`{"input":[`+existing+`,`+existing+`]}`),
		types.RelayFormatOpenAIResponses,
	)
	meta.ObserveOutboundJSON([]byte(`{"type":"response.create","input":[`+existing+`]}`), types.RelayFormatOpenAIResponses)
	require.Equal(t, "unconfirmed", meta.OutboundStatus)
	require.NotNil(t, meta.OutboundCompressed)
	require.Zero(t, *meta.OutboundCompressed)
	require.Nil(t, meta.OutboundSavedBytes)
	meta.ObserveOutboundJSON(make([]byte, image_compress.OutboundObservationLimit+1), types.RelayFormatOpenAIResponses)
	require.Equal(t, "body_too_large", meta.OutboundStatus)
	require.Nil(t, meta.OutboundCompressed)
}
