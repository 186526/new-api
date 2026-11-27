package abstract

import (
	"net/url"
	"regexp"
	"strings"
	"time"
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
// URL, publication time, page title and one sentence in the model's own words
// are short, factual and not reproduced prose, so they pass the filter, and they
// are what a caller cannot recompute. Measured on the live upstream: ten queries,
// ninety lines, every one finishing with STOP and parsing into four fields.
//
//	https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI... || July 17 2026 || The Go Programming Language || Official portal for downloads and documentation
//	https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI... || None || Linux.org || Home of the Linux operating system project
//
// The separator is a double pipe because a page title may contain a single one:
// with "|" every one of 108 measured lines carried an extra pipe and the four
// fields could not be split apart.

// antigravityURLColumns reads one four-column line. The last column is taken as
// the rest of the line, because only it may contain a pipe.
var antigravityURLColumns = regexp.MustCompile(`(?m)^\s*(https?://\S+)\s*\|\|\s*([^|]*)\|\|\s*(.+?)\s*$`)

// antigravityURLTitleColumns reads a three-column line, which is what this
// endpoint asked for before the fourth column existed. Answers cached by the
// previous build still carry this shape, so they are still read.
var antigravityURLTitleColumns = regexp.MustCompile(`(?m)^\s*(https?://\S+)\s*\|\s*([^|\n]*)\|\s*(.+?)\s*$`)

// antigravityGluedSeparator repairs the one malformation the model produces
// regularly: the first separator comes out as the literal text "data:" and is
// written without spaces, so the URL and the separator form one token
// ("...D8Adata:None|Title"). Reading that token as a URL yields a link that
// answers 404, which is why a query could come back with nine unresolvable
// short links. The colon cannot occur inside a grounding short link, whose
// payload is base64url, so the match is unambiguous.
var antigravityGluedSeparator = regexp.MustCompile(`(?m)^(\s*https?://\S*?)data:`)

// antigravityRawResult is one line of the model's answer. Summary is the model's
// own sentence about the page, which is what a hit falls back to when its page
// cannot be read for a lead paragraph.
type antigravityRawResult struct {
	URL             string
	PublicationTime string
	Title           string
	Summary         string
}

// parseAntigravityRawSearch reads the result lines out of a candidate's text. It
// reports false when the text carries no such line, which is what a model
// answering in prose or refusing outright produces.
func parseAntigravityRawSearch(text string) ([]antigravityRawResult, bool) {
	text = antigravityGluedSeparator.ReplaceAllString(text, "$1 | ")
	if results := antigravityResultsFrom(text, antigravityURLColumns, true); len(results) > 0 {
		return results, true
	}
	// An answer written before the fourth column existed, or one the model wrote
	// in the older shape, still names real pages.
	results := antigravityResultsFrom(text, antigravityURLTitleColumns, false)
	return results, len(results) > 0
}

// antigravityResultsFrom reads one line shape. A four-column line splits the
// last column at its final " || ", because a page title may hold a single pipe
// and the model sentence is written last.
func antigravityResultsFrom(text string, pattern *regexp.Regexp, fourColumns bool) []antigravityRawResult {
	matches := pattern.FindAllStringSubmatch(text, -1)
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
		hit := antigravityRawResult{
			URL:             target,
			PublicationTime: normalizeAntigravityTime(match[2]),
			Title:           strings.TrimSpace(match[3]),
		}
		if fourColumns {
			hit.Title, hit.Summary = splitAntigravityTitleAndSummary(match[3])
		}
		results = append(results, hit)
	}
	return results
}

// splitAntigravityTitleAndSummary cuts the last column into the page title and
// the model's sentence. Without a separator the column is the title alone, which
// leaves the hit to the page fetch for its snippet.
func splitAntigravityTitleAndSummary(last string) (string, string) {
	const separator = " || "
	cut := strings.LastIndex(last, separator)
	if cut < 0 {
		return strings.TrimSpace(last), ""
	}
	return strings.TrimSpace(last[:cut]), strings.TrimSpace(last[cut+len(separator):])
}

// antigravityTimeLayouts are the shapes the model writes for a publication date.
// It answers in English month names, sometimes with a leading zero on the day.
var antigravityTimeLayouts = []string{
	"January 2 2006",
	"January 02 2006",
	"January 2, 2006",
	"January 02, 2006",
	"2 January 2006",
	"02 January 2006",
	"2 January, 2006",
	"02 January, 2006",
	"2006-01-02",
}

// normalizeAntigravityTime maps the model's "None" to empty and a written date to
// ISO 8601, so a caller can compare dates without parsing English month names.
// A value no layout matches is kept as it arrived: an odd date is better than no
// date, and the caller can still show it.
func normalizeAntigravityTime(value string) string {
	trimmed := strings.TrimSpace(value)
	switch strings.ToLower(trimmed) {
	case "", "none", "null", "n/a", "unknown", "-":
		return ""
	}
	for _, layout := range antigravityTimeLayouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.Format("2006-01-02")
		}
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
