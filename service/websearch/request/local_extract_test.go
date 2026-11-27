package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

// fetchLocalPage runs the in-process extractor over one page body and returns
// its markdown, which is the same path a live fetch takes.
func fetchLocalPage(t *testing.T, page string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(server.Close)

	upstream, err := localRequesterFor(server).Fetch(context.Background(), abstract.ChannelContext{}, Query{URL: server.URL})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return string(upstream.Body)
}

// A page that marks several articles keeps the one that holds the body, not the
// first one in the document. www.debian.org is the live example: its six
// <article> elements are one-line news headings.
func TestLocalFetchTakesTheArticleHoldingTheBody(t *testing.T) {
	long := strings.Repeat("This is the body of the page. ", 40)
	page := `<html><body>
	<article><h2>DC26 Group Photo</h2></article>
	<article><h2>New Debian release</h2><p>` + long + `</p></article>
	</body></html>`

	got := fetchLocalPage(t, page)
	if !strings.Contains(got, "This is the body of the page.") {
		t.Fatalf("the article holding the body was not chosen: %q", got)
	}
	if !strings.Contains(got, "New Debian release") {
		t.Errorf("the chosen article lost its heading: %q", got)
	}
}

// Every marked container coming up short means the page marked its chrome rather
// than its body, so the whole page is rendered instead of one heading.
func TestLocalFetchFallsBackToTheWholePageWhenEveryContainerIsShort(t *testing.T) {
	body := strings.Repeat("The actual article text lives outside any marked container. ", 20)
	// The body sits in a <section>, which is not one of the containers a page
	// marks its article with, so every candidate stays under the threshold.
	page := `<html><body>
	<article><h2>Short headline</h2></article>
	<main><p>Another teaser.</p></main>
	<section><p>` + body + `</p></section>
	</body></html>`

	got := fetchLocalPage(t, page)
	if !strings.Contains(got, "The actual article text lives outside any marked container.") {
		t.Fatalf("the body was dropped when every container was short: %q", got)
	}
	if !strings.Contains(got, "Short headline") {
		t.Errorf("the fallback dropped the page's own text: %q", got)
	}
}

// www.kernel.org keeps its release list in a table. Flattening the cells into one
// paragraph loses both the columns and the row boundaries.
func TestLocalFetchRendersATableAsRows(t *testing.T) {
	page := `<html><body><article>
	<h1>Releases</h1>
	<table>
	  <thead><tr><th>Version</th><th>Date</th></tr></thead>
	  <tbody>
	    <tr><td>7.3.6</td><td>2026-11-23</td></tr>
	    <tr><td>6.18.65</td><td>2026-11-23</td></tr>
	  </tbody>
	</table>
	</article></body></html>`

	got := fetchLocalPage(t, page)
	for _, want := range []string{
		"| Version | Date |",
		"| --- | --- |",
		"| 7.3.6 | 2026-11-23 |",
		"| 6.18.65 | 2026-11-23 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered table is missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "\n") < 4 {
		t.Errorf("the rows were flattened onto one line:\n%s", got)
	}
}
