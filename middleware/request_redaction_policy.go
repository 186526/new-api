package middleware

import (
	"sync"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/QuantumNous/new-api/setting/redaction_setting"
)

type redactionPolicyKey struct {
	tokenID    int
	version    [32]byte
	guardrails redaction_setting.Policy
}

type compiledTokenRedaction struct {
	policy           *redaction.Policy
	restoreRequested bool
}

var compiledRedactionPolicies = struct {
	sync.Mutex
	entries map[redactionPolicyKey]compiledTokenRedaction
}{entries: make(map[redactionPolicyKey]compiledTokenRedaction)}

func compileTokenRedaction(token model.Token, guardrails redaction_setting.Policy) (*redaction.Policy, bool, error) {
	key := redactionPolicyKey{token.Id, token.RedactionPolicyVersion(), guardrails}
	cache := &compiledRedactionPolicies
	cache.Lock()
	cached, found := cache.entries[key]
	cache.Unlock()
	if found {
		return cached.policy, cached.restoreRequested, nil
	}
	encoded, err := token.RedactionConfigBytes()
	if err != nil {
		return nil, false, err
	}
	config, err := redaction.DecodeConfig(encoded)
	if err != nil {
		return nil, false, err
	}
	policy, err := redaction.Compile(config, guardrails.MaxCustomRulesPerKey, guardrails.AllowResponseRestore)
	if err != nil {
		return nil, false, err
	}
	cache.Lock()
	if len(cache.entries) >= 128 {
		for previous := range cache.entries {
			delete(cache.entries, previous)
			break
		}
	}
	cache.entries[key] = compiledTokenRedaction{policy, config.RestorePII}
	cache.Unlock()
	return policy, config.RestorePII, nil
}
