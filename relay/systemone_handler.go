package relay

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// SystemOneHelper relays a TypeSafe decision request. The upstream answers with
// probabilities, labels or scores instead of chat text, and reports its own
// input/output token counts, so billing settles on those numbers.
func SystemOneHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	switch info.ChannelType {
	case constant.ChannelTypeOpenAI,
		constant.ChannelTypeNewAPI,
		constant.ChannelTypeAdvancedCustom:
	default:
		// Allow retry onto another channel that may support this endpoint.
		return types.NewError(
			errors.New("channel does not support /v1/systemone"),
			types.ErrorCodeInvalidRequest,
		)
	}

	request, ok := info.Request.(*dto.SystemOneRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected *dto.SystemOneRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	if err := helper.ModelMappedHelper(c, info, request); err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	jsonData, err := buildSystemOneRequestBody(request.RawBody, info.OriginModelName, info.UpstreamModelName)
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
	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	resp, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		return types.NewOpenAIError(errors.New("invalid http response"), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return types.NewError(err, types.ErrorCodeReadResponseBodyFailed, types.ErrOptionWithSkipRetry())
	}

	usage := systemOneUsage(responseBody)
	responseBody = rewriteSystemOneResponse(responseBody, info.OriginModelName)

	contentType := httpResp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.WriteHeader(httpResp.StatusCode)
	if _, err := c.Writer.Write(responseBody); err != nil {
		return types.NewError(err, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}

	service.PostTextConsumeQuota(c, info, usage, nil)
	return nil
}

// rewriteSystemOneResponse reports the model name the client asked for and
// drops the upstream account block, which names the provider's own billing
// source rather than anything the caller can use. Bodies that are not a JSON
// object pass through unchanged.
func rewriteSystemOneResponse(responseBody []byte, modelName string) []byte {
	var response map[string]any
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return responseBody
	}
	if modelName != "" {
		response["model"] = modelName
	}
	delete(response, "opencode")
	if usage, ok := response["usage"].(map[string]any); ok {
		delete(usage, "opencode")
	}
	rewritten, err := common.Marshal(response)
	if err != nil {
		return responseBody
	}
	return rewritten
}

// buildSystemOneRequestBody returns RawBody unchanged unless the model was
// mapped, in which case only the "model" field is rewritten so unknown fields
// are preserved.
func buildSystemOneRequestBody(rawBody []byte, originModel, upstreamModel string) ([]byte, error) {
	if len(rawBody) == 0 {
		return nil, errors.New("empty systemone request body")
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

// systemOneUsage reads the token counts the upstream reports for a decision.
// It returns nil when the response carries no usage, which makes the settlement
// fall back to the prompt estimate.
func systemOneUsage(responseBody []byte) *dto.Usage {
	var response struct {
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return nil
	}
	if response.Usage.InputTokens == 0 && response.Usage.OutputTokens == 0 {
		return nil
	}
	return &dto.Usage{
		PromptTokens:     response.Usage.InputTokens,
		CompletionTokens: response.Usage.OutputTokens,
		TotalTokens:      response.Usage.InputTokens + response.Usage.OutputTokens,
	}
}
