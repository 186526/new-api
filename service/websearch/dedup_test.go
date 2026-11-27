package websearch

import (
	"testing"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

// The model lists a page under two of its own URLs, and both hits carry the same
// sentence because the page answered both reads with the same text. One hit is
// what the client can use.
func TestDropRepeatedSiteResultsKeepsOneCopyOfASite(t *testing.T) {
	results := []abstract.Result{
		{URL: "https://www.linux.org/", Snippet: "You are using an out of date browser."},
		{URL: "https://www.linux.org/pages/download/", Snippet: "You are using an out of date browser."},
		{URL: "https://en.wikipedia.org/wiki/Linux", Snippet: "Linux is a family of open-source Unix-like operating systems."},
	}

	kept := dropRepeatedSiteResults(results)
	if len(kept) != 2 {
		t.Fatalf("kept %d results, want the repeated site collapsed to one", len(kept))
	}
	if kept[0].URL != "https://www.linux.org/" {
		t.Errorf("kept %q first, want the highest-ranked copy", kept[0].URL)
	}
}

// A site's separate pages describe different things, so both stay listed. Only a
// repeated description identifies a repeated entry.
func TestDropRepeatedSiteResultsKeepsDifferentPagesOfOneSite(t *testing.T) {
	results := []abstract.Result{
		{URL: "https://go.dev/", Snippet: "Build simple, secure, scalable systems with Go."},
		{URL: "https://go.dev/doc/", Snippet: "Documentation for the Go programming language."},
		{URL: "https://go.dev/blog/", Snippet: "The Go blog."},
	}

	kept := dropRepeatedSiteResults(results)
	if len(kept) != len(results) {
		t.Fatalf("kept %d of %d results, want every page of the site", len(kept), len(results))
	}
}

// A result the model wrote no sentence for identifies nothing, so it is kept
// rather than compared against the other sentence-less results.
func TestDropRepeatedSiteResultsKeepsResultsWithoutASnippet(t *testing.T) {
	results := []abstract.Result{
		{URL: "https://a.example/one"},
		{URL: "https://a.example/two"},
	}

	kept := dropRepeatedSiteResults(results)
	if len(kept) != 2 {
		t.Fatalf("kept %d results, want both results that carry no snippet", len(kept))
	}
}
