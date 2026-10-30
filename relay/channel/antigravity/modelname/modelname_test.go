package modelname

import "testing"

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
		{name: "xhigh collapses to high", model: "gemini-4-argon", effort: "xhigh", want: "gemini-4-argon-high"},
		{name: "max collapses to high", model: "gemini-4-argon", effort: "max", want: "gemini-4-argon-high"},
		{name: "unknown effort uses default", model: "gemini-4-argon", effort: "turbo", want: "gemini-4-argon-medium"},
		{name: "single level model keeps medium", model: "gpt-oss-120b", effort: "high", want: "gpt-oss-120b-medium"},
		{name: "legacy suffix passes through", model: "claude-sonnet-5-5-high", effort: "low", want: "claude-sonnet-5-5-high"},
		{name: "model without effort variants", model: "gemini-3-flash", effort: "high", want: "gemini-3-flash"},
		{name: "tiered variant passes through", model: "gemini-3.8-flash-tiered", effort: "low", want: "gemini-3.8-flash-tiered"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.model, tc.effort); got != tc.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", tc.model, tc.effort, got, tc.want)
			}
		})
	}
}

func TestCollapse(t *testing.T) {
	cases := map[string]string{
		"gemini-4-argon-high":      "gemini-4-argon",
		"claude-opus-5-5-low":      "claude-opus-5-5",
		"gpt-oss-120b-medium":      "gpt-oss-120b",
		"gemini-3.8-flash-tiered":  "gemini-3.8-flash-tiered",
		"gemini-3-flash":           "gemini-3-flash",
		"gemini-2.5-flash-thinking": "gemini-2.5-flash-thinking",
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
