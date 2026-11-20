package service

import (
	"reflect"
	"testing"
)

func TestUsableAntigravityModelsCollapsesEffortVariants(t *testing.T) {
	raw := map[string]any{
		"gemini-4-argon-high":   map[string]any{},
		"gemini-4-argon-low":    map[string]any{},
		"gemini-4-argon-medium": map[string]any{},
		"gemini-3-flash":        map[string]any{},
		"chat_20706":            map[string]any{},
		"  ":                    map[string]any{},
	}

	want := []string{"gemini-3-flash", "gemini-4-argon"}
	if got := usableAntigravityModels(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("usableAntigravityModels = %v, want %v", got, want)
	}
}

func TestUsableAntigravityModelsDropsDisabledModels(t *testing.T) {
	raw := map[string]any{
		"gemini-4-carbon-disabled": map[string]any{"displayName": "Gemini 4 Carbon", "disabled": true},
		"gemini-4-neon-high":       map[string]any{"displayName": "Gemini 4 Neon (High)"},
		"gemini-4-neon-xhigh":      map[string]any{"displayName": "Gemini 4 Neon (XHigh)", "disabled": false},
		"gemini-4-hydrogen-sonar":  map[string]any{"disabled": "false"},
		"claude-opus-5-5-medium":   "not-an-object",
		"gemini-2.5-pro":           map[string]any{"disabled": false},
	}

	want := []string{"claude-opus-5-5", "gemini-4-hydrogen-sonar", "gemini-4-neon"}
	if got := usableAntigravityModels(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("usableAntigravityModels = %v, want %v", got, want)
	}
}
