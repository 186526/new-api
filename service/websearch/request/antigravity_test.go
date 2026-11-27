package request

// This file covers ResolveSearchRedirects and its short-link cache only.
// Search itself needs a real Antigravity OAuth credential: EnsureAntigravityCredential
// refreshes a refresh_token against the Google token endpoint and bootstraps a
// project, so exercising the full Search path would mean standing up a fake
// credential service for a path that holds no logic of its own beyond building
// the envelope. The stub therefore answers the generateContent document and the
// test reads it with the production parser, so the grounding shape under test is
// the one the composition layer really parses.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// redirectStub counts how often the short link was fetched and answers with the
// status the test wants.
type redirectStub struct {
	server *httptest.Server
	calls  atomic.Int32
}

func newRedirectStub(t *testing.T, status int, location string) *redirectStub {
	t.Helper()
	stub := &redirectStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.calls.Add(1)
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

// groundingSearch reads one generateContent answer through the parser the
// composition layer uses, so the test starts from the same abstract object the
// production path hands to ResolveSearchRedirects.
func groundingSearch(t *testing.T, citationURL string) *abstract.Search {
	t.Helper()
	answer := `{"response":{"candidates":[{"groundingMetadata":{"groundingChunks":[{"web":{"uri":"` +
		citationURL + `","title":"Example"}}],"groundingSupports":[{"segment":{"text":"a cited sentence."},"groundingChunkIndices":[0]}]}}]}}`

	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != antigravityGenerateContentPath {
			t.Errorf("generateContent stub saw path %q, want %q", r.URL.Path, antigravityGenerateContentPath)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(stub.Close)

	resp, err := http.Post(stub.URL+antigravityGenerateContentPath, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("call generateContent stub: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read generateContent stub: %v", err)
	}

	parser, err := abstract.ParserFor(websearch_setting.ProviderAntigravity)
	if err != nil {
		t.Fatalf("ParserFor(antigravity): %v", err)
	}
	// The stub answers in the envelope shape the upstream uses, so the test also
	// pins that the unwrapper hands the parser what it can read.
	unwrapped, err := unwrapAntigravityResponse(body)
	if err != nil {
		t.Fatalf("unwrap stub answer: %v", err)
	}
	results, err := parser.ParseSearch(unwrapped)
	if err != nil {
		t.Fatalf("parse stub grounding: %v", err)
	}
	return &abstract.Search{Results: results, StatusCode: resp.StatusCode}
}

// setRedirectCacheTTL writes the redirect lifetime and publishes it, because
// readers see the published copy rather than the mutable struct.
func setRedirectCacheTTL(t *testing.T, seconds int) {
	t.Helper()
	original := websearch_setting.RedirectCacheTTL()

	registered, ok := config.GlobalConfig.Get("websearch_setting").(*websearch_setting.WebSearchSetting)
	if !ok {
		t.Fatal("websearch_setting is not registered with the config manager")
	}
	if err := config.UpdateConfigFromMap(registered, map[string]string{
		"redirect_cache_ttl_seconds": strconv.Itoa(seconds),
	}); err != nil {
		t.Fatalf("write redirect_cache_ttl_seconds: %v", err)
	}
	if err := websearch_setting.UpdateAndSync(); err != nil {
		t.Fatalf("publish websearch_setting: %v", err)
	}
	t.Cleanup(func() {
		if err := config.UpdateConfigFromMap(registered, map[string]string{
			"redirect_cache_ttl_seconds": strconv.Itoa(original),
		}); err != nil {
			t.Errorf("restore redirect_cache_ttl_seconds: %v", err)
		}
		if err := websearch_setting.UpdateAndSync(); err != nil {
			t.Errorf("republish websearch_setting: %v", err)
		}
	})
}

// shortLinkCitation names a grounding redirect the way the upstream writes one:
// the path carries grounding-api-redirect, which is what isGroundingRedirect
// reads.
func shortLinkCitation(stub *redirectStub, slug string) string {
	return stub.server.URL + "/grounding-api-redirect/" + slug + "?sig=abc"
}

func TestAntigravityResolveSearchRedirectsReplacesTheShortLink(t *testing.T) {
	setRedirectCacheTTL(t, 600)
	shortLink := newRedirectStub(t, http.StatusFound, "https://a.example/page")
	citation := shortLinkCitation(shortLink, "replace")

	search := groundingSearch(t, citation)
	ResolveSearchRedirects(context.Background(), abstract.ChannelContext{}, search)

	if len(search.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(search.Results))
	}
	if got := search.Results[0].URL; got != "https://a.example/page" {
		t.Fatalf("Result.URL = %q, want %q", got, "https://a.example/page")
	}
}

func TestAntigravityResolveSearchRedirectsCachesAResolvedLink(t *testing.T) {
	setRedirectCacheTTL(t, 600)
	shortLink := newRedirectStub(t, http.StatusFound, "https://a.example/cached")
	citation := shortLinkCitation(shortLink, "cached")

	first := groundingSearch(t, citation)
	ResolveSearchRedirects(context.Background(), abstract.ChannelContext{}, first)
	if got := first.Results[0].URL; got != "https://a.example/cached" {
		t.Fatalf("first Result.URL = %q, want %q", got, "https://a.example/cached")
	}

	second := groundingSearch(t, citation)
	ResolveSearchRedirects(context.Background(), abstract.ChannelContext{}, second)
	if got := second.Results[0].URL; got != "https://a.example/cached" {
		t.Fatalf("second Result.URL = %q, want %q", got, "https://a.example/cached")
	}

	if got := shortLink.calls.Load(); got != 1 {
		t.Fatalf("short link fetched %d times, want 1: the second resolution must read the cache", got)
	}
}

func TestAntigravityResolveSearchRedirectsSkipsTheCacheWhenDisabled(t *testing.T) {
	setRedirectCacheTTL(t, -1)
	shortLink := newRedirectStub(t, http.StatusFound, "https://a.example/uncached")
	citation := shortLinkCitation(shortLink, "uncached")

	for round := 0; round < 2; round++ {
		search := groundingSearch(t, citation)
		ResolveSearchRedirects(context.Background(), abstract.ChannelContext{}, search)
		if got := search.Results[0].URL; got != "https://a.example/uncached" {
			t.Fatalf("round %d Result.URL = %q, want %q", round, got, "https://a.example/uncached")
		}
	}

	if got := shortLink.calls.Load(); got != 2 {
		t.Fatalf("short link fetched %d times, want 2 with the cache disabled", got)
	}
}

func TestAntigravityResolveSearchRedirectsKeepsTheShortLinkOnFailure(t *testing.T) {
	setRedirectCacheTTL(t, 600)
	shortLink := newRedirectStub(t, http.StatusNotFound, "")
	citation := shortLinkCitation(shortLink, "failed")

	for round := 0; round < 2; round++ {
		search := groundingSearch(t, citation)
		ResolveSearchRedirects(context.Background(), abstract.ChannelContext{}, search)
		if got := search.Results[0].URL; got != citation {
			t.Fatalf("round %d Result.URL = %q, want the unresolved short link %q", round, got, citation)
		}
	}

	// A failure must not be cached: the second round asks again, so a transient
	// refusal does not pin the short link for the whole TTL.
	if got := shortLink.calls.Load(); got != 2 {
		t.Fatalf("short link fetched %d times, want 2: a failed resolution must not be cached", got)
	}
}

func TestAntigravityRequesterPublishesNoNativeContract(t *testing.T) {
	requester, err := For(websearch_setting.ProviderAntigravity)
	if err != nil {
		t.Fatalf("For(%s): %v", websearch_setting.ProviderAntigravity, err)
	}
	if got := requester.Name(); got != websearch_setting.ProviderAntigravity {
		t.Fatalf("Name() = %q, want %q", got, websearch_setting.ProviderAntigravity)
	}
	if got := requester.Native(); got != nil {
		t.Fatalf("Native() = %v, want nil", got)
	}

	_, fetchErr := requester.Fetch(context.Background(), abstract.ChannelContext{}, Query{})
	if fetchErr == nil {
		t.Fatal("Fetch must refuse, Antigravity publishes no fetch endpoint")
	}
	if WasSent(fetchErr) {
		t.Fatalf("WasSent = true, want false for a refused fetch: %v", fetchErr)
	}
}

// TestAntigravitySearchEnvelopeVariesOnlyByQuery requires the envelope's random
// requestId to be the only part that changes between two identical searches.
//
// Forward caches by ForwardOptions.CacheKey, not by the request body, precisely
// because this envelope cannot be reused: requestId is a fresh UUID per build. If
// the envelope ever carried something else that varies, the cache key would have
// to grow to cover it, and this test is where that shows up.
func TestAntigravitySearchEnvelopeVariesOnlyByQuery(t *testing.T) {
	decode := func(t *testing.T, payload []byte, query string) map[string]any {
		t.Helper()
		var envelope map[string]any
		if err := common.Unmarshal(payload, &envelope); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		return envelope
	}

	first, err := antigravityEnvelope("gemini-3.5-flash-lite", "proj", "bird routing")
	if err != nil {
		t.Fatalf("antigravityEnvelope: %v", err)
	}
	second, err := antigravityEnvelope("gemini-3.5-flash-lite", "proj", "bird routing")
	if err != nil {
		t.Fatalf("antigravityEnvelope: %v", err)
	}

	firstEnvelope := decode(t, first, "bird routing")
	secondEnvelope := decode(t, second, "bird routing")
	firstID, _ := firstEnvelope["requestId"].(string)
	secondID, _ := secondEnvelope["requestId"].(string)
	if firstID == "" || secondID == "" {
		t.Fatalf("requestId must be set: %q and %q", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("requestId did not vary between two envelopes: %q", firstID)
	}

	// Everything except requestId must match, otherwise the cache key is
	// incomplete and two identical searches could be answered from one entry.
	delete(firstEnvelope, "requestId")
	delete(secondEnvelope, "requestId")
	firstRest, err := common.Marshal(firstEnvelope)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	secondRest, err := common.Marshal(secondEnvelope)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if string(firstRest) != string(secondRest) {
		t.Fatalf("envelopes differ beyond requestId:\n%s\n%s", firstRest, secondRest)
	}

	// A different query must produce a different body, which is what makes the
	// query part of the cache key meaningful.
	other, err := antigravityEnvelope("gemini-3.5-flash-lite", "proj", "bgp filtering")
	if err != nil {
		t.Fatalf("antigravityEnvelope: %v", err)
	}
	if string(other) == string(first) {
		t.Fatal("two different queries produced the same envelope")
	}
}
