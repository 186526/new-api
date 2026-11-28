package image_compression_setting

import "testing"

func TestCompressionScopeExclusionsTakePrecedence(t *testing.T) {
	settings, err := DecodeOptions(map[string]string{
		Prefix + "enabled":                "true",
		Prefix + "channel_ids":            "12, 14",
		Prefix + "model_patterns":         "gpt-*",
		Prefix + "exclude_channel_ids":    "14",
		Prefix + "exclude_model_patterns": "mapped-private-*",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !settings.ThresholdEnabled {
		t.Fatal("legacy settings must retain the threshold")
	}
	if !settings.Applies(12, "gpt-public") || settings.Applies(15, "gpt-public") || settings.Applies(12, "other-model") {
		t.Fatal("inclusion must still require both channel and model to match")
	}
	if settings.Applies(14, "gpt-public") || settings.Applies(12, "gpt-public", "mapped-private-model") {
		t.Fatal("an excluded channel or model name must override inclusion")
	}
}

func TestCompressionScopeRejectsInvalidExclusions(t *testing.T) {
	for key, invalid := range map[string]string{
		"exclude_channel_ids":    "0",
		"exclude_model_patterns": "model-[",
	} {
		t.Run(key, func(t *testing.T) {
			if _, err := DecodeOptions(map[string]string{Prefix + key: invalid}); err == nil {
				t.Fatalf("accepted %q in %s", invalid, key)
			}
		})
	}
}

func TestDraftAllowsTargetAboveDisabledDimensionThreshold(t *testing.T) {
	settings, err := DecodeDraft([]byte(`{"threshold_enabled":false,"max_dimension":4096}`))
	if err != nil || settings.ThresholdEnabled {
		t.Fatalf("settings=%+v, err=%v", settings, err)
	}
}
