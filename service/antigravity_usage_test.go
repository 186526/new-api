package service

import "testing"

func TestParseAntigravityQuotaSummary(t *testing.T) {
	body := []byte(`{
		"groups": [
			{
				"displayName": "Gemini Models",
				"description": "Models within this group: Gemini Flash, Gemini Pro",
				"buckets": [
					{
						"bucketId": "gemini-weekly",
						"displayName": "Weekly Limit Remaining",
						"window": "weekly",
						"resetTime": "2026-10-31T07:42:19Z",
						"remainingFraction": 0.9680629
					},
					{
						"bucketId": "gemini-5h",
						"displayName": "Five Hour Limit Remaining",
						"window": "5h",
						"resetTime": "2026-10-30T13:52:32Z",
						"remainingFraction": 1
					}
				]
			},
			{"displayName": "Empty group", "buckets": []}
		]
	}`)

	groups := parseAntigravityQuotaSummary(body)
	if len(groups) != 1 {
		t.Fatalf("expected the empty group to be dropped, got %d groups", len(groups))
	}
	group := groups[0]
	if group.DisplayName != "Gemini Models" {
		t.Fatalf("unexpected group name %q", group.DisplayName)
	}
	if len(group.Buckets) != 2 {
		t.Fatalf("expected two buckets, got %d", len(group.Buckets))
	}
	weekly := group.Buckets[0]
	if weekly.BucketID != "gemini-weekly" || weekly.Window != "weekly" {
		t.Fatalf("unexpected first bucket %+v", weekly)
	}
	if weekly.RemainingFraction != 0.9680629 {
		t.Fatalf("unexpected remaining fraction %v", weekly.RemainingFraction)
	}
}

func TestParseAntigravityQuotaSummaryRejectsInvalidJSON(t *testing.T) {
	if groups := parseAntigravityQuotaSummary([]byte("not json")); groups != nil {
		t.Fatalf("expected nil groups, got %+v", groups)
	}
}

func TestParseAntigravityQuotaBuckets(t *testing.T) {
	body := []byte(`{
		"buckets": [
			{"tokenType": "WTUS", "modelId": "chat_20706", "remainingFraction": 1},
			{"tokenType": "WTUS", "modelId": "gemini-3-flash", "remainingFraction": 0.5, "resetTime": "2026-10-30T13:42:38Z"},
			{"tokenType": "WTUS", "modelId": "claude-opus-5-5-high", "remainingFraction": 0.25, "resetTime": "2026-10-30T13:42:38Z"},
			{"tokenType": "WTUS", "modelId": "  ", "remainingFraction": 1}
		]
	}`)

	buckets := parseAntigravityQuotaBuckets(body)
	if len(buckets) != 2 {
		t.Fatalf("expected internal and empty models to be dropped, got %d buckets", len(buckets))
	}
	if buckets[0].ModelID != "claude-opus-5-5-high" || buckets[1].ModelID != "gemini-3-flash" {
		t.Fatalf("expected buckets sorted by model id, got %+v", buckets)
	}
	if buckets[0].DisplayName != "claude-opus-5-5-high" {
		t.Fatalf("expected the model id to be used as the display name, got %q", buckets[0].DisplayName)
	}
}

func TestParseAntigravityQuotaBucketsRejectsInvalidJSON(t *testing.T) {
	if buckets := parseAntigravityQuotaBuckets([]byte("not json")); buckets != nil {
		t.Fatalf("expected nil buckets, got %+v", buckets)
	}
}
