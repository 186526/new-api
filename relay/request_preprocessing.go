package relay

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/image_compress"
	"github.com/QuantumNous/new-api/setting/image_compression_setting"
	"github.com/gin-gonic/gin"
)

type RequestPreprocessing struct {
	settings image_compression_setting.Settings
	prepared bool
	storage  common.BodyStorage
	meta     relaycommon.ImageCompressionMeta
	failure  *types.NewAPIError
}

func NewRequestPreprocessing(info *relaycommon.RelayInfo) *RequestPreprocessing {
	helper.PrepareAutoReasoning(info)
	return &RequestPreprocessing{settings: image_compression_setting.Get()}
}

func (processing *RequestPreprocessing) Close() {
	if processing.storage != nil {
		_ = processing.storage.Close()
	}
}

// ResetRequestAttemptObservations clears previous channel attribution, including
// when selection or an image ceiling rejects this attempt before conversion.
func ResetRequestAttemptObservations(info *relaycommon.RelayInfo) {
	if previous := info.ImageCompression; previous != nil && previous.Preprocessing != nil {
		info.ImageCompression = &relaycommon.ImageCompressionMeta{Preprocessing: previous.Preprocessing}
	} else {
		info.ImageCompression = nil
	}
	info.AutoReasoning.ResetAttempt()
}

// ApplyForAttempt checks the selected channel's ceiling on the baseline before
// installing the outbound representation. Restore before selecting another channel.
func (processing *RequestPreprocessing) ApplyForAttempt(c *gin.Context, info *relaycommon.RelayInfo) (func(), *types.NewAPIError) {
	restore := func() {}
	settings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	mapping := common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping)
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	channelName := common.GetContextKeyString(c, constant.ContextKeyChannelName)
	if err := CheckRequestImageLimitForModels(info.Request, info.RelayFormat, settings, mapping, channelID, channelName, info.OriginModelName); err != nil {
		return restore, err
	}
	if !processing.settings.Enabled {
		return restore, nil
	}
	attempt := &relaycommon.ImageCompressionMeta{Preprocessing: processing.meta.Preprocessing}
	info.ImageCompression = attempt
	switch {
	case !supportsImageCompression(info), !common.IsJSONMediaType(common.MediaTypeOf(c.GetHeader("Content-Type"))):
		attempt.Reason = "unsupported"
	case helper.RequestPassesThrough(c, info, settings):
		attempt.Reason = "passthrough"
	default:
		names, err := UpstreamModelNames(settings, mapping, info.OriginModelName)
		if err != nil || !processing.settings.Applies(channelID, names...) {
			attempt.Reason = "excluded"
			return restore, nil
		}
		if !processing.prepared {
			processing.prepared = true
			processing.prepare(c, info.RelayFormat)
		}
		if processing.failure == nil && processing.storage != nil {
			restore = processing.install(c, info)
		}
		*attempt = processing.meta
		return restore, processing.failure
	}
	return restore, nil
}

func supportsImageCompression(info *relaycommon.RelayInfo) bool {
	switch info.Request.(type) {
	case *dto.GeneralOpenAIRequest:
		return info.RelayMode == relayconstant.RelayModeChatCompletions
	case *dto.OpenAIResponsesRequest, *dto.ClaudeRequest, *dto.GeminiChatRequest:
		return true
	default:
		return false
	}
}

func (processing *RequestPreprocessing) prepare(c *gin.Context, format types.RelayFormat) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		processing.fail(err)
		return
	}
	raw, err := storage.Bytes()
	if err != nil {
		processing.fail(err)
		return
	}
	body, stats, err := image_compress.Rewrite(c.Request.Context(), processing.settings, format, raw, service.DownloadCompressionImage)
	processing.meta = relaycommon.ImageCompressionMeta{
		Processed: stats.Processed, SavedBytes: stats.Saved, ElapsedMS: stats.Elapsed, Skipped: stats.Skipped, Failed: stats.Failed,
		Preprocessing: &relaycommon.ImageCompressionWork{
			Processed: stats.Processed, SavedBytes: stats.Saved, ElapsedMS: stats.Elapsed, Skipped: stats.Skipped, Failed: stats.Failed,
		},
	}
	if err != nil {
		processing.fail(err)
		return
	}
	switch {
	case len(body) != 0:
		processing.storage, err = common.CreateBodyStorage(body)
		if err != nil {
			processing.fail(err)
			return
		}
		processing.meta.Reason = "compressed"
		processing.meta.TrackOutboundImages(raw, body, format)
	case stats.Failed > 0:
		processing.meta.Reason = "failed"
	default:
		processing.meta.Reason = "retained"
	}
}

func (processing *RequestPreprocessing) fail(err error) {
	processing.meta.Reason = "failed"
	if errors.Is(err, image_compress.ErrImageLimit) || errors.Is(err, context.DeadlineExceeded) {
		processing.meta.Reason = "budget"
	}
	processing.meta.Processed, processing.meta.SavedBytes = 0, 0
	if processing.settings.FailurePolicy != "reject_request" {
		return
	}
	status := http.StatusBadRequest
	if errors.Is(err, image_compress.ErrImageLimit) {
		status = http.StatusRequestEntityTooLarge
	}
	processing.failure = types.NewErrorWithStatusCode(errors.New("request image compression failed"), types.ErrorCodeInvalidRequest, status, types.ErrOptionWithSkipRetry())
}

func (processing *RequestPreprocessing) install(c *gin.Context, info *relaycommon.RelayInfo) func() {
	baseline, _ := c.Get(common.KeyBodyStorage)
	legacy, _ := c.Get(common.KeyRequestBody)
	body, length, getBody := c.Request.Body, c.Request.ContentLength, c.Request.GetBody
	headers := c.Request.Header
	request := info.Request
	restore := func() {
		c.Set(common.KeyBodyStorage, baseline)
		c.Set(common.KeyRequestBody, legacy)
		c.Request.Body, c.Request.ContentLength, c.Request.GetBody = body, length, getBody
		c.Request.Header = headers
		info.Request = request
	}
	c.Set(common.KeyBodyStorage, processing.storage)
	c.Set(common.KeyRequestBody, nil)
	c.Request.Body = io.NopCloser(processing.storage)
	c.Request.ContentLength = processing.storage.Size()
	c.Request.GetBody = processing.storage.NewReader
	c.Request.Header = headers.Clone()
	c.Request.Header.Set("Content-Length", strconv.FormatInt(c.Request.ContentLength, 10))
	rewritten, err := helper.GetAndValidateRequest(c, info.RelayFormat)
	if err != nil {
		restore()
		processing.fail(err)
		_ = processing.storage.Close()
		processing.storage = nil
		return func() {}
	}
	if original, ok := request.(*dto.OpenAIResponsesRequest); ok {
		// WebSocket normalization clears these HTTP fields before conversion.
		updated := rewritten.(*dto.OpenAIResponsesRequest)
		updated.Stream, updated.StreamOptions = original.Stream, original.StreamOptions
	}
	info.Request = rewritten
	return restore
}
