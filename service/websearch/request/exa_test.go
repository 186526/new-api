package request

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// exaStub records the body, path and headers the requester sent, and lets each
// test answer as it wants.
type exaStub struct {
	server *httptest.Server
	body   atomic.Value // []byte
	path   atomic.Value // string
	header atomic.Value // http.Header
	calls  atomic.Int32
}

func newExaStub(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, call int32)) *exaStub {
	t.Helper()
	stub := &exaStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := stub.calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read stub body: %v", err)
		}
		stub.body.Store(body)
		stub.path.Store(r.URL.Path)
		stub.header.Store(r.Header.Clone())
		handler(w, r, call)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *exaStub) lastBody() []byte {
	raw, _ := s.body.Load().([]byte)
	return raw
}

func (s *exaStub) lastHeader(name string) string {
	header, _ := s.header.Load().(http.Header)
	if header == nil {
		return ""
	}
	return header.Get(name)
}

func decodeStubBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	return payload
}

const exaSearchAnswer = `{"results":[{"title":"Go","url":"https://go.dev","text":"body","highlights":["snippet"]}]}`

func exaRequesterFor(t *testing.T) Requester {
	t.Helper()
	requester, err := For(websearch_setting.ProviderExa)
	if err != nil {
		t.Fatalf("For(%s): %v", websearch_setting.ProviderExa, err)
	}
	return requester
}

func exaTestChannel() abstract.ChannelContext {
	return abstract.ChannelContext{Key: "test-key"}
}

func TestExaSearchOmitsNumResultsWhenCountIsZero(t *testing.T) {
	stub := newExaStub(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		_, _ = w.Write([]byte(exaSearchAnswer))
	})

	query := Query{Contract: abstract.ContractToolsSearch, Text: "zero results count", Endpoint: stub.server.URL}
	if _, err := exaRequesterFor(t).Search(context.Background(), exaTestChannel(), query); err != nil {
		t.Fatalf("search: %v", err)
	}
	if _, present := decodeStubBody(t, stub.lastBody())["numResults"]; present {
		t.Fatalf("numResults must be omitted when Count is zero, body: %s", stub.lastBody())
	}
}

func TestExaSearchSendsNumResultsWhenCountIsSet(t *testing.T) {
	stub := newExaStub(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		_, _ = w.Write([]byte(exaSearchAnswer))
	})

	query := Query{
		Contract: abstract.ContractToolsSearch,
		Text:     "a count-bearing query",
		Count:    5,
		Endpoint: stub.server.URL,
	}
	if _, err := exaRequesterFor(t).Search(context.Background(), exaTestChannel(), query); err != nil {
		t.Fatalf("search: %v", err)
	}
	payload := decodeStubBody(t, stub.lastBody())
	if got, _ := payload["numResults"].(float64); got != 5 {
		t.Fatalf("numResults = %v, want 5", payload["numResults"])
	}
}

func TestExaSearchOmitsContentsTextWithoutNeedContent(t *testing.T) {
	stub := newExaStub(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		_, _ = w.Write([]byte(exaSearchAnswer))
	})
	requester := exaRequesterFor(t)

	query := Query{Contract: abstract.ContractToolsSearch, Text: "a query that needs no content", Endpoint: stub.server.URL}
	if _, err := requester.Search(context.Background(), exaTestChannel(), query); err != nil {
		t.Fatalf("search: %v", err)
	}
	contents, _ := decodeStubBody(t, stub.lastBody())["contents"].(map[string]any)
	if _, present := contents["text"]; present {
		t.Fatalf("contents.text must be omitted without NeedContent, body: %s", stub.lastBody())
	}

	needing := query
	needing.Text = "a query that needs content"
	needing.NeedContent = true
	if _, err := requester.Search(context.Background(), exaTestChannel(), needing); err != nil {
		t.Fatalf("search with content: %v", err)
	}
	contents, _ = decodeStubBody(t, stub.lastBody())["contents"].(map[string]any)
	if _, present := contents["text"]; !present {
		t.Fatalf("contents.text must be present with NeedContent, body: %s", stub.lastBody())
	}
}

func TestExaSearchSendsAPIKeyHeader(t *testing.T) {
	stub := newExaStub(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		_, _ = w.Write([]byte(exaSearchAnswer))
	})

	query := Query{Contract: abstract.ContractToolsSearch, Text: "a query with an api key", Endpoint: stub.server.URL}
	if _, err := exaRequesterFor(t).Search(context.Background(), exaTestChannel(), query); err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := stub.lastHeader("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want %q", got, "test-key")
	}
	if got := stub.lastHeader("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestExaSearchRetriesOnceAfter429(t *testing.T) {
	stub := newExaStub(t, func(w http.ResponseWriter, _ *http.Request, call int32) {
		if call == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		_, _ = w.Write([]byte(exaSearchAnswer))
	})

	query := Query{Contract: abstract.ContractToolsSearch, Text: "a query that is rate limited once", Endpoint: stub.server.URL}
	upstream, err := exaRequesterFor(t).Search(context.Background(), exaTestChannel(), query)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got := stub.calls.Load(); got != 2 {
		t.Fatalf("upstream called %d times, want 2 (429 then 200)", got)
	}
	if upstream.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200 after the retry succeeded", upstream.StatusCode)
	}
	if !strings.Contains(string(upstream.Body), "go.dev") {
		t.Fatalf("body does not carry the successful answer: %s", upstream.Body)
	}
}

func TestExaWithoutAnyKeyIsNotSent(t *testing.T) {
	query := Query{Contract: abstract.ContractToolsSearch, Text: "a query without a key"}
	_, err := exaRequesterFor(t).Search(context.Background(), abstract.ChannelContext{}, query)
	if err == nil {
		t.Fatal("expected an error without a key")
	}
	if WasSent(err) {
		t.Fatalf("WasSent = true, want false for a missing key: %v", err)
	}
	if !strings.Contains(err.Error(), "has no api key") {
		t.Fatalf("error does not name the missing key: %v", err)
	}
}

func TestExaFetchSendsURLToContents(t *testing.T) {
	stub := newExaStub(t, func(w http.ResponseWriter, _ *http.Request, _ int32) {
		_, _ = w.Write([]byte(`{"results":[{"title":"Go","text":"fetched body"}]}`))
	})

	query := Query{Contract: abstract.ContractToolsFetch, URL: "https://go.dev/doc", Endpoint: stub.server.URL}
	upstream, err := exaRequesterFor(t).Fetch(context.Background(), exaTestChannel(), query)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got, _ := stub.path.Load().(string); got != "/contents" {
		t.Fatalf("fetch path = %q, want /contents", got)
	}
	if upstream.Family != websearch_setting.ProviderExa {
		t.Fatalf("Family = %q, want %q", upstream.Family, websearch_setting.ProviderExa)
	}
	if !strings.Contains(string(upstream.Body), "fetched body") {
		t.Fatalf("body does not carry the upstream answer: %s", upstream.Body)
	}
	urls, _ := decodeStubBody(t, stub.lastBody())["urls"].([]any)
	if len(urls) != 1 || urls[0] != "https://go.dev/doc" {
		t.Fatalf("urls = %v, want [https://go.dev/doc]", urls)
	}
}
