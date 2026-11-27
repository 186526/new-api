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

// A page ships text for readers who cannot see the rendered result: a browser
// upgrade notice, a login dialog, a cookie bar. They sit behind display:none
// until a script shows them, so reading the markup as written puts a warning
// about the visitor's own browser into an answer about the page.
func TestLocalFetchSkipsHiddenSubtrees(t *testing.T) {
	long := strings.Repeat("This is the body of the page. ", 40)
	page := `<html><body><article><h1>A page</h1><p>` + long + `</p>
	<div style="display: none">You are using an out of date browser.</div>
	<div style="display:none">Compressed hidden notice.</div>
	<div hidden>Hidden by attribute.</div>
	<div aria-hidden="true">Hidden from assistive tools.</div>
	<div style="border: 1px solid red; display:  NONE ">Spaced and cased hidden notice.</div>
	<p>Visible closing paragraph.</p>
	</article></body></html>`

	got := fetchLocalPage(t, page)
	for _, unwanted := range []string{
		"out of date browser",
		"Compressed hidden notice",
		"Hidden by attribute",
		"Hidden from assistive tools",
		"Spaced and cased hidden notice",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("a hidden subtree reached the extracted text: %q\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "Visible closing paragraph.") {
		t.Errorf("visible text was dropped along with the hidden subtrees:\n%s", got)
	}
}

// A target that answered with its own status keeps it, so a caller tells a
// mistyped URL from a host that could not be reached.
func TestLocalFetchReportsTheStatusTheTargetAnswered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	_, err := localRequesterFor(server).Fetch(context.Background(), abstract.ChannelContext{}, Query{URL: server.URL})
	if err == nil {
		t.Fatal("a 404 answered the fetch instead of failing it")
	}
	status, answered := FetchAnswerStatus(err)
	if !answered {
		t.Fatalf("the status was not carried by the error: %v", err)
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want the target's own %d", status, http.StatusNotFound)
	}
}

// A host that cannot be reached answers no status of its own, so the failure
// stays a gateway error rather than borrowing one.
func TestLocalFetchReportsNoStatusForAnUnreachableHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	server.Close()

	_, err := localRequesterFor(server).Fetch(context.Background(), abstract.ChannelContext{}, Query{URL: server.URL})
	if err == nil {
		t.Fatal("an unreachable host answered the fetch")
	}
	if status, answered := FetchAnswerStatus(err); answered {
		t.Errorf("status = %d was reported for a host that never answered", status)
	}
}

// A listing page puts each entry's title and date in that entry's own header. A
// header used to be dropped wherever it appeared, so blog.186526.xyz's index
// page came out empty and a fetch answered 200 with no text at all.
func TestLocalFetchKeepsAnArticlesOwnHeader(t *testing.T) {
	page := `<html><body>
<header class="site-banner"><a href="/">Site name</a></header>
<main><article>
<header class="article-header"><h2>First post title</h2><time>10月 27, 2026</time></header>
</article><article>
<header class="article-header"><h2>Second post title</h2><time>1月 10, 2026</time></header>
</article></main>
<footer class="site-footer">Copyright notice</footer>
</body></html>`

	got := fetchLocalPage(t, page)
	for _, wanted := range []string{"First post title", "Second post title", "10月 27, 2026"} {
		if !strings.Contains(got, wanted) {
			t.Errorf("an article's own header was dropped with the site banner: %q missing\n%s", wanted, got)
		}
	}
	if strings.Contains(got, "Site name") || strings.Contains(got, "Copyright notice") {
		t.Errorf("the page banner or footer reached the text:\n%s", got)
	}
}
