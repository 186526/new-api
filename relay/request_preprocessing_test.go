package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/image_compression_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImagePreprocessingSurvivesRetryToPassthrough(t *testing.T) {
	work := &relaycommon.ImageCompressionWork{Processed: 1, SavedBytes: 456}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIResponses,
		Request:     &dto.OpenAIResponsesRequest{},
		ImageCompression: &relaycommon.ImageCompressionMeta{
			Preprocessing: work, Reason: "compressed", OutboundStatus: "observed",
			OutboundCompressed: common.GetPointer(1), OutboundSavedBytes: common.GetPointer(456),
		},
		AutoReasoning: &relaycommon.AutoReasoningMeta{
			Scale: 3, Reason: "applied", OutboundStatus: "observed",
			OutboundFields: map[string]any{"reasoning.effort": "high"},
		},
	}
	ResetRequestAttemptObservations(info)
	reset := info.RequestObservation()
	require.Equal(t, work, reset.Image.Preprocessing)
	require.Empty(t, reset.Image.Reason)
	require.Empty(t, reset.Image.OutboundStatus)
	require.Nil(t, reset.Image.OutboundCompressed)
	require.Equal(t, 3, reset.AutoScale)
	require.Empty(t, reset.AutoReason)
	require.Empty(t, reset.AutoOutboundFields)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
	settings := image_compression_setting.Defaults()
	settings.Enabled = true
	processing := &RequestPreprocessing{settings: settings, prepared: true, meta: relaycommon.ImageCompressionMeta{Preprocessing: work}}
	restore, err := processing.ApplyForAttempt(ctx, info)
	defer restore()
	require.Nil(t, err)
	final := info.RequestObservation()
	require.Equal(t, "passthrough", final.Image.Reason)
	require.Equal(t, work, final.Image.Preprocessing)
	require.Nil(t, final.Image.OutboundCompressed)
	require.Nil(t, final.Image.OutboundSavedBytes)
}
