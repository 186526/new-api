package relay

import (
	"errors"
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
	"github.com/QuantumNous/new-api/relaykit/systemone"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// SystemOneHelper relays a TypeSafe decision request. The upstream answers with
// probabilities, labels or scores instead of chat text, and reports its own
// input/output token counts, so billing settles on those numbers.
//
// The request is checked against the schema the upstream publishes before it is
// forwarded, and an upstream refusal reaches the client with the status and the
// document the upstream wrote. A request the schema rejects answers 422 with the
// field locations, which is what a generated client reads to fix the call.
func SystemOneHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)
	// The contract prefix is this gateway's own namespace; the upstream answers
	// only its own path, so the request is forwarded under that one.
	info.RequestURLPath = relaycommon.SystemOneUpstreamRequestPath(info.RequestURLPath)

	switch info.ChannelType {
	case constant.ChannelTypeOpenAI,
		constant.ChannelTypeNewAPI,
		constant.ChannelTypeAdvancedCustom:
	default:
		// Allow retry onto another channel that may support this endpoint.
		return types.NewError(
			errors.New("this channel does not serve /v1/systemone"),
			types.ErrorCodeInvalidRequest,
			types.ErrOptionWithStatusCode(http.StatusBadRequest),
			types.ErrOptionWithSkipRetry(),
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

	rawBody, bodyError := systemOneForwardableBody(c, request.RawBody)
	if bodyError != nil {
		return bodyError
	}

	longContext := HeadersEnabled(c.GetHeader(SystemOneLongContextHeader))
	imagesEnabled := HeadersEnabled(c.GetHeader(SystemOneImagesHeader))
	if problems := ValidateSystemOneBody(rawBody, longContext, imagesEnabled); len(problems) > 0 {
		return systemOneValidationRefusal(problems)
	}

	jsonData, err := buildSystemOneRequestBody(rawBody, info.OriginModelName, info.UpstreamModelName)
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
		return systemOneFailure(c, info, httpResp, statusCodeMappingStr)
	}

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return types.NewError(err, types.ErrorCodeReadResponseBodyFailed, types.ErrOptionWithSkipRetry())
	}

	usage := systemOneUsage(responseBody)
	responseBody = rewriteSystemOneResponse(responseBody, info.OriginModelName)

	if contentType := httpResp.Header.Get("Content-Type"); contentType != "" {
		c.Writer.Header().Set("Content-Type", contentType)
	} else {
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	// The upstream may answer compressed. The body was read and rewritten here,
	// so the encoding it arrived in no longer describes what is written.
	c.Writer.Header().Del("Content-Encoding")
	c.Writer.Header().Del("Transfer-Encoding")
	c.Writer.WriteHeader(httpResp.StatusCode)
	if _, err := c.Writer.Write(responseBody); err != nil {
		return types.NewError(err, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}

	service.PostTextConsumeQuota(c, info, usage, nil)
	return nil
}

// systemOneFailure answers a non-2xx upstream. A status the caller can act on
// keeps its own value and document; anything else leaves the retry loop to
// decide, so a transient upstream fault still retries.
func systemOneFailure(c *gin.Context, info *relaycommon.RelayInfo, httpResp *http.Response, statusCodeMappingStr string) *types.NewAPIError {
	body, readErr := readSystemOneUpstreamBody(httpResp)
	if readErr != nil {
		return service.RelayErrorHandler(c.Request.Context(), httpResp, false)
	}

	if apiError := systemOneUpstreamError(httpResp.StatusCode, body, httpResp.Header.Get("Retry-After")); apiError != nil {
		if retryAfter := httpResp.Header.Get("Retry-After"); retryAfter != "" {
			c.Writer.Header().Set("Retry-After", retryAfter)
		}
		service.ResetStatusCode(apiError, statusCodeMappingStr)
		// The channel answered; whether it is at fault is the disable check's
		// business. An upstream that rejects this body is not, so the retry loop
		// must not count it against the channel.
		return apiError
	}

	apiError := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
	service.ResetStatusCode(apiError, statusCodeMappingStr)
	return apiError
}

// systemOneValidationRefusal is the error a rejected body answers with. It is
// never sent to another channel, because every channel answers the same way, and
// the document it carries is the one the client reads: the array of rejected
// fields, written verbatim by the error writer.
func systemOneValidationRefusal(problems []systemone.ValidationError) *types.NewAPIError {
	document, err := common.Marshal(systemone.ValidationDocument(problems))
	if err != nil {
		return types.NewErrorWithStatusCode(
			errors.New(systemOneValidationSummary(problems)),
			types.ErrorCodeInvalidRequest,
			http.StatusUnprocessableEntity,
			types.ErrOptionWithSkipRetry(),
		)
	}
	apiError := types.NewErrorWithStatusCode(
		errors.New(systemOneValidationSummary(problems)),
		types.ErrorCodeInvalidRequest,
		http.StatusUnprocessableEntity,
		types.ErrOptionWithSkipRetry(),
	)
	apiError.Metadata = document
	return apiError
}

// systemOneForwardableBody answers the JSON to validate and forward. A multipart
// request is folded into the JSON shape first, because that is the only shape the
// rest of the path reads.
func systemOneForwardableBody(c *gin.Context, rawBody []byte) ([]byte, *types.NewAPIError) {
	if !common.IsMultipartFormContentType(c.GetHeader("Content-Type")) {
		return rawBody, nil
	}
	joined, problems := systemone.MultipartBody(c.GetHeader("Content-Type"), rawBody)
	if len(problems) > 0 {
		return nil, systemOneValidationRefusal(problems)
	}
	return joined, nil
}

// systemOneValidationSummary is the one-line message the error log records for a
// rejected body. The client reads the array, so this is only for the log.
func systemOneValidationSummary(problems []systemone.ValidationError) string {
	if len(problems) == 0 {
		return "request rejected"
	}
	var fields []string
	for _, problem := range problems {
		fields = append(fields, systemOneFieldPath(problem.Loc))
	}
	return "request rejected: " + strings.Join(fields, ", ")
}

func systemOneFieldPath(loc []any) string {
	parts := make([]string, 0, len(loc))
	for _, item := range loc {
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, ".")
}

// rewriteSystemOneResponse reports the model name the client asked for, drops
// the upstream account block, and leaves only the two usage counters the
// contract declares. Bodies that are not a JSON object pass through unchanged.
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
		// The Usage schema declares input_tokens and output_tokens and nothing
		// else; a strict client rejects the document when another key is there.
		for key := range usage {
			if key != "input_tokens" && key != "output_tokens" {
				delete(usage, key)
			}
		}
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
