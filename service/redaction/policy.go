package redaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const MaxTextBytes = 256 << 10
const MaxPolicyBytes = 24 << 10

var ErrLimit = errors.New("redaction processing limit exceeded")
var ErrInvalidDocument = errors.New("invalid redaction document")

type Config struct {
	Strategy     string       `json:"strategy"`
	BuiltinRules []string     `json:"builtin_rules"`
	Scopes       Scopes       `json:"scopes"`
	Allowlist    []string     `json:"allowlist"`
	CustomRules  []CustomRule `json:"custom_rules"`
	RestorePII   bool         `json:"restore_pii_in_response"`
}

type Scopes struct {
	UserMessages bool `json:"user_messages"`
	ToolIO       bool `json:"tool_io"`
	SystemPrompt bool `json:"system_prompt"`
}

type CustomRule struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement,omitempty"`
}

type FieldError struct {
	Field      string
	Reason     string
	MessageKey string
}

func (e *FieldError) Error() string {
	return "invalid redaction configuration: " + e.Field + ": " + e.Reason
}

func DefaultConfig() Config {
	return Config{Strategy: "typed_placeholder", BuiltinRules: []string{"email", "phone", "cn_id", "credit_card", "api_keys", "cloud_tokens", "dsn_secret", "jwt_bearer", "pem_private_key"},
		Scopes: Scopes{UserMessages: true, ToolIO: true}, Allowlist: []string{}, CustomRules: []CustomRule{}}
}

func DecodeConfig(data []byte) (Config, error) {
	if len(data) == 0 {
		return DefaultConfig(), nil
	}
	if len(data) > MaxPolicyBytes {
		return Config{}, ErrLimit
	}
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, ErrInvalidDocument
	}
	if dec.Decode(new(any)) != io.EOF {
		return Config{}, ErrInvalidDocument
	}
	return cfg, nil
}

type rule struct {
	id          string
	label       string
	pattern     *regexp.Regexp
	group       int
	valid       func(string) bool
	restorable  bool
	replacement string
}
type Policy struct {
	strategy   string
	scopes     Scopes
	rules      []rule
	literals   map[string]bool
	exceptions []*regexp.Regexp
	restore    bool
}

var ruleLabel = regexp.MustCompile(`^[A-Z0-9_]{2,24}$`)

func Compile(cfg Config, maxRules int, allowRestore bool) (*Policy, error) {
	invalid := func(field, key, reason string) (*Policy, error) {
		return nil, &FieldError{Field: field, Reason: reason, MessageKey: "redaction." + key}
	}
	if cfg.Strategy != "typed_placeholder" && cfg.Strategy != "mask" && cfg.Strategy != "hmac_short" {
		return invalid("strategy", "unsupported_strategy", "unsupported strategy")
	}
	if !cfg.Scopes.UserMessages && !cfg.Scopes.ToolIO && !cfg.Scopes.SystemPrompt {
		return invalid("scopes", "scope_required", "select at least one scope")
	}
	if len(cfg.BuiltinRules)+len(cfg.CustomRules) == 0 {
		return invalid("builtin_rules", "rule_required", "select at least one rule")
	}
	if len(cfg.CustomRules) > maxRules || len(cfg.CustomRules) > 10 {
		return invalid("custom_rules", "too_many_rules", "too many rules")
	}
	if len(cfg.Allowlist) > 50 {
		return invalid("allowlist", "too_many_allowlist_entries", "too many entries")
	}
	policy := &Policy{strategy: cfg.Strategy, scopes: cfg.Scopes, literals: map[string]bool{}, restore: cfg.RestorePII && allowRestore && cfg.Strategy == "typed_placeholder"}
	seen := map[string]bool{}
	for _, id := range cfg.BuiltinRules {
		if seen[id] || !builtinIDs[id] {
			return invalid("builtin_rules", "invalid_builtin_rule", "unknown or repeated rule")
		}
		seen[id] = true
	}
	for _, candidate := range builtin {
		if seen[candidate.id] {
			policy.rules = append(policy.rules, candidate)
		}
	}
	ids := map[string]bool{}
	for i, custom := range cfg.CustomRules {
		field := fmt.Sprintf("custom_rules.%d", i)
		if len(custom.ID) > 64 || ids[custom.ID] || custom.ID == "" {
			return invalid(field+".id", "invalid_rule_id", "invalid or repeated rule ID")
		}
		ids[custom.ID] = true
		if !ruleLabel.MatchString(custom.Label) {
			return invalid(field+".label", "invalid_rule_label", "use 2–24 uppercase letters, digits or underscores")
		}
		if len(custom.Replacement) > 128 {
			return invalid(field+".replacement", "replacement_too_long", "replacement is too long")
		}
		pattern, err := compilePattern(custom.Pattern, 256)
		if err != nil {
			return invalid(field+".pattern", "invalid_pattern", "use a valid nonempty RE2 pattern")
		}
		policy.rules = append(policy.rules, rule{id: "custom", label: custom.Label, pattern: pattern, replacement: custom.Replacement})
	}
	for i, value := range cfg.Allowlist {
		field := fmt.Sprintf("allowlist.%d", i)
		if len(value) == 0 || len(value) > 128 {
			return invalid(field, "invalid_allowlist_entry", "entry must contain 1–128 bytes")
		}
		if pattern, ok := strings.CutPrefix(value, "re:"); ok {
			re, err := compilePattern(pattern, 125)
			if err != nil {
				return invalid(field, "invalid_pattern", "use a valid nonempty RE2 pattern")
			}
			policy.exceptions = append(policy.exceptions, re)
		} else {
			policy.literals[value] = true
		}
	}
	return policy, nil
}

func compilePattern(pattern string, limit int) (*regexp.Regexp, error) {
	if len(pattern) == 0 || len(pattern) > limit {
		return nil, ErrInvalidDocument
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil || compiled.MatchString("") {
		return nil, ErrInvalidDocument
	}
	return compiled, nil
}

func (p *Policy) Scopes() Scopes    { return p.scopes }
func (p *Policy) RestoresPII() bool { return p.restore }
