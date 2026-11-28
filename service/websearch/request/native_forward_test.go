package request

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

// recordedRequest is what a stub upstream saw, so a test can assert on the
// bytes and the headers rather than on the handler's own bookkeeping.
type recordedRequest struct {
	Method  string
	Path    string
	Body    []byte
	Headers http.Header
}

func newStubUpstream(t *testing.T, status int, answer string) (*httptest.Server, *recordedRequest) {
	t.Helper()
	seen := &recordedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read stub request body: %v", err)
		}
		*seen = recordedRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Body:    body,
			Headers: r.Header.Clone(),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(server.Close)
	return server, seen
}

func testChannel(baseURL string) abstract.ChannelContext {
	return abstract.ChannelContext{
		ChannelID:   1,
		ChannelType: 43,
		BaseURL:     baseURL,
		Key:         "channel-key",
		Settings:    dto.ChannelSettings{},
	}
}

// TestDeepSeekNativeSearchForwardsBodyVerbatim pins the path an existing channel
// takes: the client's own body reaches the upstream unchanged and the upstream's
// own answer comes back unchanged, which is what the client read before this
// package existed.
func TestDeepSeekNativeSearchForwardsBodyVerbatim(t *testing.T) {
	answer := "{\"results\":[{\"title\":\"A\",\"url\":\"https://a.example\"}]}"
	server, seen := newStubUpstream(t, http.StatusOK, answer)

	// Odd spacing and an unknown member prove the body is forwarded rather than
	// re-serialized, which would normalize both.
	raw := []byte("{ \"query\":\"birds\",  \"unknown\":[1,2], \"need_content\": false }")

	requester, err := For("deepseek")
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	upstream, err := requester.Search(context.Background(), testChannel(""), Query{
		Contract: abstract.ContractToolsSearch,
		Text:     "birds",
		Raw:      raw,
		Endpoint: server.URL + "/v1/tools/search",
		Prepare: func(req *http.Request) error {
			req.Header.Set("Authorization", "Bearer adapter-key")
			req.Header.Set("X-Adapter", "deepseek")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if string(seen.Body) != string(raw) {
		t.Errorf("upstream body = %q, want the client body verbatim %q", seen.Body, raw)
	}
	if seen.Path != "/v1/tools/search" {
		t.Errorf("upstream path = %q", seen.Path)
	}
	if got := seen.Headers.Get("Authorization"); got != "Bearer adapter-key" {
		t.Errorf("Authorization = %q, want the value Prepare set", got)
	}
	if got := seen.Headers.Get("X-Adapter"); got != "deepseek" {
		t.Errorf("X-Adapter = %q, want the value Prepare set", got)
	}
	if string(upstream.Body) != answer {
		t.Errorf("answer = %q, want the upstream bytes verbatim %q", upstream.Body, answer)
	}
	if upstream.Family != "deepseek" {
		t.Errorf("Family = %q, want deepseek", upstream.Family)
	}
	if upstream.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d", upstream.StatusCode)
	}
}

// TestDeepSeekNonNativeSearchRebuildsBody covers a query that reached DeepSeek
// by a global credential or an operator binding: there is no channel upstream to
// forward to, so the body is built from the fields alone.
func TestDeepSeekNonNativeSearchRebuildsBody(t *testing.T) {
	server, seen := newStubUpstream(t, http.StatusOK, "{\"results\":[]}")

	requester, _ := For("deepseek")
	_, err := requester.Search(context.Background(), testChannel(server.URL), Query{
		Contract:    abstract.ContractToolsSearch,
		Text:        "birds",
		Count:       3,
		NeedContent: true,
		// No Endpoint and no Raw: this is the rebuilt path.
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var body map[string]any
	if err := common.Unmarshal(seen.Body, &body); err != nil {
		t.Fatalf("decode rebuilt body %q: %v", seen.Body, err)
	}
	if body["query"] != "birds" {
		t.Errorf("query = %v", body["query"])
	}
	if body["need_content"] != true {
		t.Errorf("need_content = %v", body["need_content"])
	}
	if body["count"] != float64(3) {
		t.Errorf("count = %v", body["count"])
	}
	if got := seen.Headers.Get("Authorization"); got != "Bearer channel-key" {
		t.Errorf("Authorization = %q, want the channel key", got)
	}
	if seen.Path != deepSeekSearchPath {
		t.Errorf("path = %q, want %q", seen.Path, deepSeekSearchPath)
	}
}

// TestCodexNativeSearchForwardsBodyVerbatim covers the Codex alpha search: the
// body is forwarded and the path is the one the adaptor routes to.
func TestCodexNativeSearchForwardsBodyVerbatim(t *testing.T) {
	answer := "{\"id\":\"s1\",\"results\":[{\"type\":\"text_result\",\"title\":\"A\",\"url\":\"https://a.example\"}]}"
	server, seen := newStubUpstream(t, http.StatusOK, answer)

	raw := []byte(`{ "model":"gpt-5",  "commands":{"search_query":[{"q":"birds"}]}, "future_field":true }`)

	requester, err := For("codex")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	upstream, err := requester.Search(context.Background(), testChannel(""), Query{
		Contract: abstract.ContractAlphaSearch,
		Text:     "ignore this query text",
		Model:    "ignore this model",
		Raw:      raw,
		Endpoint: server.URL + codexAlphaSearchPath,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if string(seen.Body) != string(raw) {
		t.Errorf("upstream body = %q, want %q", seen.Body, raw)
	}
	if seen.Path != "/backend-api/codex/alpha/search" {
		t.Errorf("path = %q, want /backend-api/codex/alpha/search", seen.Path)
	}
	if string(upstream.Body) != answer {
		t.Errorf("answer = %q, want %q", upstream.Body, answer)
	}
	if upstream.Family != "codex" {
		t.Errorf("Family = %q, want codex", upstream.Family)
	}
}

// TestCodexWithoutEndpointIsNotSent proves Codex refuses a request it cannot
// sign itself: the failure must read as never sent so the caller may fall
// through to another candidate.
func TestCodexWithoutEndpointIsNotSent(t *testing.T) {
	requester, _ := For("codex")

	_, err := requester.Search(context.Background(), testChannel("https://chatgpt.com"), Query{
		Contract: abstract.ContractAlphaSearch,
		Text:     "birds",
	})
	if err == nil {
		t.Fatal("expected an error without a channel endpoint")
	}
	if WasSent(err) {
		t.Errorf("WasSent = true, want false: %v", err)
	}
}

// TestCodexFetchIsNotSent covers the contract Codex does not publish.
func TestCodexFetchIsNotSent(t *testing.T) {
	requester, _ := For("codex")

	_, err := requester.Fetch(context.Background(), testChannel("https://chatgpt.com"), Query{
		Contract: abstract.ContractToolsFetch,
		URL:      "https://a.example",
	})
	if err == nil {
		t.Fatal("expected an error for a codex fetch")
	}
	if WasSent(err) {
		t.Errorf("WasSent = true, want false: %v", err)
	}
}

func TestDeepSeekSearchReturnsTransientRefusalWithoutRetry(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = io.WriteString(w, "timeout")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"results\":[]}")
	}))
	t.Cleanup(server.Close)

	requester, _ := For("deepseek")
	upstream, err := requester.Search(context.Background(), testChannel(""), Query{
		Contract: abstract.ContractToolsSearch,
		Text:     "retry-me",
		Raw:      []byte("{\"query\":\"retry-me\"}"),
		Endpoint: server.URL + deepSeekSearchPath,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("upstream attempts = %d, want 1", got)
	}
	if upstream.StatusCode != http.StatusRequestTimeout || string(upstream.Body) != "timeout" {
		t.Errorf("upstream response = %d %q, want 408 timeout", upstream.StatusCode, upstream.Body)
	}
}

// TestDeepSeekSearchCachesAnswer checks the second identical search reads the
// cache: the stub counts one upstream attempt.
func TestDeepSeekSearchCachesAnswer(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{\"results\":[{\"title\":\"A\",\"url\":\"https://a.example\"}]}")
	}))
	t.Cleanup(server.Close)

	requester, _ := For("deepseek")
	query := Query{
		Contract: abstract.ContractToolsSearch,
		Text:     "cached-query",
		Raw:      []byte("{\"query\":\"cached-query\"}"),
		Endpoint: server.URL + deepSeekSearchPath,
	}
	channel := testChannel("")

	first, err := requester.Search(context.Background(), channel, query)
	if err != nil {
		t.Fatalf("first Search: %v", err)
	}
	second, err := requester.Search(context.Background(), channel, query)
	if err != nil {
		t.Fatalf("second Search: %v", err)
	}

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("upstream attempts = %d, want 1 after the cache hit", got)
	}
	if first.Cached {
		t.Error("first answer was reported cached")
	}
	if !second.Cached {
		t.Error("second answer was not reported cached")
	}
	if string(first.Body) != string(second.Body) {
		t.Errorf("cached body differs: %q vs %q", first.Body, second.Body)
	}
}

// TestDeepSeekNativeCacheKeyCoversTheBody proves two different native bodies to
// the same URL do not share a cache entry: they are different searches.
func TestDeepSeekNativeCacheKeyCoversTheBody(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	requester, _ := For("deepseek")
	channel := testChannel("")
	endpoint := server.URL + deepSeekSearchPath

	for _, raw := range []string{"{\"query\":\"one\"}", "{\"query\":\"two\"}"} {
		if _, err := requester.Search(context.Background(), channel, Query{
			Contract: abstract.ContractToolsSearch,
			Text:     "same text",
			Raw:      []byte(raw),
			Endpoint: endpoint,
		}); err != nil {
			t.Fatalf("Search %q: %v", raw, err)
		}
	}

	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("upstream attempts = %d, want 2: different bodies must not share a cache entry", got)
	}
}
