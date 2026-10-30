package antigravity

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const streamPath = "/v1internal:streamGenerateContent"

type Adaptor struct {
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
}

// GetRequestURL 始终走上游的流式接口：非流式下游由 DoResponse 聚合流式帧得到，
// 因为 Antigravity 对 Claude 这类模型不接受一次性返回。
func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return strings.TrimRight(a.baseURL(info), "/") + streamPath + "?alt=sse", nil
}

func (a *Adaptor) baseURL(info *relaycommon.RelayInfo) string {
	baseURL := strings.TrimSpace(info.ChannelBaseUrl)
	if baseURL == "" {
		baseURL = constant.GetChannelBaseURL(constant.ChannelTypeAntigravity)
	}
	return baseURL
}

func (a *Adaptor) credential(c *gin.Context, info *relaycommon.RelayInfo) (*service.AntigravityCredential, error) {
	return service.EnsureAntigravityCredential(
		c.Request.Context(),
		info.ChannelId,
		info.ApiKey,
		a.baseURL(info),
		info.ChannelSetting,
	)
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, req *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, req)
	credential, err := a.credential(c, info)
	if err != nil {
		return err
	}
	req.Set("Authorization", "Bearer "+credential.AccessToken)
	req.Set("Content-Type", "application/json")
	req.Set("User-Agent", credential.UserAgent)
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	result, err := service.ConvertRequest(c, info, types.RelayFormatGemini, request)
	if err != nil {
		return nil, err
	}
	return a.wrapGeminiRequest(c, info, result.Value)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	result, err := service.ConvertRequest(c, info, types.RelayFormatGemini, request)
	if err != nil {
		return nil, err
	}
	return a.wrapGeminiRequest(c, info, result.Value)
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	result, err := service.ConvertRequest(c, info, types.RelayFormatGemini, &request)
	if err != nil {
		return nil, err
	}
	return a.wrapGeminiRequest(c, info, result.Value)
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	if err := relayconvert.ApplyGeminiThinkingConfigChecked(request, info); err != nil {
		return nil, err
	}
	if len(request.Contents) > 0 && request.Contents[0].Role == "" {
		request.Contents[0].Role = "user"
	}
	return a.wrapGeminiRequest(c, info, request)
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return nil, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return nil, errors.New("antigravity channel does not support the embeddings endpoint")
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	return nil, errors.New("antigravity channel does not support the audio endpoint")
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	return nil, errors.New("antigravity channel does not support the images endpoint; use a chat model that returns inline images")
}

func (a *Adaptor) wrapGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, value any) (any, error) {
	geminiRequest, err := geminiRequestFromValue(value)
	if err != nil {
		return nil, err
	}
	credential, err := a.credential(c, info)
	if err != nil {
		return nil, err
	}
	body, err := buildRequestBody(info.UpstreamModelName, credential.ProjectID, geminiRequest)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	return channel.DoApiRequest(a, c, info, requestBody)
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	if info.IsStream {
		resp.Body = newUnwrapReader(resp.Body)
	} else {
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
		}
		unwrapped, err := unwrapResponseBody(raw)
		if err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		resp.Body = io.NopCloser(bytes.NewReader(unwrapped))
	}

	if info.RelayMode == relayconstant.RelayModeResponses {
		if info.IsStream {
			return gemini.GeminiResponsesStreamHandler(c, info, resp)
		}
		return gemini.GeminiResponsesHandler(c, info, resp)
	}

	if info.RelayMode == relayconstant.RelayModeGemini {
		if info.IsStream {
			return gemini.GeminiTextGenerationStreamHandler(c, info, resp)
		}
		return gemini.GeminiTextGenerationHandler(c, info, resp)
	}

	if info.IsStream {
		return gemini.GeminiChatStreamHandler(c, info, resp)
	}
	return gemini.GeminiChatHandler(c, info, resp)
}

func (a *Adaptor) GetModelList() []string {
	return ModelList
}

func (a *Adaptor) GetChannelName() string {
	return ChannelName
}
