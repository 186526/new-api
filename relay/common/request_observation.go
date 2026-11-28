package common

type ToolExecutionMeta struct {
	Contract string `json:"contract"`
	Provider string `json:"provider,omitempty"`
	Outcome  string `json:"outcome,omitempty"`
	CacheHit bool   `json:"cache_hit"`
	Refund   string `json:"refund,omitempty"`
}

type RequestRedactionMeta struct {
	Matches    int `json:"matches"`
	Categories int `json:"categories"`
}

type RequestObservation struct {
	Tool               *ToolExecutionMeta
	Image              *ImageCompressionMeta
	Redaction          *RequestRedactionMeta
	AutoEffort         string
	AutoScale          int
	AutoReason         string
	AutoConflict       string
	AutoPolicyEffort   string
	AutoOutboundFields map[string]any
	AutoOutboundStatus string
	AutoTier           *int
}

func (info *RelayInfo) RequestObservation() RequestObservation {
	if info == nil {
		return RequestObservation{}
	}
	observation := RequestObservation{Tool: safeToolExecution(info.ToolExecution)}
	if image := info.ImageCompression; image != nil {
		copy := *image
		copy.evidence = nil
		copy.Reason = observationValue(image.Reason, "passthrough", "excluded", "unsupported", "budget", "failed", "retained", "compressed")
		copy.OutboundStatus = observationValue(image.OutboundStatus, "observed", "unconfirmed", "not_replayable", "body_too_large", "read_failed", "invalid_json", "unsupported")
		if copy.Reason == "compressed" && copy.OutboundStatus == "" {
			copy.OutboundStatus = "not_observed"
		}
		observation.Image = &copy
	}
	if redaction := info.RequestRedaction; redaction != nil {
		observation.Redaction = &RequestRedactionMeta{Matches: max(0, redaction.Matches), Categories: max(0, redaction.Categories)}
	}
	if automatic := info.AutoReasoning; automatic != nil {
		observation.AutoScale = max(0, automatic.Scale)
		observation.AutoEffort = observationValue(automatic.Effort, "none", "minimal", "low", "medium", "high", "xhigh")
		observation.AutoReason = observationValue(automatic.Reason, "applied", "passthrough", "unsupported_model", "blacklisted", "model_modifier", "group_excluded", "model_excluded", "explicit_disable", "explicit_strength", "within_cap")
		observation.AutoConflict = observationValue(automatic.Conflict, "applied", "capped", "overridden", "explicit_disable", "explicit_strength", "within_cap")
		observation.AutoPolicyEffort = observationValue(automatic.PolicyEffort, "none", "minimal", "low", "medium", "high", "xhigh")
		if automatic.Rules != nil && observation.AutoReason != "" {
			_, tier := automatic.Rules.Tier(observation.AutoScale)
			observation.AutoTier = &tier
		}
		observation.AutoOutboundFields = automatic.OutboundFields
		observation.AutoOutboundStatus = observationValue(automatic.OutboundStatus, "observed", "not_replayable", "body_too_large", "read_failed", "invalid_json", "not_observed")
		if observation.AutoReason != "" && observation.AutoOutboundStatus == "" {
			observation.AutoOutboundStatus = "not_observed"
		}
	}
	return observation
}

func safeToolExecution(tool *ToolExecutionMeta) *ToolExecutionMeta {
	if tool == nil {
		return nil
	}
	contract := observationValue(tool.Contract, "tools/search", "tools/fetch", "alpha/search", "moonshot/search", "moonshot/fetch")
	if contract == "" {
		return nil
	}
	return &ToolExecutionMeta{
		Contract: contract,
		Provider: observationValue(tool.Provider, "deepseek", "codex", "antigravity", "exa", "local", "gemini", "vertex", "zhipu", "claude", "aws", "openai", "azure", "xai", "moonshot", "perplexity", "ali", "baidu_v2", "advanced_custom"),
		Outcome:  observationValue(tool.Outcome, "success", "empty", "upstream_error", "unavailable"),
		CacheHit: tool.CacheHit,
		Refund:   observationValue(tool.Refund, "not_needed", "requested", "completed", "failed"),
	}
}

func observationValue(value string, allowed ...string) string {
	for _, valid := range allowed {
		if value == valid {
			return valid
		}
	}
	return ""
}
