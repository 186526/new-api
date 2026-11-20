package modelname

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name   string
		model  string
		effort string
		want   string
	}{
		{name: "explicit low", model: "gemini-3.8-flash", effort: "low", want: "gemini-3.8-flash-low"},
		{name: "explicit high", model: "gemini-3.8-flash", effort: "high", want: "gemini-3.8-flash-high"},
		{name: "no effort uses default", model: "gemini-3.8-flash", effort: "", want: "gemini-3.8-flash-medium"},
		{name: "case and spaces", model: "claude-opus-5-5", effort: "  HIGH ", want: "claude-opus-5-5-high"},
		{name: "none collapses to low", model: "claude-sonnet-5-5", effort: "none", want: "claude-sonnet-5-5-low"},
		{name: "minimal collapses to low", model: "gemini-4-argon", effort: "minimal", want: "gemini-4-argon-low"},
		{name: "argon has no xhigh so xhigh falls back to high", model: "gemini-4-argon", effort: "xhigh", want: "gemini-4-argon-high"},
		{name: "argon has no xhigh so max falls back to high", model: "gemini-4-argon", effort: "max", want: "gemini-4-argon-high"},
		{name: "neon keeps xhigh", model: "gemini-4-neon", effort: "xhigh", want: "gemini-4-neon-xhigh"},
		{name: "neon maps max to xhigh", model: "gemini-4-neon", effort: "max", want: "gemini-4-neon-xhigh"},
		{name: "neon low", model: "gemini-4-neon", effort: "low", want: "gemini-4-neon-low"},
		{name: "no effort uses neon default", model: "gemini-4-neon", effort: "", want: "gemini-4-neon-medium"},
		{name: "hydrogen keeps xhigh", model: "gemini-4-hydrogen", effort: "xhigh", want: "gemini-4-hydrogen-xhigh"},
		{name: "hydrogen low", model: "gemini-4-hydrogen", effort: "low", want: "gemini-4-hydrogen-low"},
		{name: "no effort uses tiered default", model: "gemini-4-hydrogen-tiered", effort: "", want: "gemini-4-hydrogen-tiered-high"},
		{name: "tiered low downgrades to high", model: "gemini-4-hydrogen-tiered", effort: "low", want: "gemini-4-hydrogen-tiered-high"},
		{name: "tiered keeps max as xhigh", model: "gemini-4-hydrogen-tiered", effort: "max", want: "gemini-4-hydrogen-tiered-xhigh"},
		{name: "sonar is sent as listed", model: "gemini-4-hydrogen-sonar", effort: "high", want: "gemini-4-hydrogen-sonar"},
		{name: "unknown effort uses default", model: "gemini-4-argon", effort: "turbo", want: "gemini-4-argon-medium"},
		{name: "single level model keeps medium", model: "gpt-oss-120b", effort: "high", want: "gpt-oss-120b-medium"},
		{name: "legacy suffix passes through", model: "claude-sonnet-5-5-high", effort: "low", want: "claude-sonnet-5-5-high"},
		{name: "model without effort variants", model: "gemini-3-flash", effort: "high", want: "gemini-3-flash"},
		{name: "tiered variant passes through", model: "gemini-3.8-flash-tiered", effort: "low", want: "gemini-3.8-flash-tiered"},
		{name: "disabled model is not resolved", model: "gemini-4-carbon", effort: "high", want: "gemini-4-carbon"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.model, tc.effort); got != tc.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
			}
		})
	}
}

func TestResolveStaysInsideDeclaredLevels(t *testing.T) {
	for _, model := range models {
		for effort := range effortLevels {
			resolved, ok := trimLevel(Resolve(model.Name, effort), model.Name)
			if !ok {
				t.Errorf("Resolve(%q, %q) = %q, want the base name plus one of its levels", model.Name, effort, resolved)
				continue
			}
			if len(model.Levels) > 0 && !contains(model.Levels, resolved) {
				t.Errorf("Resolve(%q, %q) picked undeclared level %q", model.Name, effort, resolved)
			}
		}
	}
}

func TestCollapse(t *testing.T) {
	cases := map[string]string{
		"gemini-4-argon-high":            "gemini-4-argon",
		"claude-opus-5-5-low":            "claude-opus-5-5",
		"gpt-oss-120b-medium":            "gpt-oss-120b",
		"gemini-4-neon-xhigh":            "gemini-4-neon",
		"gemini-4-neon-low":              "gemini-4-neon",
		"gemini-4-hydrogen-xhigh":        "gemini-4-hydrogen",
		"gemini-4-hydrogen-tiered-high":  "gemini-4-hydrogen-tiered",
		"gemini-4-hydrogen-tiered-xhigh": "gemini-4-hydrogen-tiered",
		"gemini-4-hydrogen-sonar-xhigh":  "gemini-4-hydrogen-sonar-xhigh",
		"gemini-4-hydrogen-sonar":        "gemini-4-hydrogen-sonar",
		"gemini-3.8-flash-tiered":        "gemini-3.8-flash-tiered",
		"gemini-3-flash":                 "gemini-3-flash",
		"gemini-2.5-flash-thinking":      "gemini-2.5-flash-thinking",
		"gemini-4-carbon-disabled":       "gemini-4-carbon-disabled",
	}

	for input, want := range cases {
		if got := Collapse(input); got != want {
			t.Errorf("Collapse(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestListExposesBaseNames(t *testing.T) {
	names := List()
	if len(names) != len(models) {
		t.Fatalf("List() returned %d names, want %d", len(names), len(models))
	}
	for _, name := range names {
		if Collapse(name) != name {
			t.Errorf("List() contains effort variant %q", name)
		}
	}
}

// trimLevel 去掉模型名末尾声明的档位后缀，返回剩下的档位。
func trimLevel(resolved, base string) (string, bool) {
	if resolved == base {
		return "", true
	}
	suffix, found := strings.CutPrefix(resolved, base+"-")
	return suffix, found
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestModelsDeclareDefaultsAndUniqueLevels(t *testing.T) {
	names := make(map[string]struct{}, len(models))
	for _, model := range models {
		if _, duplicate := names[model.Name]; duplicate {
			t.Errorf("models lists %q twice", model.Name)
		}
		names[model.Name] = struct{}{}
		if len(model.Levels) == 0 || contains(model.Levels, model.DefaultLevel) {
			continue
		}
		t.Errorf("%s defaults to %q but declares only %v", model.Name, model.DefaultLevel, model.Levels)
	}
}

func TestOnlyUpstreamXHighModelsDeclareXHigh(t *testing.T) {
	want := map[string]bool{"gemini-4-neon": true, "gemini-4-hydrogen": true, "gemini-4-hydrogen-tiered": true}
	for _, model := range models {
		if got := contains(model.Levels, LevelXHigh); got != want[model.Name] {
			t.Errorf("%s declares xhigh = %v, want %v", model.Name, got, want[model.Name])
		}
	}
}
