package request

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// localRequesterFor injects a plain client: the default one refuses a loopback
// address through the SSRF guard, and a test server only listens there.
func localRequesterFor(server *httptest.Server) localRequester {
	return localRequester{client: server.Client()}
}

const localPage = `<!DOCTYPE html>
<html><head><title>An article</title><script>stealEverything()</script></head>
<body><nav>menu</nav>
<article><h1>An article</h1><p>The body a caller asked for.</p>
<p>Second paragraph of the article.</p></article>
<footer>footer text</footer></body></html>`

func TestLocalFetchReturnsExtractedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(localPage))
	}))
	t.Cleanup(server.Close)

	upstream, err := localRequesterFor(server).Fetch(context.Background(), abstract.ChannelContext{}, Query{URL: server.URL})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if upstream.Family != websearch_setting.ProviderLocal {
		t.Fatalf("Family = %q, want %q", upstream.Family, websearch_setting.ProviderLocal)
	}
	if upstream.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", upstream.StatusCode)
	}
	body := string(upstream.Body)
	if !strings.Contains(body, "The body a caller asked for.") {
		t.Fatalf("body does not carry the article text: %s", body)
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "stealEverything") {
		t.Fatalf("body still carries the script element: %s", body)
	}
}

func TestLocalFetchRejectsANonHTTPURL(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	_, err := localRequesterFor(server).Fetch(context.Background(), abstract.ChannelContext{}, Query{URL: "file:///etc/passwd"})
	if err == nil {
		t.Fatal("expected an error for a non-http url")
	}
	if WasSent(err) {
		t.Fatalf("WasSent = true, want false for a url refused before any request: %v", err)
	}
}

func TestLocalSearchIsUnsupported(t *testing.T) {
	requester, err := For(websearch_setting.ProviderLocal)
	if err != nil {
		t.Fatalf("For(%s): %v", websearch_setting.ProviderLocal, err)
	}
	_, err = requester.Search(context.Background(), abstract.ChannelContext{}, Query{Text: "anything"})
	if err == nil {
		t.Fatal("expected the local requester to refuse a search")
	}
	if !strings.Contains(err.Error(), abstract.ErrUnsupportedChannel.Error()) {
		t.Fatalf("error = %v, want ErrUnsupportedChannel", err)
	}
	if WasSent(err) {
		t.Fatalf("WasSent = true, want false: %v", err)
	}
}
