package redaction_setting

import (
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const MaxRules = 10

type Policy struct {
	Enabled              bool `json:"enabled"`
	MaxCustomRulesPerKey int  `json:"max_custom_rules_per_key"`
	AllowResponseRestore bool `json:"allow_response_restore"`
}

var settings = Policy{Enabled: true, MaxCustomRulesPerKey: MaxRules, AllowResponseRestore: true}
var active atomic.Value

func init() {
	active.Store(settings)
	config.GlobalConfig.Register("redaction_setting", &settings)
}

func Get() Policy { return active.Load().(Policy) }

// UpdateAndSync runs under the option/config update lock after validation.
func UpdateAndSync() {
	next := settings
	if next.MaxCustomRulesPerKey < 0 || next.MaxCustomRulesPerKey > MaxRules {
		next.MaxCustomRulesPerKey = 0
	}
	active.Store(next)
}

const InputProtocolVersion = 1
const RestoreProtocolVersion = 1

type RuntimeCapabilities struct {
	Role                   string `json:"role"`
	InputProtocolVersion   int    `json:"input_protocol_version"`
	RestoreProtocolVersion int    `json:"restore_protocol_version"`
	RestoreDispatchEnabled bool   `json:"restore_dispatch_enabled"`
}

func (state ReadinessState) Runtime() RuntimeCapabilities {
	result := RuntimeCapabilities{Role: common.ServerRole}
	if result.Role == common.ServerRoleProxy || result.Role == common.ServerRoleAll {
		result.InputProtocolVersion = InputProtocolVersion
		result.RestoreProtocolVersion = RestoreProtocolVersion
		result.RestoreDispatchEnabled = state.RestoreDispatch
	}
	return result
}
