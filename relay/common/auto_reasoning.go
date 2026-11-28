package common

import (
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/tidwall/gjson"
)

type AutoReasoningMeta struct {
	Rules          *reasoning.AutoEffortRules
	Scale          int
	Effort         string
	Reason         string
	Conflict       string
	PolicyEffort   string
	OutboundFields map[string]any
	OutboundStatus string
}

func (meta *AutoReasoningMeta) ResetAttempt() {
	if meta == nil {
		return
	}
	meta.Effort, meta.Reason, meta.Conflict, meta.PolicyEffort = "", "", "", ""
	meta.OutboundFields, meta.OutboundStatus = nil, ""
}

const maxObservedReasoningBodyBytes = 1 << 20

func (meta *AutoReasoningMeta) ObserveHTTPBody(request *http.Request) {
	if meta == nil || meta.Reason == "" {
		return
	}
	meta.OutboundFields = nil
	if request == nil || request.GetBody == nil {
		meta.OutboundStatus = "not_replayable"
		return
	}
	if request.ContentLength > maxObservedReasoningBodyBytes {
		meta.OutboundStatus = "body_too_large"
		return
	}
	body, err := request.GetBody()
	if err != nil {
		meta.OutboundStatus = "read_failed"
		return
	}
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, maxObservedReasoningBodyBytes+1))
	if err != nil {
		meta.OutboundStatus = "read_failed"
		return
	}
	meta.ObserveOutboundJSON(raw)
}

func (meta *AutoReasoningMeta) ObserveOutboundJSON(raw []byte) {
	if meta == nil || meta.Reason == "" {
		return
	}
	meta.OutboundFields = nil
	if len(raw) > maxObservedReasoningBodyBytes {
		meta.OutboundStatus = "body_too_large"
		return
	}
	meta.OutboundFields = OutboundReasoningFields(raw)
	meta.OutboundStatus = "observed"
	if meta.OutboundFields == nil {
		meta.OutboundStatus = "invalid_json"
	}
}

func OutboundReasoningFields(raw []byte) map[string]any {
	if !gjson.ValidBytes(raw) {
		return nil
	}
	fields := make(map[string]any)
	for _, path := range []string{
		"reasoning_effort", "reasoning.effort", "reasoning.summary", "reasoning.enabled", "reasoning.max_tokens",
		"thinking.type", "thinking.budget_tokens", "output_config.effort", "enable_thinking",
		"generationConfig.thinkingConfig.thinkingBudget", "generationConfig.thinkingConfig.thinkingLevel", "generationConfig.thinkingConfig.includeThoughts",
		"generation_config.thinking_config.thinking_budget", "generation_config.thinking_config.thinking_level", "generation_config.thinking_config.include_thoughts",
		"request.generationConfig.thinkingConfig.thinkingBudget", "request.generationConfig.thinkingConfig.thinkingLevel", "request.generationConfig.thinkingConfig.includeThoughts",
	} {
		value := gjson.GetBytes(raw, path)
		switch value.Type {
		case gjson.Number:
			fields[path] = value.Num
		case gjson.True, gjson.False:
			fields[path] = value.Bool()
		case gjson.String:
			switch strings.ToLower(value.Str) {
			case "none", "minimal", "low", "medium", "high", "xhigh", "max", "enabled", "disabled", "adaptive", "auto", "concise", "detailed":
				fields[path] = value.Str
			}
		}
	}
	return fields
}
