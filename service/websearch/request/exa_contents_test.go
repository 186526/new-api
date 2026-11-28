package request

import (
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeExaContents(t *testing.T) {
	const target = "https://example.org/page?a=1"
	tests := []struct {
		name string
		body string
		want string
	}{
		{"target after unrelated result", `{"results":[{"url":"https://example.org/other","text":"wrong"},{"url":"https://EXAMPLE.org:443/page?a=1#top","text":"right"}]}`, "right"},
		{"different query", `{"results":[{"url":"https://example.org/page?a=2","text":"wrong"}]}`, ""},
		{"wrong identifier", `{"results":[{"url":"https://example.org/page?a=1","id":"https://example.org/other","text":"wrong"}]}`, ""},
		{"opaque identifier and exact URL", `{"results":[{"url":"https://example.org/page?a=1","id":"document-42","text":"right"}]}`, "right"},
		{"target failed", `{"statuses":[{"id":"https://example.org/page?a=1","status":"error"}],"results":[{"url":"https://example.org/page?a=1","text":"wrong"}]}`, ""},
		{"conflicting statuses", `{"statuses":[{"id":"https://example.org/page?a=1","status":"success"},{"id":"https://example.org/page?a=1","status":"error"}],"results":[{"url":"https://example.org/page?a=1","text":"wrong"}]}`, ""},
		{"document status failed", `{"statuses":[{"id":"https://example.org/page?a=1","status":"success"},{"id":"document-42","status":"error"}],"results":[{"url":"https://example.org/page?a=1","id":"document-42","text":"wrong"}]}`, ""},
		{"document identity confirms exact URL", `{"statuses":[{"id":"document-42","status":"success"}],"results":[{"url":"https://example.org/page?a=1","id":"document-42","text":"right"}]}`, "right"},
		{"summary only", `{"results":[{"url":"https://example.org/page?a=1","summary":"wrong"}]}`, ""},
		{"document and status confirm redirect", `{"statuses":[{"id":"https://example.org/page?a=1","status":"success"}],"results":[{"id":"https://example.org/page?a=1","url":"https://example.net/new","text":"right"}]}`, "right"},
		{"unverified redirect", `{"statuses":[{"id":"https://example.org/page?a=1","status":"success"}],"results":[{"id":"document-42","url":"https://example.net/new","text":"wrong"}]}`, ""},
		{"invalid redirect target", `{"statuses":[{"id":"https://example.org/page?a=1","status":"success"}],"results":[{"id":"https://example.org/page?a=1","url":"https://example.org@other.net/new","text":"wrong"}]}`, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeExaContents(target, forwardEntry{StatusCode: http.StatusOK, Body: []byte(test.body)})
			if err != nil {
				t.Fatal(err)
			}
			if test.want == "" {
				if len(got.Body) != 0 {
					t.Fatalf("unverified response could be cached: %s", got.Body)
				}
				return
			}
			if !strings.Contains(string(got.Body), test.want) || strings.Contains(string(got.Body), "wrong") {
				t.Fatalf("response: %s", got.Body)
			}
		})
	}
}

func TestNormalizeExaContentsPreservesSentFailures(t *testing.T) {
	const invalidBody = `{"results":{}}`
	refusal := forwardEntry{StatusCode: http.StatusTooManyRequests, Body: []byte(invalidBody)}
	if got, err := normalizeExaContents("https://example.org", refusal); err != nil || got.StatusCode != refusal.StatusCode || string(got.Body) != invalidBody {
		t.Fatalf("non-2xx refusal changed: %+v, %v", got, err)
	}
	got, err := normalizeExaContents("https://example.org", forwardEntry{StatusCode: http.StatusOK, Body: []byte(invalidBody)})
	if err == nil || !WasSent(err) || len(got.Body) != 0 {
		t.Fatalf("malformed response must not be retried or cached: %+v, %v", got, err)
	}
}
