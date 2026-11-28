package redaction

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRequestRedaction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RestorePII = true
	policy, err := Compile(cfg, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	const email = "alice@example.invalid"
	const secret = "sk-000000000000000000000000"
	input := []byte(`{"unknown":12345678901234567890,"messages":[{"role":"user","content":"` + email + `"},{"role":"assistant","tool_calls":[{"function":{"arguments":"{\"token\":\"` + secret + `\"}"}}]}]}`)
	out, err := session.RewriteJSON(input, Chat)
	if err != nil || !json.Valid(out) || strings.Contains(string(out), email) || strings.Contains(string(out), secret) || !strings.Contains(string(out), "12345678901234567890") {
		t.Fatal("request rewrite failed")
	}
	var obj struct{ Messages []struct{ Content string } }
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	token := obj.Messages[0].Content
	restorer := session.NewRestorer()
	if restorer.Push(token[:7], false) != "" || restorer.Push(token[7:], true) != email {
		t.Fatal("stream restoration failed")
	}
	if session.Counts()["api_keys"] != 1 {
		t.Fatal("credential count mismatch")
	}
}

func TestRequestRedactionGeminiScopes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Scopes.SystemPrompt = true
	policy, err := Compile(cfg, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"contents":[{"parts":[{"text":"customer@example.invalid"}]},{"role":"model","parts":[{"text":"assistant@example.invalid"}]}],"systemInstruction":{"parts":[{"text":"system@example.invalid"}]}}`)
	out, err := session.RewriteJSON(body, Gemini)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "customer@example.invalid") || strings.Contains(string(out), "system@example.invalid") || !strings.Contains(string(out), "assistant@example.invalid") {
		t.Fatal("Gemini text was rewritten outside the selected scopes")
	}
}

func TestRequestRedactionGeminiBatchScopes(t *testing.T) {
	body := []byte(`{"requests":[
		{"role":"tool","contents":[
			{"parts":[{"text":"implicit@example.invalid"}]},
			{"role":"user","parts":[{"text":"explicit@example.invalid"}]},
			{"role":"model","parts":[{"text":"model@example.invalid"},{"functionCall":{"name":"fixture_call","args":{"nested":{"name":"arg@example.invalid"}}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"fixture_call","response":{"nested":{"id":"result@example.invalid"}}}}]}
		],"systemInstruction":{"parts":[{"text":"system@example.invalid"}]}},
		{"request":{"contents":[{"parts":[{"text":"wrapped@example.invalid"}]}],"system_instruction":{"parts":[{"text":"wrapped-system@example.invalid"}]}}}
	]}`)
	for _, test := range []struct {
		name               string
		user, tool, system bool
	}{
		{"user only", true, false, false},
		{"tool only", false, true, false},
		{"system only", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Scopes.UserMessages = test.user
			cfg.Scopes.ToolIO = test.tool
			cfg.Scopes.SystemPrompt = test.system
			policy, err := Compile(cfg, 10, false)
			if err != nil {
				t.Fatal(err)
			}
			session, err := policy.NewSession(nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := session.RewriteJSON(body, Gemini)
			if err != nil {
				t.Fatal(err)
			}
			for fixture, redact := range map[string]bool{
				"implicit": test.user, "explicit": test.user, "wrapped": test.user,
				"model": false, "arg": test.tool, "result": test.tool,
				"system": test.system, "wrapped-system": test.system,
			} {
				if strings.Contains(string(out), `"`+fixture+`@example.invalid"`) == redact {
					t.Errorf("unexpected redaction for fixture %q (want redacted: %v)", fixture, redact)
				}
			}
			if !strings.Contains(string(out), `"name":"fixture_call"`) {
				t.Error("tool name was not preserved")
			}
		})
	}
}

func TestRequestRedactionRefusesEncodedFetchCredential(t *testing.T) {
	policy, err := Compile(DefaultConfig(), 10, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.RewriteJSON([]byte(`{"url":"https://example.invalid/?value=alice%40example.invalid"}`), Fetch)
	if !errors.Is(err, ErrSensitiveURL) {
		t.Fatalf("expected protected target rejection: %v", err)
	}
}

func TestRequestRedactionNeverRestoresOverlappingCustomMatch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RestorePII = true
	cfg.CustomRules = []CustomRule{{ID: "custom", Label: "TAG", Pattern: `example\.invalid`}}
	policy, err := Compile(cfg, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	text, err := session.Redact("alice@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "example.invalid") || session.NewRestorer().Push(text, true) != text {
		t.Fatal("custom match was exposed")
	}
}

func TestRequestRedactionRejectsAmbiguousFields(t *testing.T) {
	policy, err := Compile(DefaultConfig(), 10, false)
	if err != nil {
		t.Fatal(err)
	}
	session, err := policy.NewSession(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.RewriteJSON([]byte(`{"messages":[{"role":"user","content":"first","Content":"second"}]}`), Chat)
	if !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("expected refusal of ambiguous fields, got %v", err)
	}
	out, err := session.RewriteJSON([]byte(`{"Messages":[{"Role":"user","Content":"sample@example.invalid"}]}`), Chat)
	if err != nil || strings.Contains(string(out), "sample@example.invalid") || !strings.Contains(string(out), "EMAIL_") {
		t.Fatal("case-insensitive fields were not redacted")
	}
}

func TestRequestRedactionInvalidRuleDoesNotEchoPattern(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CustomRules = []CustomRule{{ID: "custom", Label: "TAG", Pattern: `(?<=PRIVATE)`}}
	_, err := Compile(cfg, 10, true)
	var field *FieldError
	if !errors.As(err, &field) || field.Field != "custom_rules.0.pattern" || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("unsafe validation error")
	}
}
