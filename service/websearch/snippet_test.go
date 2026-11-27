package websearch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

// The three page shapes these tests use are the ones the live extractor
// produced for one nginx search. They are what the lead-paragraph rule has to
// tell apart: real prose from navigation left over after extraction.

const nginxOrgPage = `## nginx

 nginx ("engine x") is an HTTP web server, reverse proxy, content cache, load balancer, TCP/UDP proxy server, and mail proxy server. Originally written by [Igor Sysoev](http://sysoev.ru/en/) and distributed under the [2-clause BSD License](https://nginx.org/LICENSE).

 [Docs](https://nginx.org/en/docs/)
`

const dockerHubPage = `![nginx logo]

## nginx

Docker Official Image•

[Web servers](https://hub.docker.com/categories/web-servers)

- Maintained by: [the NGINX Docker Maintainers](https://github.com/nginx/docker-nginx)
- Where to get help: the Docker Community Slack, Server Fault, Unix & Linux, or Stack Overflow
`

const githubChromePage = `Skip to content

You signed in with another tab or window. Reload to refresh your session. You signed out in another tab or window.

[Sign in](https://github.com/login)
`

func TestLocalLeadSnippetReadsThePagesFirstParagraph(t *testing.T) {
	got := localLeadSnippet(nginxOrgPage)
	want := `nginx ("engine x") is an HTTP web server, reverse proxy, content cache, load balancer, TCP/UDP proxy server, and mail proxy server.`
	if got != want {
		t.Fatalf("snippet = %q, want %q", got, want)
	}
}

func TestLocalLeadSnippetRejectsNavigationLeftovers(t *testing.T) {
	for name, page := range map[string]string{
		"a help list":   dockerHubPage,
		"github chrome": githubChromePage,
	} {
		if got := localLeadSnippet(page); got != "" {
			t.Errorf("%s: snippet = %q, want empty so the caller keeps the model's sentence", name, got)
		}
	}
}

func TestLocalLeadSnippetJoinsASentenceSplitAcrossLines(t *testing.T) {
	// The extractor writes one source paragraph across several lines, and the
	// snippet must read as one sentence rather than as the first line only.
	page := "Some heading\n\n An HTTP web server is\n a program that serves\n files over the network. It can also proxy.\n"
	got := localLeadSnippet(page)
	if strings.Contains(got, "\n") {
		t.Fatalf("snippet = %q, want one line", got)
	}
	if !strings.HasPrefix(got, "An HTTP web server is a program") {
		t.Fatalf("snippet = %q, want the whole paragraph joined", got)
	}
}

func TestFillSnippetsKeepsTheSummaryWhenThePageCannotBeRead(t *testing.T) {
	// An unresolvable host fails the read, which is the case the model's own
	// sentence exists for.
	results := []abstract.Result{
		{URL: "https://this-domain-does-not-exist-xyz123.invalid/", Summary: "A page that cannot be reached"},
	}
	fillSnippets(context.Background(), abstract.ChannelContext{}, results)

	if results[0].Snippet != "A page that cannot be reached" {
		t.Fatalf("snippet = %q, want the model's sentence", results[0].Snippet)
	}
}

func TestFillSnippetsLeavesAResultWithNoSentenceAndNoPageEmpty(t *testing.T) {
	// Nothing was written about this hit and its page cannot be read, so the
	// snippet stays empty rather than inventing one.
	results := []abstract.Result{
		{URL: "https://this-domain-does-not-exist-xyz123.invalid/"},
	}
	fillSnippets(context.Background(), abstract.ChannelContext{}, results)

	if results[0].Snippet != "" {
		t.Fatalf("snippet = %q, want empty", results[0].Snippet)
	}
}

func TestFillSnippetsKeepsAnExistingSnippet(t *testing.T) {
	// A provider that published its own snippet keeps it: the page read is only
	// there to fill a hole.
	results := []abstract.Result{
		{URL: "https://this-domain-does-not-exist-xyz123.invalid/", Snippet: "from the upstream", Summary: "from the model"},
	}
	fillSnippets(context.Background(), abstract.ChannelContext{}, results)

	if results[0].Snippet != "from the upstream" {
		t.Fatalf("snippet = %q, want the upstream's own", results[0].Snippet)
	}
}

func TestFillSnippetsReturnsInsideTheBudget(t *testing.T) {
	// A page that never answers must not hold the search answer: the budget is
	// the promise the caller relies on.
	results := []abstract.Result{
		{URL: "https://10.255.255.1/", Summary: "a page that will not answer"},
	}

	start := time.Now()
	fillSnippets(context.Background(), abstract.ChannelContext{}, results)
	elapsed := time.Since(start)

	if elapsed > snippetFetchBudget+2*time.Second {
		t.Fatalf("fillSnippets took %v, want no more than the %v budget plus slack", elapsed, snippetFetchBudget)
	}
	if results[0].Snippet != "a page that will not answer" {
		t.Fatalf("snippet = %q, want the model's sentence after the budget", results[0].Snippet)
	}
}

func TestFillSnippetsReturnsWhenTheCallerGivesUp(t *testing.T) {
	// A client that disconnects must not leave the batch running.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results := []abstract.Result{
		{URL: "https://10.255.255.1/", Summary: "a page that will not answer"},
	}

	start := time.Now()
	fillSnippets(ctx, abstract.ChannelContext{}, results)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("fillSnippets took %v after the caller cancelled, want it to return at once", elapsed)
	}
}
