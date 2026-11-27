package relay

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/websearch"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// AlphaSearchHelper relays the Codex standalone web search through the websearch
// pipeline. A Codex channel's own upstream is the native candidate, so its
// request and answer stay byte for byte what they were; a channel that can only
// reach Codex through a global credential would be rendered, but no provider
// publishes that credential today, so in practice the Codex channel is the only
// candidate.
func AlphaSearchHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	request_, ok := info.Request.(*dto.AlphaSearchRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected *dto.AlphaSearchRequest, got %T", info.Request),
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

	target, prepare, buildErr := channelUpstream(c, info)
	if buildErr != nil {
		return types.NewOpenAIError(buildErr, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	query := request.Query{
		Contract: abstract.ContractAlphaSearch,
		Text:     gjson.GetBytes(jsonData, "commands.search_query.0.q").String(),
		Raw:      jsonData,
		Endpoint: target,
		Prepare:  prepare,
	}

	channel := websearchChannelContext(c)
	websearch.LogCandidates(c.Request.Context(), channel, query.Contract)

	answer, err := websearch.Search(c.Request.Context(), channel, query)
	if err != nil {
		if errors.Is(err, websearch.ErrUnsupportedChannel) {
			// Allow retry onto another channel that may support this endpoint.
			return types.NewError(
				errors.New("channel does not support /v1/alpha/search"),
				types.ErrorCodeInvalidRequest,
			)
		}
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	if answer.StatusCode < 200 || answer.StatusCode >= 300 {
		newAPIError = writeWebsearchAnswer(c, info, answer, func([]byte) bool { return true })
		return newAPIError
	}

	// Upstream alpha search returns no usage; bill one web_search_preview call.
	info.SetBillableToolCount(dto.BuildInToolWebSearchPreview, 1)
	return writeWebsearchAnswer(c, info, answer, func([]byte) bool { return true })
}

// buildAlphaSearchRequestBody returns RawBody unchanged unless the model was
// mapped, in which case only the "model" field is rewritten so unknown fields
// are preserved.
func buildAlphaSearchRequestBody(rawBody []byte, originModel, upstreamModel string) ([]byte, error) {
	if len(rawBody) == 0 {
		return nil, errors.New("empty alpha search request body")
	}
	if upstreamModel == "" || upstreamModel == originModel {
		return rawBody, nil
	}
	var body map[string]any
	if err := common.Unmarshal(rawBody, &body); err != nil {
		return nil, err
	}
	body["model"] = upstreamModel
	return common.Marshal(body)
}
