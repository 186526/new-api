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
