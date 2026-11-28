package helper

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/gin-gonic/gin"
)

func RequestPassesThrough(ctx *gin.Context, info *relaycommon.RelayInfo, settings dto.ChannelSettings) bool {
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || settings.PassThroughBodyEnabled {
		return true
	}
	if ctx == nil || ctx.Request == nil || common.GetContextKeyInt(ctx, constant.ContextKeyChannelType) != constant.ChannelTypeAdvancedCustom {
		return false
	}
	other, _ := common.GetContextKeyType[dto.ChannelOtherSettings](ctx, constant.ContextKeyChannelOtherSetting)
	route, matched := other.AdvancedCustom.MatchPathForModel(ctx.Request.URL.Path, info.OriginModelName)
	return matched && route.PassThroughBodyEnabled
}

func PrepareAutoReasoning(info *relaycommon.RelayInfo) {
	if info == nil || info.AutoReasoning != nil || info.Request == nil || info.RelayMode == relayconstant.RelayModeResponsesCompact {
		return
	}
	rules := hostreasoning.CurrentAutoEffort()
	if !rules.Enabled() {
		return
	}
	switch info.RelayFormat {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini:
	default:
		return
	}
	scale, ok := EstimateAutoReasoningScale(rules.Metric(), info)
	if ok {
		info.AutoReasoning = &relaycommon.AutoReasoningMeta{Rules: rules, Scale: scale}
	}
}

func EstimateAutoReasoningScale(metric string, info *relaycommon.RelayInfo) (int, bool) {
	meta := info.Request.GetTokenCountMeta()
	if meta == nil {
		return 0, false
	}
	images := 0
	if metric != "input_chars" {
		images = relaycommon.RequestImageCount(info.RelayFormat, info.Request)
	}
	return estimateAutoReasoningScale(metric, info.OriginModelName, meta, images), true
}

func estimateAutoReasoningScale(metric, model string, meta *types.TokenCountMeta, images int) int {
	if metric == "input_chars" {
		return utf8.RuneCountInString(meta.CombineText)
	}
	return tokenkit.Count(model, meta.CombineText) + 3 + meta.ToolsCount*8 + (meta.MessagesCount+meta.NameCount)*3 + images*850
}

func autoReasoningIntent(info *relaycommon.RelayInfo, model string) (reasoning.Intent, []types.ConversionDiagnostic, error) {
	meta := info.AutoReasoning
	if meta == nil {
		return reasoning.Intent{}, nil, nil
	}
	meta.Reason = "unsupported_model"
	meta.Effort = ""
	if info.ChannelMeta == nil || !autoReasoningSupported(info.ChannelType, model) {
		return reasoning.Intent{}, nil, nil
	}
	if model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) || model_setting.ShouldPreserveThinkingSuffix(model) {
		meta.Reason = "blacklisted"
		return reasoning.Intent{}, nil, nil
	}
	if _, _, _, found := hostreasoning.ParseDeepSeekV4ThinkingSuffix(model); found && info.ChannelType == constant.ChannelTypeDeepSeek {
		meta.Reason = "model_modifier"
		return reasoning.Intent{}, nil, nil
	}
	if reason := meta.Rules.Scope(info.UsingGroup, info.OriginModelName, model); reason != "" {
		meta.Reason = reason
		return reasoning.Intent{}, nil, nil
	}
	explicit, diagnostics, err := explicitIntentFromRequest(info.Request)
	if err != nil {
		return reasoning.Intent{}, nil, err
	}
	decision := meta.Rules.Resolve(explicit, meta.Scale)
	meta.Reason = decision.Reason
	meta.Conflict = decision.Reason
	meta.PolicyEffort = string(decision.TargetEffort)
	diagnostic := autoReasoningDiagnostic(info.RelayFormat, decision)
	if !decision.Applied {
		info.RecordConversionDiagnostics(context.Background(), []types.ConversionDiagnostic{diagnostic})
		return reasoning.Intent{}, nil, nil
	}
	// Keep the existing observation outcome stable; conversion diagnostics distinguish cap and override.
	meta.Reason = "applied"
	meta.Effort = string(decision.EffectiveEffort)
	return decision.Intent, append(diagnostics, diagnostic), nil
}

func autoReasoningDiagnostic(format types.RelayFormat, decision hostreasoning.AutoEffortDecision) types.ConversionDiagnostic {
	diagnostic := modelModifierDiagnostic("auto_reasoning_"+decision.Reason, "thinking", fmt.Sprintf(
		"scale=%d tier=%d target_effort=%s resolved_effort=%s",
		decision.Scale, decision.Tier, decision.TargetEffort, decision.EffectiveEffort,
	))
	diagnostic.From = format
	return diagnostic
}

func autoReasoningSupported(channelType int, model string) bool {
	name := strings.ToLower(model)
	if index := strings.LastIndexByte(name, '/'); index >= 0 {
		name = name[index+1:]
	}
	switch channelType {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeAzure, constant.ChannelTypeCodex:
		return hasReasoningFamily(name, "gpt-5", "gpt-6", "o1", "o3", "o4")
	case constant.ChannelTypeAnthropic, constant.ChannelTypeAws:
		return hasReasoningFamily(name, "claude-sonnet-4", "claude-opus-4", "claude-haiku-4", "claude-3-7-sonnet")
	case constant.ChannelTypeGemini, constant.ChannelTypeVertexAi, constant.ChannelTypeAntigravity:
		return hasReasoningFamily(name, "gemini-2.5", "gemini-3", "gemini-3.1", "gemini-3.5")
	case constant.ChannelTypeDeepSeek:
		return hasReasoningFamily(name, "deepseek-v4", "deepseek-v4.1")
	default:
		return false
	}
}

func hasReasoningFamily(name string, families ...string) bool {
	for _, family := range families {
		if name == family || strings.HasPrefix(name, family+"-") || strings.HasPrefix(name, family+".") {
			return true
		}
	}
	return false
}
