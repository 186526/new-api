package abstract

import (
	"net/url"
	"regexp"
	"strings"
)

// The Antigravity endpoint has no search action: it runs generateContent with a
// googleSearch tool, so the only way to get results is to have the model report
// what the tool returned. What it is asked to report matters:
//
//   - Asking for the tool's whole output runs into the upstream copyright
//     filter, which answers finishReason=RECITATION with no body for queries
//     whose pages it recognises (measured: "golang" failed every time).
//   - Asking for a written answer returns a summary, not a result list.
//
// URL, publication time and page title are short, factual and not prose, so they
// pass the filter, and they are what a caller cannot recompute. Measured on the
// live upstream: six out of six queries answered, nine lines each, every one
// finishing with STOP.
//
//	https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI... | July 17 2026 | The Go Programming Language
//	https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI... | None | Linux.org

// antigravityURLLine reads one "url | time | title" line. The title is taken as
// the rest of the line, because a page title may contain a pipe.
var antigravityURLLine = regexp.MustCompile(`(?m)^\s*(https?://\S+)\s*\|\s*([^|\n]*)\|\s*(.+?)\s*$`)

// antigravityGluedSeparator repairs the one malformation the model produces
// regularly: the first separator comes out as the literal text "data:" and is
// written without spaces, so the URL and the separator form one token
// ("...D8Adata:None|Title"). Reading that token as a URL yields a link that
// answers 404, which is why a query could come back with nine unresolvable
// short links. The colon cannot occur inside a grounding short link, whose
// payload is base64url, so the match is unambiguous.
var antigravityGluedSeparator = regexp.MustCompile(`(?m)^(\s*https?://\S*?)data:`)

// antigravityRawResult is one line of the model's answer.
type antigravityRawResult struct {
	URL             string
	PublicationTime string
	Title           string
}

// parseAntigravityRawSearch reads the "url | time | title" lines out of a
// candidate's text. It reports false when the text carries no such line, which
// is what a model answering in prose or refusing outright produces.
func parseAntigravityRawSearch(text string) ([]antigravityRawResult, bool) {
	text = antigravityGluedSeparator.ReplaceAllString(text, "$1 | ")
	matches := antigravityURLLine.FindAllStringSubmatch(text, -1)
	results := make([]antigravityRawResult, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		target := strings.TrimSpace(match[1])
		if target == "" {
			continue
		}
		if _, duplicated := seen[target]; duplicated {
			continue
		}
		seen[target] = struct{}{}
		results = append(results, antigravityRawResult{
			URL:             target,
			PublicationTime: normalizeAntigravityTime(match[2]),
			Title:           strings.TrimSpace(match[3]),
		})
	}
	return results, len(results) > 0
}

// normalizeAntigravityTime maps the model's "None" to empty, so a hit without a
// date reads as one rather than carrying the word into the caller's date field.
func normalizeAntigravityTime(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "", "none", "null", "n/a", "unknown", "-":
		return ""
	}
	return trimmed
}

// antigravityHostOf names the site a citation points at. The redirect service
// hides the page's own host, so the citation's host is all that is available
// before the link is resolved.
func antigravityHostOf(target string) string {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
