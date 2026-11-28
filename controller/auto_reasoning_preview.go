package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/gin-gonic/gin"
)

type autoReasoningDraft struct {
	Policy    json.RawMessage `json:"policy"`
	Protocol  string          `json:"protocol"`
	Model     string          `json:"model"`
	Group     string          `json:"group"`
	ChannelID int             `json:"channel_id"`
	Scale     *int            `json:"scale"`
	Text      *string         `json:"text"`
	Explicit  string          `json:"explicit_effort"`
}

func PreviewAutoReasoning(ctx *gin.Context) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 96<<10)
	var draft autoReasoningDraft
	if err := ctx.ShouldBindJSON(&draft); err != nil || !draft.valid() {
		autoReasoningPreviewError(ctx, "invalid reasoning preview input")
		return
	}
	rules, err := hostreasoning.ParseAutoEffortOption(string(draft.Policy))
	if err != nil {
		autoReasoningPreviewError(ctx, "invalid automatic reasoning policy")
		return
	}
	request, format, mode, path, err := draft.request()
	if err != nil {
		autoReasoningPreviewError(ctx, err.Error())
		return
	}
	channel, err := model.GetChannelById(draft.ChannelID, false)
	if err != nil {
		autoReasoningPreviewError(ctx, "preview channel is unavailable")
		return
	}
	info, fields, err := previewReasoningConversion(ctx, draft, rules, channel, request, format, mode, path)
	if err != nil {
		autoReasoningPreviewError(ctx, "channel cannot convert this preview request")
		return
	}
	meta := info.AutoReasoning
	effort, tier := rules.Tier(meta.Scale)
	common.ApiSuccess(ctx, gin.H{"scale": meta.Scale, "tier": tier, "tier_effort": effort, "effort": meta.Effort, "reason": meta.Reason, "fields": fields})
}

func autoReasoningPreviewError(ctx *gin.Context, message string) {
	ctx.JSON(http.StatusBadRequest, gin.H{"success": false, "message": message})
}

func (draft autoReasoningDraft) valid() bool {
	if draft.ChannelID <= 0 || strings.TrimSpace(draft.Model) == "" || len(draft.Model) > 256 || strings.TrimSpace(draft.Group) == "" || len(draft.Group) > 64 {
		return false
	}
	if (draft.Scale == nil) == (draft.Text == nil) {
		return false
	}
	if draft.Scale != nil && (*draft.Scale < 0 || *draft.Scale > 10_000_000) || draft.Text != nil && len(*draft.Text) > 8192 {
		return false
	}
	switch draft.Explicit {
	case "", "none", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func (draft autoReasoningDraft) request() (dto.Request, types.RelayFormat, int, string, error) {
	text := "Preview"
	if draft.Text != nil {
		text = *draft.Text
	}
	var wire map[string]any
	var request dto.Request
	var format types.RelayFormat
	var mode int
	var path string
	switch draft.Protocol {
	case "chat":
		wire = map[string]any{"model": draft.Model, "messages": []any{gin.H{"role": "user", "content": text}}}
		if draft.Explicit != "" {
			wire["reasoning_effort"] = draft.Explicit
		}
		request, format, mode, path = &dto.GeneralOpenAIRequest{}, types.RelayFormatOpenAI, relayconstant.RelayModeChatCompletions, "/v1/chat/completions"
	case "responses":
		wire = map[string]any{"model": draft.Model, "input": text}
		if draft.Explicit != "" {
			wire["reasoning"] = gin.H{"effort": draft.Explicit}
		}
		request, format, mode, path = &dto.OpenAIResponsesRequest{}, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, "/v1/responses"
	case "claude":
		wire = map[string]any{"model": draft.Model, "max_tokens": 8192, "messages": []any{gin.H{"role": "user", "content": text}}}
		if draft.Explicit == "none" {
			wire["thinking"] = gin.H{"type": "disabled"}
		} else if draft.Explicit != "" {
			wire["output_config"] = gin.H{"effort": draft.Explicit}
		}
		request, format, mode, path = &dto.ClaudeRequest{}, types.RelayFormatClaude, relayconstant.RelayModeChatCompletions, "/v1/messages"
	case "gemini":
		wire = map[string]any{"contents": []any{gin.H{"role": "user", "parts": []any{gin.H{"text": text}}}}}
		if draft.Explicit == "none" {
			wire["generationConfig"] = gin.H{"thinkingConfig": gin.H{"thinkingBudget": 0}}
		} else if draft.Explicit != "" {
			wire["generationConfig"] = gin.H{"thinkingConfig": gin.H{"thinkingLevel": strings.ToUpper(draft.Explicit)}}
		}
		request, format, mode, path = &dto.GeminiChatRequest{}, types.RelayFormatGemini, relayconstant.RelayModeGemini, "/v1beta/models/"+draft.Model+":generateContent"
	default:
		return nil, "", 0, "", errors.New("invalid preview protocol")
	}
	raw, err := json.Marshal(wire)
	if err != nil || json.Unmarshal(raw, request) != nil {
		return nil, "", 0, "", errors.New("invalid protocol parameters for reasoning preview")
	}
	return request, format, mode, path, nil
}

func (draft autoReasoningDraft) scale(request dto.Request, rules *hostreasoning.AutoEffortRules) int {
	if draft.Scale != nil {
		return *draft.Scale
	}
	scale, _ := helper.EstimateAutoReasoningScale(rules.Metric(), &relaycommon.RelayInfo{Request: request, OriginModelName: draft.Model})
	return scale
}

func previewReasoningConversion(ctx *gin.Context, draft autoReasoningDraft, rules *hostreasoning.AutoEffortRules, channel *model.Channel, request dto.Request, format types.RelayFormat, mode int, path string) (*relaycommon.RelayInfo, map[string]any, error) {
	// Never put channel credentials or inbound request headers on the conversion context.
	preview := &gin.Context{}
	preview.Request, _ = http.NewRequestWithContext(ctx.Request.Context(), http.MethodPost, "http://localhost/", nil)
	preview.Request.URL.Path = path
	common.SetContextKey(preview, constant.ContextKeyChannelId, channel.Id)
	common.SetContextKey(preview, constant.ContextKeyChannelType, channel.Type)
	var settings dto.ChannelSettings
	if channel.Setting != nil && *channel.Setting != "" {
		if err := json.Unmarshal([]byte(*channel.Setting), &settings); err != nil {
			return nil, nil, err
		}
	}
	var otherSettings dto.ChannelOtherSettings
	if channel.OtherSettings != "" {
		if err := json.Unmarshal([]byte(channel.OtherSettings), &otherSettings); err != nil {
			return nil, nil, err
		}
	}
	if preset := common.GetAdvancedCustomPreset(channel.Type); preset != nil {
		otherSettings.AdvancedCustom = preset
	}
	common.SetContextKey(preview, constant.ContextKeyChannelSetting, settings)
	common.SetContextKey(preview, constant.ContextKeyChannelOtherSetting, otherSettings)
	common.SetContextKey(preview, constant.ContextKeyChannelParamOverride, channel.GetParamOverride())
	common.SetContextKey(preview, constant.ContextKeyOriginalModel, draft.Model)
	preview.Set("model_mapping", channel.GetModelMapping())
	info := &relaycommon.RelayInfo{Request: request, OriginModelName: draft.Model, UsingGroup: draft.Group, RelayFormat: format, RelayMode: mode, RequestURLPath: path}
	info.InitChannelMeta(preview)
	meta := &relaycommon.AutoReasoningMeta{Rules: rules, Scale: draft.scale(request, rules), Reason: "disabled"}
	info.AutoReasoning = meta
	if helper.RequestPassesThrough(preview, info, info.ChannelSetting) {
		meta.Reason = "passthrough"
		raw, err := json.Marshal(request)
		return info, previewReasoningFields(raw), err
	}
	if !rules.Enabled() {
		// Leave the draft disabled even if the live policy is enabled.
		meta.Reason = "disabled"
		return info, nil, nil
	}
	outbound, _, _, _, err := draft.request()
	if err != nil {
		return nil, nil, err
	}
	if err := helper.ModelMappedHelper(preview, info, outbound); err != nil {
		return nil, nil, err
	}
	if err := helper.ApplyReasoningModelSuffix(preview, info, outbound); err != nil {
		return nil, nil, err
	}
	adaptor := relay.GetAdaptor(info.ApiType)
	if adaptor == nil {
		return nil, nil, errors.New("unsupported channel")
	}
	adaptor.Init(info)
	raw, err := previewReasoningOutbound(preview, info, adaptor, outbound)
	return info, previewReasoningFields(raw), err
}

func previewReasoningOutbound(ctx *gin.Context, info *relaycommon.RelayInfo, adaptor relaychannel.Adaptor, request dto.Request) ([]byte, error) {
	var converted any
	var err error
	switch req := request.(type) {
	case *dto.GeneralOpenAIRequest:
		if info.ChannelType == constant.ChannelTypeCodex || service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
			raw, err := previewApplyReasoningOverrides(info, req, true, true)
			if err != nil {
				return nil, err
			}
			var overridden dto.GeneralOpenAIRequest
			if err := json.Unmarshal(raw, &overridden); err != nil {
				return nil, err
			}
			if !reflect.DeepEqual(req.Messages, overridden.Messages) {
				return nil, errors.New("preview does not support overrides that replace sample messages")
			}
			result, err := service.ConvertRequest(ctx, info, types.RelayFormatOpenAIResponses, &overridden)
			if err != nil {
				return nil, err
			}
			responses, ok := result.Value.(*dto.OpenAIResponsesRequest)
			if !ok {
				return nil, errors.New("unexpected Responses conversion result")
			}
			info.RelayMode, info.RequestURLPath = relayconstant.RelayModeResponses, "/v1/responses"
			converted, err = adaptor.ConvertOpenAIResponsesRequest(ctx, info, *responses)
			if err != nil {
				return nil, err
			}
			return previewApplyReasoningOverrides(info, converted, true, false)
		}
		converted, err = adaptor.ConvertOpenAIRequest(ctx, info, req)
	case *dto.OpenAIResponsesRequest:
		converted, err = adaptor.ConvertOpenAIResponsesRequest(ctx, info, *req)
	case *dto.ClaudeRequest:
		converted, err = adaptor.ConvertClaudeRequest(ctx, info, req)
	case *dto.GeminiChatRequest:
		converted, err = adaptor.ConvertGeminiRequest(ctx, info, req)
	}
	if err != nil {
		return nil, err
	}
	return previewApplyReasoningOverrides(info, converted, info.RelayFormat != types.RelayFormatGemini, true)
}

func previewApplyReasoningOverrides(info *relaycommon.RelayInfo, value any, removeDisabled, override bool) ([]byte, error) {
	raw, ok := value.([]byte)
	if !ok {
		var err error
		raw, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	if removeDisabled {
		var err error
		raw, err = relaycommon.RemoveDisabledFields(raw, info.ChannelOtherSettings, false)
		if err != nil {
			return nil, err
		}
	}
	if override && len(info.ParamOverride) > 0 {
		return relaycommon.ApplyParamOverrideWithRelayInfo(raw, info)
	}
	return raw, nil
}

func previewReasoningFields(raw []byte) map[string]any {
	return relaycommon.OutboundReasoningFields(raw)
}
