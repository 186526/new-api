package service

import (
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func appendRequestObservation(info *relaycommon.RelayInfo, other *model.LogOther) {
	observed := info.RequestObservation()
	if tool := observed.Tool; tool != nil {
		other.SetAdmin("tool_execution", *tool)
		public := *tool
		public.Provider = ""
		other.SetPublic("tool_execution", public)
	}
	if image := observed.Image; image != nil {
		other.SetPublic("image_compression", *image)
	}
	if redaction := observed.Redaction; redaction != nil {
		other.SetPublic("request_redaction", *redaction)
	}
	if observed.AutoReason != "" || observed.AutoEffort != "" {
		reasoning := map[string]any{"effort": observed.AutoEffort, "scale": observed.AutoScale, "reason": observed.AutoReason}
		if observed.AutoConflict != "" {
			reasoning["conflict_resolution"] = observed.AutoConflict
		}
		if observed.AutoPolicyEffort != "" {
			reasoning["policy_effort"] = observed.AutoPolicyEffort
		}
		if observed.AutoTier != nil {
			reasoning["tier"] = *observed.AutoTier
		}
		if observed.AutoOutboundStatus != "" {
			reasoning["outbound_status"] = observed.AutoOutboundStatus
		}
		if observed.AutoOutboundFields != nil {
			reasoning["outbound_fields"] = observed.AutoOutboundFields
		}
		other.SetPublic("automatic_reasoning", reasoning)
	}
}
