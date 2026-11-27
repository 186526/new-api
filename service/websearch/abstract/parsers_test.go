package abstract

import (
	"errors"
	"strings"
	"testing"
)

func parserFor(t *testing.T, family string) Parser {
	t.Helper()
	parser, err := ParserFor(family)
	if err != nil {
		t.Fatalf("ParserFor(%q): %v", family, err)
	}
	return parser
}

func TestParsersAreRegisteredForEveryFamily(t *testing.T) {
	families := []string{"deepseek", "codex", "antigravity", "exa", "local"}
	for _, family := range families {
		parser := parserFor(t, family)
		if parser == nil {
			t.Fatalf("family %q registered a nil parser", family)
		}
	}
}

func TestDeepSeekParseSearchNormalizesNullMembers(t *testing.T) {
	body := []byte(`{"results":[
	  {"title":"One","url":"https://a.example","snippet":"s1","summary":null,"content":null,"site_name":null,"site_icon":null,"date":null,"authority":null,"rerank_score":0.5},
	  {"title":"Two","url":"https://b.example","snippet":"s2","summary":"sum","content":"body","site_name":"B","site_icon":"https://b.example/favicon.ico","date":"2026-01-02","authority":3,"rerank_score":1.5}
	]}`)

	results, err := parserFor(t, "deepseek").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	first := results[0]
	if first.Summary != "" || first.Content != "" || first.SiteName != "" || first.SiteIcon != "" || first.Date != "" {
		t.Errorf("null members did not normalize to empty: %+v", first)
	}
	if first.Authority != -1 {
		t.Errorf("null authority = %d, want -1", first.Authority)
	}
	if first.Known.Summary || first.Known.Content || first.Known.SiteName || first.Known.SiteIcon || first.Known.Date || first.Known.Authority {
		t.Errorf("a null member is marked known: %+v", first.Known)
	}
	if !first.Known.RerankScore {
		t.Error("a present rerank_score is marked unknown")
	}
	if first.RerankScore != 0.5 {
		t.Errorf("rerank score = %v, want 0.5", first.RerankScore)
	}

	second := results[1]
	if second.Authority != 3 {
		t.Errorf("authority = %d, want 3", second.Authority)
	}
	for field, known := range map[string]bool{
		"summary":   second.Known.Summary,
		"content":   second.Known.Content,
		"site_name": second.Known.SiteName,
		"site_icon": second.Known.SiteIcon,
		"date":      second.Known.Date,
		"authority": second.Known.Authority,
		"rerank":    second.Known.RerankScore,
	} {
		if !known {
			t.Errorf("%s is marked unknown but was sent: %+v", field, second.Known)
		}
	}
}

func TestDeepSeekParseSearchDefaultsMissingAuthorityToMinusOne(t *testing.T) {
	body := []byte(`{"results":[{"title":"Only","url":"https://a.example"}]}`)
	results, err := parserFor(t, "deepseek").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if results[0].Authority != -1 {
		t.Errorf("authority = %d, want -1 for a missing member", results[0].Authority)
	}
	if results[0].Known.Authority {
		t.Error("a missing authority is marked known")
	}
}

func TestDeepSeekParseSearchDeduplicatesByURL(t *testing.T) {
	body := []byte(`{"results":[
	  {"title":"First","url":"https://a.example","snippet":"kept"},
	  {"title":"Second","url":"https://b.example"},
	  {"title":"Repeat","url":"https://a.example","snippet":"dropped"}
	]}`)
	results, err := parserFor(t, "deepseek").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 after deduplication", len(results))
	}
	if results[0].Snippet != "kept" {
		t.Errorf("deduplication kept the later duplicate: %+v", results[0])
	}
}

func TestDeepSeekParseSearchSkipsEmptyURL(t *testing.T) {
	body := []byte(`{"results":[{"title":"No URL","snippet":"s"},{"title":"Has URL","url":"https://a.example"}]}`)
	results, err := parserFor(t, "deepseek").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://a.example" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestDeepSeekParseSearchEmptyResults(t *testing.T) {
	if _, err := parserFor(t, "deepseek").ParseSearch([]byte(`{"results":[]}`)); !errors.Is(err, ErrEmptyResults) {
		t.Fatalf("err = %v, want ErrEmptyResults", err)
	}
	// A body whose only hits lack a URL is as empty as an empty list.
	if _, err := parserFor(t, "deepseek").ParseSearch([]byte(`{"results":[{"title":"x"}]}`)); !errors.Is(err, ErrEmptyResults) {
		t.Fatalf("err = %v, want ErrEmptyResults for URL-less hits", err)
	}
}

func TestDeepSeekParseFetch(t *testing.T) {
	page, err := parserFor(t, "deepseek").ParseFetch([]byte(`{"title":"Example","content":"# Body"}`))
	if err != nil {
		t.Fatalf("ParseFetch: %v", err)
	}
	if page.Title != "Example" || page.Content != "# Body" {
		t.Errorf("page = %+v", page)
	}
}

func TestDeepSeekParseFetchEmptyContent(t *testing.T) {
	if _, err := parserFor(t, "deepseek").ParseFetch([]byte(`{"title":"Example","content":"  \n "}`)); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("err = %v, want ErrEmptyContent", err)
	}
}

func TestCodexParseSearchReadsOnlyThreeFields(t *testing.T) {
	body := []byte(`{"id":"search-1","results":[
	  {"type":"text_result","title":"One","url":"https://a.example","snippet":"s1","summary":"ignored"},
	  {"type":"text_result","title":"Two","url":"https://b.example","snippet":"s2"}
	]}`)
	results, err := parserFor(t, "codex").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	first := results[0]
	if first.Title != "One" || first.URL != "https://a.example" || first.Snippet != "s1" {
		t.Errorf("first = %+v", first)
	}
	if first.Authority != -1 {
		t.Errorf("authority = %d, want -1", first.Authority)
	}
	if first.Known != (KnownFields{}) {
		t.Errorf("codex marked fields it never reads: %+v", first.Known)
	}
	if first.Summary != "" || first.Content != "" || first.SiteName != "" || first.SiteIcon != "" || first.Date != "" {
		t.Errorf("codex filled a field it does not read: %+v", first)
	}
}

func TestCodexParseSearchEmptyResults(t *testing.T) {
	if _, err := parserFor(t, "codex").ParseSearch([]byte(`{"id":"","results":[]}`)); !errors.Is(err, ErrEmptyResults) {
		t.Fatalf("err = %v, want ErrEmptyResults", err)
	}
}

func TestCodexParseFetchIsUnsupported(t *testing.T) {
	if _, err := parserFor(t, "codex").ParseFetch([]byte(`{}`)); !errors.Is(err, ErrUnsupportedContentType) {
		t.Fatalf("err = %v, want ErrUnsupportedContentType", err)
	}
}

func TestAntigravityParseSearchGroupsSegmentsByChunk(t *testing.T) {
	body := []byte(`{"candidates":[{
	  "content":{"parts":[{"text":"The model's own answer, not a citation."}]},
	  "groundingMetadata":{
	    "groundingChunks":[
	      {"web":{"uri":"https://a.example/page","title":"A Site"}},
	      {"web":{"uri":"https://b.example/page","title":"B Site"}},
	      {"web":{"uri":"https://a.example/page","title":"A Site"}}
	    ],
	    "groundingSupports":[
	      {"segment":{"text":"Birds migrate south."},"groundingChunkIndices":[0,1]},
	      {"segment":{"text":"They return in spring."},"groundingChunkIndices":[1]},
	      {"segment":{"text":"Uncited remark."},"groundingChunkIndices":[]}
	    ]
	  }
	}]}`)

	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 after deduplicating the repeated URL", len(results))
	}
	if results[0].Snippet != "Birds migrate south." {
		t.Errorf("first snippet = %q", results[0].Snippet)
	}
	if results[1].Snippet != "Birds migrate south. They return in spring." {
		t.Errorf("second snippet = %q, want both cited segments joined", results[1].Snippet)
	}
	if results[0].SiteName != "A Site" || results[0].URL != "https://a.example/page" {
		t.Errorf("first result = %+v", results[0])
	}
	if results[0].Title == "" {
		t.Error("title is empty")
	}
	if results[0].Authority != -1 {
		t.Errorf("authority = %d, want -1", results[0].Authority)
	}
	if !results[0].Known.SiteName {
		t.Errorf("the site name is not marked known: %+v", results[0].Known)
	}
	if results[0].Known.Summary || results[0].Known.Content || results[0].Known.Date || results[0].Known.Authority || results[0].Known.RerankScore {
		t.Errorf("grounding marked a field it never carries: %+v", results[0].Known)
	}
	for _, result := range results {
		if strings.Contains(result.Snippet, "The model's own answer") {
			t.Errorf("the answer text leaked into a snippet: %q", result.Snippet)
		}
	}
}

func TestAntigravityParseSearchTruncatesSnippetAndTitle(t *testing.T) {
	long := strings.Repeat("x", 500)
	body := []byte(`{"candidates":[{"groundingMetadata":{
	  "groundingChunks":[{"web":{"uri":"https://a.example","title":"A"}}],
	  "groundingSupports":[{"segment":{"text":"` + long + `"},"groundingChunkIndices":[0]}]
	}}]}`)

	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if got := len([]rune(results[0].Snippet)); got != antigravitySnippetMaxRune {
		t.Errorf("snippet length = %d, want %d", got, antigravitySnippetMaxRune)
	}
	if got := len([]rune(results[0].Title)); got != antigravityTitleMaxRune {
		t.Errorf("title length = %d, want %d", got, antigravityTitleMaxRune)
	}
}

func TestAntigravityParseSearchUsesTheSiteNameWithoutASnippet(t *testing.T) {
	body := []byte(`{"candidates":[{"groundingMetadata":{
	  "groundingChunks":[{"web":{"uri":"https://a.example","title":"A Site"}}]
	}}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if results[0].Snippet != "" {
		t.Errorf("snippet = %q, want empty", results[0].Snippet)
	}
	if results[0].Title != "A Site" {
		t.Errorf("title = %q, want the site name as fallback", results[0].Title)
	}
}

func TestAntigravityParseSearchReadsTheURLLines(t *testing.T) {
	// What the endpoint is asked for: one line per hit, carrying the three facts
	// a caller cannot recompute. Asking for page prose instead makes the upstream
	// copyright filter answer RECITATION and drop the body. This is the shape
	// written before the fourth column existed, and a cached answer of it still
	// has to parse.
	body := []byte(`{"candidates":[{"content":{"parts":[{"text":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI1 | July 17 2026 | The Go Programming Language\nhttps://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI2 | None | Linux.org"}]}}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want one per line", len(results))
	}
	first := results[0]
	if first.Title != "The Go Programming Language" {
		t.Errorf("title = %q, want the page title", first.Title)
	}
	if first.Date != "2026-07-17" {
		t.Errorf("date = %q, want the publication time in ISO 8601", first.Date)
	}
	if !first.Known.Date || !first.Known.SiteName {
		t.Errorf("known = %+v, want date and site name marked", first.Known)
	}
	if first.Snippet != "" {
		t.Errorf("snippet = %q, want empty: the model is not asked to repeat page prose", first.Snippet)
	}
	// "None" is how the upstream says a page carries no date.
	if results[1].Date != "" {
		t.Errorf("date = %q, want empty for None", results[1].Date)
	}
	if !results[1].Known.SiteName || results[1].Known.Date {
		t.Errorf("known = %+v for a hit without a date", results[1].Known)
	}
}

func TestAntigravityParseSearchReadsTheFourthColumn(t *testing.T) {
	// The current shape: the model adds a sentence of its own words, which is
	// what a hit falls back to when its page cannot be read. The separator is a
	// double pipe because a page title may contain a single one.
	body := []byte(`{"candidates":[{"content":{"parts":[{"text":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI1 || November 23 2026 || nginx || Enterprise server and commercial distribution\nhttps://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI2 || None || Nginx | Wikipedia || Encyclopedia entry about the web server"}]}}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want one per line", len(results))
	}
	if results[0].Title != "nginx" {
		t.Errorf("title = %q, want the page title", results[0].Title)
	}
	if results[0].Summary != "Enterprise server and commercial distribution" {
		t.Errorf("summary = %q, want the model's own sentence", results[0].Summary)
	}
	if !results[0].Known.Summary {
		t.Errorf("known = %+v, want the summary marked", results[0].Known)
	}
	if results[0].Date != "2026-11-23" {
		t.Errorf("date = %q, want ISO 8601", results[0].Date)
	}
	if results[0].Snippet != "" {
		t.Errorf("snippet = %q, want empty: the page supplies it", results[0].Snippet)
	}
	// A single pipe inside the title survives, because only " || " splits.
	if results[1].Title != "Nginx | Wikipedia" {
		t.Errorf("title = %q, want the pipe kept inside the title", results[1].Title)
	}
	if results[1].Summary != "Encyclopedia entry about the web server" {
		t.Errorf("summary = %q, want the model's own sentence", results[1].Summary)
	}
}

func TestAntigravityParseSearchKeepsADateItCannotRead(t *testing.T) {
	// A shape no layout matches is kept as it arrived: a caller can show an odd
	// date, but cannot recover one that was dropped.
	body := []byte(`{"candidates":[{"content":{"parts":[{"text":"https://a.example || last Tuesday || A page || A page about something"}]}}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if results[0].Date != "last Tuesday" {
		t.Errorf("date = %q, want the value kept as it arrived", results[0].Date)
	}
}

func TestAntigravityParseSearchRepairsASeparatorWrittenAsData(t *testing.T) {
	// Measured on the live upstream: the model sometimes writes the first
	// separator as the literal "data:" with no spaces around it, which glues it
	// to the URL ("...D8Adata:None|Title"). Read as one token, that URL answers
	// 404, so nine results could come back unopenable.
	body := []byte(`{"candidates":[{"content":{"parts":[{"text":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI9Adata:None|Artificial intelligence - Wikipedia"}]}}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].URL != "https://vertexaisearch.cloud.google.com/grounding-api-redirect/AUZI9A" {
		t.Errorf("url = %q, want the link with the glued separator removed", results[0].URL)
	}
	if results[0].Title != "Artificial intelligence - Wikipedia" {
		t.Errorf("title = %q, want the page title", results[0].Title)
	}
	if results[0].Date != "" {
		t.Errorf("date = %q, want empty for None", results[0].Date)
	}
}

func TestAntigravityParseSearchFallsBackToGroundingWhenTheLinesAreMissing(t *testing.T) {
	// A model that answers in prose instead of reporting the tool output leaves
	// the grounding metadata as the only result the upstream produced.
	body := []byte(`{"candidates":[{
	  "content":{"parts":[{"text":"Linux is a family of operating systems."}]},
	  "groundingMetadata":{
	    "groundingChunks":[{"web":{"uri":"https://a.example","domain":"a.example"}}],
	    "groundingSupports":[{"segment":{"text":"A cited sentence."},"groundingChunkIndices":[0]}]
	  }
	}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://a.example" {
		t.Fatalf("results = %+v, want the cited page", results)
	}
}

func TestAntigravityParseSearchReportsAProseAnswerWhenNothingWasSearched(t *testing.T) {
	body := []byte(`{"candidates":[{"content":{"parts":[
	  {"text":"**Linux** is a free, open-source operating system family. It runs on most servers."}
	]}}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want the answer reported as one", len(results))
	}
	if results[0].URL != "" || results[0].SiteName != "" {
		t.Errorf("url = %q site_name = %q, want both empty: nothing was cited", results[0].URL, results[0].SiteName)
	}
	if results[0].Title != "Linux is a free, open-source operating system family" {
		t.Errorf("title = %q, want the first sentence without markdown emphasis", results[0].Title)
	}
}

func TestAntigravityParseSearchDropsTheOverlapBetweenSegments(t *testing.T) {
	// A chunk cited twice in a row arrives as two overlapping segments, which
	// made the title a copy of the snippet's prefix.
	body := []byte(`{"candidates":[{"groundingMetadata":{
	  "groundingChunks":[{"web":{"uri":"https://nginx.org/","domain":"nginx.org"}}],
	  "groundingSupports":[
	    {"segment":{"text":"**NGINX** is a web server."},"groundingChunkIndices":[0]},
	    {"segment":{"text":"**NGINX** is a web server. It also proxies."},"groundingChunkIndices":[0]}
	  ]
	}}]}`)
	results, err := parserFor(t, "antigravity").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if got := results[0].Snippet; strings.Count(got, "is a web server") != 1 {
		t.Errorf("snippet repeats the overlapping sentence: %q", got)
	}
	if got := results[0].Title; strings.Contains(got, "*") {
		t.Errorf("title kept markdown emphasis: %q", got)
	}
}

func TestAntigravityParseSearchEmptyResults(t *testing.T) {
	body := []byte(`{"candidates":[{"groundingMetadata":{"groundingChunks":[],"groundingSupports":[]}}]}`)
	if _, err := parserFor(t, "antigravity").ParseSearch(body); !errors.Is(err, ErrEmptyResults) {
		t.Fatalf("err = %v, want ErrEmptyResults", err)
	}
}

func TestAntigravityParseFetchIsUnsupported(t *testing.T) {
	if _, err := parserFor(t, "antigravity").ParseFetch([]byte(`{}`)); !errors.Is(err, ErrUnsupportedContentType) {
		t.Fatalf("err = %v, want ErrUnsupportedContentType", err)
	}
}

func TestExaParseSearchMapsEveryField(t *testing.T) {
	body := []byte(`{"results":[{
	  "title":"Example",
	  "url":"https://www.example.com/article",
	  "publishedDate":"2026-01-02T00:00:00.000Z",
	  "favicon":"https://www.example.com/favicon.ico",
	  "text":"the body",
	  "highlights":["alpha","beta"],
	  "summary":"the summary"
	}]}`)

	results, err := parserFor(t, "exa").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	hit := results[0]
	if hit.Title != "Example" {
		t.Errorf("title = %q", hit.Title)
	}
	if hit.URL != "https://www.example.com/article" {
		t.Errorf("url = %q", hit.URL)
	}
	if hit.SiteName != "www.example.com" {
		t.Errorf("site name = %q, want the URL host", hit.SiteName)
	}
	if hit.Content != "the body" || hit.Summary != "the summary" {
		t.Errorf("content/summary = %q/%q", hit.Content, hit.Summary)
	}
	if hit.SiteIcon != "https://www.example.com/favicon.ico" {
		t.Errorf("site icon = %q", hit.SiteIcon)
	}
	if hit.Date != "2026-01-02T00:00:00.000Z" {
		t.Errorf("date = %q", hit.Date)
	}
	if hit.Authority != -1 {
		t.Errorf("authority = %d, want -1", hit.Authority)
	}
	if hit.RerankScore != 0 {
		t.Errorf("rerank score = %v, want 0", hit.RerankScore)
	}
	if !hit.Known.Summary || !hit.Known.Content || !hit.Known.SiteName || !hit.Known.SiteIcon || !hit.Known.Date {
		t.Errorf("a sent field is marked unknown: %+v", hit.Known)
	}
	if hit.Known.Authority || hit.Known.RerankScore {
		t.Errorf("exa marked a field it never sends: %+v", hit.Known)
	}
}

func TestExaParseSearchJoinsHighlightsWithNewline(t *testing.T) {
	body := []byte(`{"results":[{"url":"https://a.example","highlights":["first passage","second passage","third passage"]}]}`)
	results, err := parserFor(t, "exa").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	const want = "first passage\nsecond passage\nthird passage"
	if results[0].Snippet != want {
		t.Errorf("snippet = %q, want %q", results[0].Snippet, want)
	}
}

func TestExaParseSearchEmptyFieldsAreMarkedUnknown(t *testing.T) {
	body := []byte(`{"results":[{"url":"https://a.example","title":"T","text":"","summary":"","publishedDate":"","favicon":""}]}`)
	results, err := parserFor(t, "exa").ParseSearch(body)
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if results[0].Known.Content || results[0].Known.Summary || results[0].Known.Date || results[0].Known.SiteIcon {
		t.Errorf("an empty field is marked known: %+v", results[0].Known)
	}
}

func TestExaParseSearchSkipsEmptyURLAndReportsEmptyResults(t *testing.T) {
	if _, err := parserFor(t, "exa").ParseSearch([]byte(`{"results":[{"title":"No URL"}]}`)); !errors.Is(err, ErrEmptyResults) {
		t.Fatalf("err = %v, want ErrEmptyResults", err)
	}
}

func TestExaParseFetchTakesTheFirstResult(t *testing.T) {
	body := []byte(`{"results":[{"title":"First","text":"body one"},{"title":"Second","text":"body two"}]}`)
	page, err := parserFor(t, "exa").ParseFetch(body)
	if err != nil {
		t.Fatalf("ParseFetch: %v", err)
	}
	if page.Title != "First" || page.Content != "body one" {
		t.Errorf("page = %+v", page)
	}
}

func TestExaParseFetchEmptyContent(t *testing.T) {
	if _, err := parserFor(t, "exa").ParseFetch([]byte(`{"results":[{"title":"Empty","text":"   "}]}`)); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("err = %v, want ErrEmptyContent", err)
	}
	if _, err := parserFor(t, "exa").ParseFetch([]byte(`{"results":[]}`)); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("err = %v, want ErrEmptyContent for no result", err)
	}
}

func TestLocalParseSearchIsUnsupported(t *testing.T) {
	if _, err := parserFor(t, "local").ParseSearch([]byte(`{}`)); !errors.Is(err, ErrUnsupportedChannel) {
		t.Fatalf("err = %v, want ErrUnsupportedChannel", err)
	}
}

func TestLocalParseFetchReadsTheTitleFromTheBody(t *testing.T) {
	body := []byte("<!doctype html><html><head><title>  Page\n  Title </title></head><body><p>hi</p></body></html>")
	page, err := parserFor(t, "local").ParseFetch(body)
	if err != nil {
		t.Fatalf("ParseFetch: %v", err)
	}
	if page.Title != "Page Title" {
		t.Errorf("title = %q, want %q", page.Title, "Page Title")
	}
	if page.Content != string(body) {
		t.Errorf("content = %q, want the body unchanged", page.Content)
	}
}

func TestLocalParseFetchWithoutATitleKeepsItEmpty(t *testing.T) {
	page, err := parserFor(t, "local").ParseFetch([]byte("plain text, no markup"))
	if err != nil {
		t.Fatalf("ParseFetch: %v", err)
	}
	if page.Title != "" {
		t.Errorf("title = %q, want empty", page.Title)
	}
	if page.Content != "plain text, no markup" {
		t.Errorf("content = %q", page.Content)
	}
}
