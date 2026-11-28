package relay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
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
	"github.com/QuantumNous/new-api/service/websearch/response"
	"github.com/QuantumNous/new-api/setting/websearch_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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

	jsonData := request_.RawBody

	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}

	contract := abstract.ContractToolsSearch
	fetch := info.RelayMode == relayconstant.RelayModeToolsFetch
	if fetch {
		contract = abstract.ContractToolsFetch
	} else if websearch_setting.Get().FallbackEnabled && !gjson.GetBytes(jsonData, "count").Exists() {
		jsonData, err = sjson.SetBytes(jsonData, "count", websearch_setting.DefaultSearchResultCount)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}

	query := request.Query{
		Contract:    contract,
		Text:        gjson.GetBytes(jsonData, "query").String(),
		URL:         gjson.GetBytes(jsonData, "url").String(),
		Count:       int(gjson.GetBytes(jsonData, "count").Int()),
		NeedContent: gjson.GetBytes(jsonData, "need_content").Bool(),
		Raw:         jsonData,
	}
	if apiErr := prepareWebsearchAttempt(c, info, &query); apiErr != nil {
		return apiErr
	}

	channel := websearchChannelContext(c)
	websearch.LogCandidates(c.Request.Context(), channel, contract)

	var answer websearch.Answer
	if fetch {
		answer, err = websearch.Fetch(c.Request.Context(), channel, query)
	} else {
		answer, err = websearch.Search(c.Request.Context(), channel, query)
	}
	recordWebsearchResult(info, contract, answer, err)
	clearNonChannelWebsearchAttribution(c, info, answer, err)
	if err != nil {
		if errors.Is(err, websearch.ErrUnsupportedChannel) {
			// Allow retry onto another channel that may support this endpoint.
			return types.NewError(
				fmt.Errorf("channel does not support %s", c.Request.URL.Path),
				types.ErrorCodeInvalidRequest,
			)
		}
		if (fetch && errors.Is(err, websearch.ErrEmptyContent)) || (!fetch && errors.Is(err, websearch.ErrEmptyResults)) {
			return writeEmptyWebsearchAnswer(c, info, contract)
		}
		return websearchExecutionError(err)
	}

	return writeWebsearchAnswer(c, info, answer, func(body []byte) bool {
		// DeepSeek bills a successful request only when it returns something: a
		// non-empty results array for Search, page content for Fetch. A billable
		// call settles at the routing name's request price; any other response
		// releases the reservation.
		if fetch {
			return gjson.GetBytes(body, "content").String() != ""
		}
		return websearchHasResults(body, "results")
	})
}

func websearchHasResults(body []byte, field string) bool {
	results := gjson.GetBytes(body, field)
	return results.IsArray() && len(results.Array()) > 0
}

func websearchOK(status int) bool {
	return status >= 200 && status < 300
}

func websearchExecutionError(err error) *types.NewAPIError {
	apiErr := types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	if request.WasSent(err) {
		types.ErrOptionWithSkipRetry()(apiErr)
	}
	return apiErr
}

func websearchStatusError(c *gin.Context, answer websearch.Answer) *types.NewAPIError {
	resp := &http.Response{
		StatusCode: answer.StatusCode,
		Header:     answer.Header,
		Body:       io.NopCloser(bytes.NewReader(answer.Body)),
		Request:    c.Request,
	}
	apiErr := service.RelayErrorHandler(c.Request.Context(), resp, false)
	service.ResetStatusCode(apiErr, c.GetString("status_code_mapping"))
	types.ErrOptionWithSkipRetry()(apiErr)
	return apiErr
}

func writeEmptyWebsearchAnswer(c *gin.Context, info *relaycommon.RelayInfo, contract abstract.Contract) *types.NewAPIError {
	renderer := response.For(contract)
	if renderer == nil {
		return websearchExecutionError(websearch.ErrUnsupportedChannel)
	}
	var body []byte
	var err error
	if contract.IsSearch() {
		body, err = renderer.RenderSearch(abstract.Search{})
	} else {
		body, err = renderer.RenderFetch(abstract.Fetch{})
	}
	if err != nil {
		return websearchExecutionError(err)
	}
	return writeWebsearchAnswer(c, info, websearch.Answer{
		StatusCode:  http.StatusOK,
		ContentType: renderer.ContentType(),
		Body:        body,
	}, func([]byte) bool { return false })
}

func writeWebsearchAnswer(c *gin.Context, info *relaycommon.RelayInfo, answer websearch.Answer, billable func([]byte) bool) *types.NewAPIError {
	if !websearchOK(answer.StatusCode) {
		return websearchStatusError(c, answer)
	}

	if answer.ContentType != "" {
		c.Writer.Header().Set("Content-Type", answer.ContentType)
	}
	service.IOCopyBytesGracefully(c, &http.Response{
		StatusCode: answer.StatusCode,
		Header:     answer.Header,
	}, answer.Body)

	if !billable(answer.Body) {
		info.ToolExecution.Outcome = "empty"
		requestWebsearchRefund(c, info)
		return nil
	}
	service.PostTextConsumeQuota(c, info, &dto.Usage{}, nil)
	return nil
}

func prepareWebsearchAttempt(c *gin.Context, info *relaycommon.RelayInfo, query *request.Query) *types.NewAPIError {
	info.ToolExecution = &relaycommon.ToolExecutionMeta{Contract: string(query.Contract), Outcome: "unavailable"}
	adaptor, ok := GetAdaptor(info.ApiType).(relaychannel.WebSearchAdaptor)
	if !ok {
		return nil
	}
	upstream, err := adaptor.PrepareWebSearch(c, info, query.Contract)
	if errors.Is(err, abstract.ErrUnsupportedChannel) {
		return nil // The channel's own operation is unavailable; other candidates may still answer.
	}
	if err != nil {
		return websearchExecutionError(err)
	}
	query.EndpointProvider, query.Endpoint, query.Model, query.Prepare = upstream.Provider, upstream.Endpoint, upstream.Model, upstream.Prepare
	return nil
}

func recordWebsearchResult(info *relaycommon.RelayInfo, contract abstract.Contract, answer websearch.Answer, err error) {
	outcome := "upstream_error"
	switch {
	case errors.Is(err, websearch.ErrUnsupportedChannel):
		outcome = "unavailable"
	case errors.Is(err, websearch.ErrEmptyResults), errors.Is(err, websearch.ErrEmptyContent):
		outcome = "empty"
	case err == nil && websearchOK(answer.StatusCode):
		outcome = "success"
	}
	info.ToolExecution = &relaycommon.ToolExecutionMeta{Contract: string(contract), Provider: answer.Provider, Outcome: outcome, CacheHit: answer.Cached}
}

// clearNonChannelWebsearchAttribution clears the carrier channel attribution
// when a tool call was fulfilled by the in-process local fetcher or a global
// credential rather than the selected channel's own upstream, so channel quota
// and usage logs are not erroneously attributed to the carrier channel.
func clearNonChannelWebsearchAttribution(c *gin.Context, info *relaycommon.RelayInfo, answer websearch.Answer, err error) {
	if err != nil || !websearchOK(answer.StatusCode) {
		return
	}
	if answer.Provider != websearch_setting.ProviderLocal && answer.Origin != abstract.OriginGlobalCredential {
		return
	}
	info.ChannelId = 0
	if c != nil {
		common.SetContextKey(c, constant.ContextKeyChannelId, 0)
		common.SetContextKey(c, constant.ContextKeyChannelName, "")
	}
}

func requestWebsearchRefund(c *gin.Context, info *relaycommon.RelayInfo) {
	service.RefundRequestBilling(c, info)
}

// websearchChannelContext assembles what the pipeline needs from the attempt's
// context. The channel object the relay layer holds is a stub without settings,
// so the settings come from the entry the distributor stored.
func websearchChannelContext(c *gin.Context) abstract.ChannelContext {
	service.ApplyWebSearchRoutingPolicy(c)
	channel := abstract.ChannelContext{
		ModelMapping:       c.GetString("model_mapping"),
		ChannelID:          common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		ChannelType:        common.GetContextKeyInt(c, constant.ContextKeyChannelType),
		BaseURL:            common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl),
		Key:                common.GetContextKeyString(c, constant.ContextKeyChannelKey),
		PreferredProviders: service.WebSearchPreferredProviders(c),
	}
	if settings, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting); ok {
		channel.Settings = settings
	}
	if channel.ChannelType == constant.ChannelTypeAdvancedCustom {
		if saved, err := model.LoadChannelCustomTools(c.Request.Context(), channel.ChannelID); err == nil {
			channel.BaseURL, channel.CustomTools = saved.GetBaseURL(), saved.CustomTools
		}
	}
	return channel
}
