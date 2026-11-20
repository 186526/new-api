package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

// claudeErrorEnvelopeRefusal reads a non-200 answer of a channel that speaks
// the Anthropic Messages API as the image-ceiling envelope it may be, and raises
// it with the status the upstream answered with. That status is the whole
// condition the image-ceiling translation reads, so a refusal the upstream
// answered with a 400 stays a 400 instead of the bad_response_status_code
// service.RelayErrorHandler raises for a body it does not unwrap. It is read by
// both relays that talk to such a channel: ClaudeHelper for a request already in
// the Claude format, and ResponsesHelper for a /v1/responses request it converts.
//
// Only a message that names the ceiling is read. Every other answer goes back to
// service.RelayErrorHandler untouched, so an error of another kind keeps the
// code, the source and the retry and health decisions it had before this reader
// existed: raising a rate limit or an admission error as the envelope would
// turn a reply new-api skips into one it retries, and a channel fault into one
// that disables the channel.
//
// An upstream may also write its envelope as an event stream, and that shape is
// read too, so the answer is taken as a whole and put back: a stream either
// reader leaves alone is scanned as it was written, byte for byte.
func claudeErrorEnvelopeRefusal(httpResp *http.Response, info *relaycommon.RelayInfo) *types.NewAPIError {
	return channelErrorEnvelopeRefusal(httpResp, info, constant.APITypeAnthropic)
}

// channelErrorEnvelopeRefusal is claudeErrorEnvelopeRefusal read for the given
// channel API type: an answer of another provider is left to
// service.RelayErrorHandler, whatever the route asked for. A Claude channel
// answers every route it serves with the envelope of the Messages API, so the
// relay path that converts a request into Claude (ClaudeHelper) and the one that
// converts a Claude channel's answer into another format (ResponsesHelper) read
// it the same way.
func channelErrorEnvelopeRefusal(httpResp *http.Response, info *relaycommon.RelayInfo, apiType int) *types.NewAPIError {
	if info == nil || info.ApiType != apiType {
		return nil
	}
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil
	}
	httpResp.Body = io.NopCloser(bytes.NewReader(body))
	return claudeImageCeilingEnvelopeErrorOf(claudeEnvelopeAnswer(string(body)), httpResp.StatusCode)
}

// claudeEnvelopeAnswer is the Claude error envelope an answer carries: the body
// itself, or the payload of the last data line of an answer written as an event
// stream.
func claudeEnvelopeAnswer(body string) string {
	if strings.HasPrefix(strings.TrimSpace(body), "data:") || strings.Contains(body, "\ndata:") {
		var last string
		for _, line := range strings.Split(body, "\n") {
			if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
				last = strings.TrimSpace(data)
			}
		}
		return last
	}
	return body
}

// claudeImageCeilingEnvelopeErrorOf raises the error an envelope carries when its
// message names the upstream image ceiling, and returns nil for every other
// document: one that does not unpack as an envelope, one without an error type,
// and one that describes any other failure.
func claudeImageCeilingEnvelopeErrorOf(document string, statusCode int) *types.NewAPIError {
	var claudeResponse dto.ClaudeResponse
	if common.Unmarshal([]byte(document), &claudeResponse) != nil {
		return nil
	}
	claudeError := claudeResponse.GetClaudeError()
	if claudeError == nil || claudeError.Type == "" || !NamesUpstreamImageLimit(claudeError.Message) {
		return nil
	}
	return types.WithClaudeError(*claudeError, statusCode)
}

func ClaudeHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {

	info.InitChannelMeta(c)

	claudeReq, ok := info.Request.(*dto.ClaudeRequest)

	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected *dto.ClaudeRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(claudeReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to ClaudeRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}
	if err := helper.ApplyReasoningModelSuffix(c, info, request); err != nil {
		return newConvertRequestFailedError(c, info, err)
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	if info.ChannelSetting.SystemPrompt != "" {
		if request.System == nil {
			request.SetStringSystem(info.ChannelSetting.SystemPrompt)
		} else if info.ChannelSetting.SystemPromptOverride {
			common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
			if request.IsStringSystem() {
				existing := strings.TrimSpace(request.GetStringSystem())
				if existing == "" {
					request.SetStringSystem(info.ChannelSetting.SystemPrompt)
				} else {
					request.SetStringSystem(info.ChannelSetting.SystemPrompt + "\n" + existing)
				}
			} else {
				systemContents := request.ParseSystem()
				newSystem := dto.ClaudeMediaMessage{Type: dto.ContentTypeText}
				newSystem.SetText(info.ChannelSetting.SystemPrompt)
				if len(systemContents) == 0 {
					request.System = []dto.ClaudeMediaMessage{newSystem}
				} else {
					request.System = append([]dto.ClaudeMediaMessage{newSystem}, systemContents...)
				}
			}
		}
	}

	if !model_setting.GetGlobalSettings().PassThroughRequestEnabled &&
		!info.ChannelSetting.PassThroughBodyEnabled &&
		service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
		usage, newApiErr := textRequestViaResponses(c, info, adaptor, request)
		if newApiErr != nil {
			return newApiErr
		}

		service.PostTextConsumeQuota(c, info, usage, nil)
		return nil
	}

	var requestBody io.Reader
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		requestBody = common.NewReplayableBodyReader(storage)
	} else {
		convertedRequest, err := adaptor.ConvertClaudeRequest(c, info, request)
		if err != nil {
			return newConvertRequestFailedError(c, info, err)
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
		jsonData, err := common.Marshal(convertedRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// remove disabled fields for Claude API
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}

		// apply param override
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return newAPIErrorFromParamOverride(err)
			}
		}

		logger.LogDebug(c, "requestBody: %s", jsonData)
		body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer closer.Close()
		jsonData = nil
		requestBody = body
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	var httpResp *http.Response
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK {
			// A refusal written as the image-ceiling envelope of the Messages
			// API is read here, with the status the upstream answered with,
			// which is what the retry loop recognizes the ceiling by; every
			// other failed answer, an envelope of another kind included, is
			// read the way it always was.
			newAPIError = claudeErrorEnvelopeRefusal(httpResp, info)
			if newAPIError == nil {
				newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			}
			// reset status code 重置状态码
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
	return nil
}
