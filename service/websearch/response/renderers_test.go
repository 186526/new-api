package response

import (
	"testing"

	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

const testContentTypeJSON = "application/json"

// fullyKnown and fullyUnknown are the two extremes a provider can publish: every
// optional member present, and none of them. A renderer must keep a present
// empty value apart from a missing one, which these two cover.
func fullyKnownResult() abstract.Result {
	return abstract.Result{
		Title:       "Example title",
		URL:         "https://example.com/page",
		Snippet:     "A short snippet.",
		Summary:     "A longer summary.",
		Content:     "The whole page.",
		SiteName:    "Example",
		SiteIcon:    "https://example.com/icon.png",
		Date:        "2026-11-27",
		Authority:   42,
		RerankScore: 0.75,
		Known: abstract.KnownFields{
			Summary:     true,
			Content:     true,
			SiteName:    true,
			SiteIcon:    true,
			Date:        true,
			Authority:   true,
			RerankScore: true,
		},
	}
}

func fullyUnknownResult() abstract.Result {
	return abstract.Result{
		Title:     "Example title",
		URL:       "https://example.com/page",
		Snippet:   "A short snippet.",
		Authority: -1,
	}
}

func searchOf(results ...abstract.Result) abstract.Search {
	return abstract.Search{Results: results}
}

func TestRenderersNameTheirContractAndContentType(t *testing.T) {
	cases := []struct {
		contract    abstract.Contract
		contentType string
	}{
		{abstract.ContractToolsSearch, testContentTypeJSON},
		{abstract.ContractToolsFetch, testContentTypeJSON},
		{abstract.ContractAlphaSearch, testContentTypeJSON},
		{abstract.ContractMoonshotSearch, testContentTypeJSON},
		{abstract.ContractMoonshotFetch, "text/markdown; charset=utf-8"},
	}

	for _, testCase := range cases {
		renderer := For(testCase.contract)
		if renderer == nil {
			t.Fatalf("no renderer registered for %s", testCase.contract)
		}
		if got := renderer.Contract(); got != testCase.contract {
			t.Errorf("%s: Contract() = %q, want %q", testCase.contract, got, testCase.contract)
		}
		if got := renderer.ContentType(); got != testCase.contentType {
			t.Errorf("%s: ContentType() = %q, want %q", testCase.contract, got, testCase.contentType)
		}
	}
}

func TestForUnknownContractReturnsNil(t *testing.T) {
	if renderer := For(abstract.Contract("unknown/contract")); renderer != nil {
		t.Fatalf("For(unknown) = %#v, want nil", renderer)
	}
}

func TestRenderSearch(t *testing.T) {
	cases := []struct {
		name     string
		contract abstract.Contract
		results  []abstract.Result
		want     string
	}{
		{
			name:     "tools/search with every member known",
			contract: abstract.ContractToolsSearch,
			results:  []abstract.Result{fullyKnownResult()},
			want: `{"results":[{"rerank_score":0.75,"title":"Example title","url":"https://example.com/page",` +
				`"snippet":"A short snippet.","summary":"A longer summary.","content":"The whole page.",` +
				`"site_name":"Example","site_icon":"https://example.com/icon.png","date":"2026-11-27","authority":42}]}`,
		},
		{
			name:     "tools/search with no optional member known",
			contract: abstract.ContractToolsSearch,
			results:  []abstract.Result{fullyUnknownResult()},
			want: `{"results":[{"rerank_score":null,"title":"Example title","url":"https://example.com/page",` +
				`"snippet":"A short snippet.","summary":null,"content":null,"site_name":null,"site_icon":null,` +
				`"date":null,"authority":-1}]}`,
		},
		{
			name:     "tools/search without a hit keeps an empty list",
			contract: abstract.ContractToolsSearch,
			results:  nil,
			want:     `{"results":[]}`,
		},
		{
			name:     "alpha/search with every member known",
			contract: abstract.ContractAlphaSearch,
			results:  []abstract.Result{fullyKnownResult()},
			want: `{"id":"","results":[{"type":"text_result","title":"Example title",` +
				`"url":"https://example.com/page","snippet":"A short snippet."}]}`,
		},
		{
			name:     "alpha/search with no optional member known",
			contract: abstract.ContractAlphaSearch,
			results:  []abstract.Result{fullyUnknownResult()},
			want: `{"id":"","results":[{"type":"text_result","title":"Example title",` +
				`"url":"https://example.com/page","snippet":"A short snippet."}]}`,
		},
		{
			name:     "alpha/search without a hit keeps an empty list",
			contract: abstract.ContractAlphaSearch,
			results:  nil,
			want:     `{"id":"","results":[]}`,
		},
		{
			name:     "moonshot/search with every member known",
			contract: abstract.ContractMoonshotSearch,
			results:  []abstract.Result{fullyKnownResult()},
			want: `{"search_results":[{"title":"Example title","url":"https://example.com/page",` +
				`"snippet":"A short snippet.","date":"2026-11-27","site_name":"Example"}]}`,
		},
		{
			name:     "moonshot/search with no optional member known",
			contract: abstract.ContractMoonshotSearch,
			results:  []abstract.Result{fullyUnknownResult()},
			// The date is written as null rather than left out, so a caller can
			// tell "this page names no date" from "this build does not know dates".
			want: `{"search_results":[{"title":"Example title","url":"https://example.com/page",` +
				`"snippet":"A short snippet.","date":null}]}`,
		},
		{
			name:     "moonshot/search without a hit is an empty list not null",
			contract: abstract.ContractMoonshotSearch,
			results:  nil,
			want:     `{"search_results":[]}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := For(testCase.contract).RenderSearch(searchOf(testCase.results...))
			if err != nil {
				t.Fatalf("RenderSearch returned %v", err)
			}
			if string(body) != testCase.want {
				t.Fatalf("RenderSearch =\n%s\nwant\n%s", body, testCase.want)
			}
		})
	}
}

func TestRenderFetch(t *testing.T) {
	page := abstract.Page{Title: "Example title", Content: "# Heading"}
	cases := []struct {
		name     string
		contract abstract.Contract
		page     abstract.Page
		want     string
	}{
		{
			name:     "tools/fetch writes both members",
			contract: abstract.ContractToolsFetch,
			page:     page,
			want:     `{"title":"Example title","content":"# Heading"}`,
		},
		{
			name:     "tools/fetch writes an empty page as empty strings",
			contract: abstract.ContractToolsFetch,
			want:     `{"title":"","content":""}`,
		},
		{
			name:     "moonshot/fetch writes the body alone",
			contract: abstract.ContractMoonshotFetch,
			page:     page,
			want:     "# Heading",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := For(testCase.contract).RenderFetch(abstract.Fetch{Page: testCase.page})
			if err != nil {
				t.Fatalf("RenderFetch returned %v", err)
			}
			if string(body) != testCase.want {
				t.Fatalf("RenderFetch =\n%s\nwant\n%s", body, testCase.want)
			}
		})
	}
}

func TestRenderersForwardANativeAnswerByteForByte(t *testing.T) {
	// The bytes are deliberately not what the renderer would write, so a
	// re-serialization would show up as a mismatch.
	raw := []byte(`{"upstream":true, "order":["untouched"]}`)
	cases := []struct {
		name     string
		contract abstract.Contract
		render   func(Renderer) ([]byte, error)
	}{
		{
			name:     "tools/search",
			contract: abstract.ContractToolsSearch,
			render: func(renderer Renderer) ([]byte, error) {
				return renderer.RenderSearch(abstract.Search{
					Results:     []abstract.Result{fullyKnownResult()},
					Raw:         raw,
					RawContract: abstract.ContractToolsSearch,
				})
			},
		},
		{
			name:     "tools/fetch",
			contract: abstract.ContractToolsFetch,
			render: func(renderer Renderer) ([]byte, error) {
				return renderer.RenderFetch(abstract.Fetch{
					Page:        abstract.Page{Title: "ignored", Content: "ignored"},
					Raw:         raw,
					RawContract: abstract.ContractToolsFetch,
				})
			},
		},
		{
			name:     "alpha/search",
			contract: abstract.ContractAlphaSearch,
			render: func(renderer Renderer) ([]byte, error) {
				return renderer.RenderSearch(abstract.Search{
					Raw:         raw,
					RawContract: abstract.ContractAlphaSearch,
				})
			},
		},
		{
			name:     "moonshot/search",
			contract: abstract.ContractMoonshotSearch,
			render: func(renderer Renderer) ([]byte, error) {
				return renderer.RenderSearch(abstract.Search{
					Raw:         raw,
					RawContract: abstract.ContractMoonshotSearch,
				})
			},
		},
		{
			name:     "moonshot/fetch",
			contract: abstract.ContractMoonshotFetch,
			render: func(renderer Renderer) ([]byte, error) {
				return renderer.RenderFetch(abstract.Fetch{
					Raw:         raw,
					RawContract: abstract.ContractMoonshotFetch,
				})
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := testCase.render(For(testCase.contract))
			if err != nil {
				t.Fatalf("render returned %v", err)
			}
			if string(body) != string(raw) {
				t.Fatalf("render = %q, want the raw bytes %q", body, raw)
			}
		})
	}
}

func TestNativeAnswerFromAnotherContractIsRendered(t *testing.T) {
	// A provider that speaks a different contract must still be rendered: only
	// the contract this renderer writes is forwarded.
	raw := []byte(`{"upstream":true}`)
	body, err := For(abstract.ContractToolsSearch).RenderSearch(abstract.Search{
		Results:     []abstract.Result{fullyUnknownResult()},
		Raw:         raw,
		RawContract: abstract.ContractMoonshotSearch,
	})
	if err != nil {
		t.Fatalf("RenderSearch returned %v", err)
	}
	if string(body) == string(raw) {
		t.Fatalf("RenderSearch forwarded a raw body of another contract: %s", body)
	}
}

func TestEmptyRawIsNeverForwarded(t *testing.T) {
	// An empty Raw with a matching contract still renders: forwarding nothing
	// would answer the client with an empty body.
	body, err := For(abstract.ContractToolsFetch).RenderFetch(abstract.Fetch{
		Page:        abstract.Page{Title: "Example title", Content: "body"},
		RawContract: abstract.ContractToolsFetch,
	})
	if err != nil {
		t.Fatalf("RenderFetch returned %v", err)
	}
	if string(body) != `{"title":"Example title","content":"body"}` {
		t.Fatalf("RenderFetch = %s, want the rendered page", body)
	}
}

func TestRenderersRefuseTheOtherAnswerHalf(t *testing.T) {
	cases := []struct {
		name     string
		contract abstract.Contract
	}{
		{"tools/fetch", abstract.ContractToolsFetch},
		{"moonshot/fetch", abstract.ContractMoonshotFetch},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := For(testCase.contract).RenderSearch(abstract.Search{}); err == nil {
				t.Fatal("RenderSearch accepted a fetch contract")
			}
		})
	}

	searchContracts := []abstract.Contract{
		abstract.ContractToolsSearch,
		abstract.ContractAlphaSearch,
		abstract.ContractMoonshotSearch,
	}
	for _, contract := range searchContracts {
		if _, err := For(contract).RenderFetch(abstract.Fetch{}); err == nil {
			t.Fatalf("%s accepted a fetch answer", contract)
		}
	}
}
