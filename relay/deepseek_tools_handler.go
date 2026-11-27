package relay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// DeepSeekToolsHelper relays /v1/tools/search and /v1/tools/fetch through the
// websearch pipeline. The DeepSeek upstream speaks both contracts itself, so it
// is the native candidate and its answer is forwarded byte for byte; a channel
// bound to another provider, or one that can only be reached through a global
// credential, is served by rendering instead.
//
// Everything that shapes the request for this channel stays here: the model
// mapping, the parameter override, and the adaptor's URL and headers. The
// pipeline never learns which channel type it is serving.
func DeepSeekToolsHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	request_, ok := info.Request.(*dto.DeepSeekToolsRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected *dto.DeepSeekToolsRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	err := helper.ModelMappedHelper(c, info, request_)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	jsonData, err := buildAlphaSearchRequestBody(request_.RawBody, info.OriginModelName, info.UpstreamModelName)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}

	logger.LogDebug(c, "requestBody: %s", jsonData)

	contract := abstract.ContractToolsSearch
	fetch := info.RelayMode == relayconstant.RelayModeToolsFetch
	if fetch {
		contract = abstract.ContractToolsFetch
	}

	query := request.Query{
		Contract:    contract,
		Text:        gjson.GetBytes(jsonData, "query").String(),
		URL:         gjson.GetBytes(jsonData, "url").String(),
		Count:       int(gjson.GetBytes(jsonData, "count").Int()),
		NeedContent: gjson.GetBytes(jsonData, "need_content").Bool(),
		Raw:         jsonData,
	}
	target, prepare, buildErr := channelUpstream(c, info)
	if buildErr != nil {
		return types.NewOpenAIError(buildErr, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	query.Endpoint, query.Prepare = target, prepare

	channel := websearchChannelContext(c)
	websearch.LogCandidates(c.Request.Context(), channel, contract)

	var answer websearch.Answer
	if fetch {
		answer, err = websearch.Fetch(c.Request.Context(), channel, query)
	} else {
		answer, err = websearch.Search(c.Request.Context(), channel, query)
	}
	if err != nil {
		if errors.Is(err, websearch.ErrUnsupportedChannel) {
			// Allow retry onto another channel that may support this endpoint.
			return types.NewError(
				fmt.Errorf("channel does not support %s", c.Request.URL.Path),
				types.ErrorCodeInvalidRequest,
			)
		}
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	return writeWebsearchAnswer(c, info, answer, func(body []byte) bool {
		// DeepSeek bills a successful request only when it returns something: a
		// non-empty results array for Search, page content for Fetch. A billable
		// call settles at the routing name's request price; any other response
		// releases the reservation.
		if fetch {
			return gjson.GetBytes(body, "content").String() != ""
		}
		results := gjson.GetBytes(body, "results")
		return results.IsArray() && len(results.Array()) > 0
	})
}

// writeWebsearchAnswer writes one pipeline answer and settles the charge. A
// non-2xx answer is rebuilt into a response the shared error handler can read,
// so status_code_mapping and its message rewriting stay in one place.
func writeWebsearchAnswer(c *gin.Context, info *relaycommon.RelayInfo, answer websearch.Answer, billable func([]byte) bool) *types.NewAPIError {
	if answer.StatusCode < 200 || answer.StatusCode >= 300 {
		resp := &http.Response{
			StatusCode: answer.StatusCode,
			Header:     answer.Header,
			Body:       http.NoBody,
			Request:    c.Request,
		}
		resp.Body = io.NopCloser(bytes.NewReader(answer.Body))
		newAPIError := service.RelayErrorHandler(c.Request.Context(), resp, false)
		service.ResetStatusCode(newAPIError, c.GetString("status_code_mapping"))
		return newAPIError
	}

	if answer.ContentType != "" {
		c.Writer.Header().Set("Content-Type", answer.ContentType)
	}
	service.IOCopyBytesGracefully(c, &http.Response{
		StatusCode: answer.StatusCode,
		Header:     answer.Header,
	}, answer.Body)

	if !billable(answer.Body) {
		if info.Billing != nil {
			info.Billing.Refund(c)
		}
		return nil
	}
	service.PostTextConsumeQuota(c, info, &dto.Usage{}, nil)
	return nil
}

// channelUpstream resolves the URL and the header preparation this channel's
// adaptor applies. Passing them into the pipeline is what keeps every
// channel-specific credential, base URL and header override working without the
// pipeline learning any channel type.
func channelUpstream(c *gin.Context, info *relaycommon.RelayInfo) (string, func(*http.Request) error, error) {
	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return "", nil, fmt.Errorf("invalid api type: %d", info.ApiType)
	}
	adaptor.Init(info)

	target, err := adaptor.GetRequestURL(info)
	if err != nil {
		return "", nil, fmt.Errorf("get request url failed: %w", err)
	}
	prepare := func(req *http.Request) error {
		headers := req.Header
		if err := adaptor.SetupRequestHeader(c, &headers, info); err != nil {
			return fmt.Errorf("setup request header failed: %w", err)
		}
		return relaychannel.ApplyHeaderOverride(info, c, req)
	}
	return target, prepare, nil
}

// websearchChannelContext assembles what the pipeline needs from the attempt's
// context. The channel object the relay layer holds is a stub without settings,
// so the settings come from the entry the distributor stored.
func websearchChannelContext(c *gin.Context) abstract.ChannelContext {
	channel := abstract.ChannelContext{
		ChannelID:   common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		ChannelType: common.GetContextKeyInt(c, constant.ContextKeyChannelType),
		BaseURL:     common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl),
		Key:         common.GetContextKeyString(c, constant.ContextKeyChannelKey),
	}
	if settings, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting); ok {
		channel.Settings = settings
	}
	return channel
}
