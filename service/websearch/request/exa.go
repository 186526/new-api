package request

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

const (
	exaSearchPath      = "/search"
	exaFetchPath       = "/contents"
	defaultExaEndpoint = "https://api.exa.ai"
	// exaTextCharacters bounds the text Exa returns per hit. The parser keeps the
	// whole value, so this is what stops an article from filling the caller's
	// context.
	exaTextCharacters = 2000
)

// exaRequester sends a query to Exa. Exa publishes none of the client contracts,
// so Native is empty and every answer is rendered by the exa parser.
type exaRequester struct{}

func init() {
	Register(exaRequester{})
}

func (exaRequester) Name() string { return websearch_setting.ProviderExa }

// Native reports nothing: Exa answers in its own document, never in one the
// client asked for.
func (exaRequester) Native() []abstract.Contract { return nil }

func (exaRequester) Search(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	if query.CustomOperation != nil {
		return sendCustomTool(ctx, channel, query, customToolClient)
	}
	apiKey, err := exaAPIKey(channel)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	payload, err := common.Marshal(exaSearchPayloadOf(query))
	if err != nil {
		return Upstream{}, notSent(err)
	}

	upstream, err := forwardManaged(ctx, channel, query, func(requestCtx context.Context) (*http.Request, error) {
		return exaRequest(requestCtx, exaBaseURL(channel, query)+exaSearchPath, apiKey, payload, query.Prepare)
	}, ForwardOptions{
		Search:   true,
		CacheKey: CacheKeyFor(websearch_setting.ProviderExa, string(payload)),
	})
	if err != nil {
		return Upstream{}, err
	}
	upstream.Family = exaRequester{}.Name()
	return upstream, nil
}

func (exaRequester) Fetch(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	if query.CustomOperation != nil {
		return sendCustomTool(ctx, channel, query, customToolClient)
	}
	apiKey, err := exaAPIKey(channel)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	payload, err := common.Marshal(exaFetchPayload{
		URLs: []string{query.URL},
		Text: exaText{MaxCharacters: exaTextCharacters},
	})
	if err != nil {
		return Upstream{}, notSent(err)
	}

	upstream, err := forwardManaged(ctx, channel, query, func(requestCtx context.Context) (*http.Request, error) {
		return exaRequest(requestCtx, exaBaseURL(channel, query)+exaFetchPath, apiKey, payload, query.Prepare)
	}, ForwardOptions{
		CacheKey: CacheKeyFor("exa-fetch-verified-v1", query.URL),
		normalize: func(_ context.Context, entry forwardEntry) (forwardEntry, error) {
			return normalizeExaContents(query.URL, entry)
		},
	})
	if err != nil {
		return Upstream{}, err
	}
	upstream.Family = exaRequester{}.Name()
	return upstream, nil
}

func exaAPIKey(channel abstract.ChannelContext) (string, error) {
	if key := strings.TrimSpace(channel.Key); key != "" {
		return key, nil
	}
	return "", errors.New("websearch: exa has no api key")
}

// A custom endpoint and its credential must be selected together by the caller.
func exaBaseURL(channel abstract.ChannelContext, query Query) string {
	base := strings.TrimSpace(query.Endpoint)
	if base == "" {
		base = strings.TrimSpace(channel.BaseURL)
	}
	if base == "" {
		base = defaultExaEndpoint
	}
	return strings.TrimRight(base, "/")
}

func exaRequest(ctx context.Context, endpoint, apiKey string, payload []byte, prepare func(*http.Request) error) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return nil, notSent(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	if prepare != nil {
		if err := prepare(req); err != nil {
			// The request never left with the auth the relay layer intended, so
			// the caller may still try another candidate without a double charge.
			return nil, notSent(err)
		}
	}
	return req, nil
}

type exaSearchPayload struct {
	Query string `json:"query"`
	Type  string `json:"type"`
	// NumResults is omitted rather than sent as zero: Exa reads an absent member
	// as its own default of ten, and a zero would ask for no result at all.
	NumResults int               `json:"numResults,omitempty"`
	Contents   exaSearchContents `json:"contents"`
}

type exaSearchContents struct {
	// Text is a pointer so a request that needs no content leaves the member out
	// instead of asking for zero characters.
	Text       *exaText `json:"text,omitempty"`
	Highlights bool     `json:"highlights"`
	Summary    bool     `json:"summary"`
}

type exaText struct {
	MaxCharacters int `json:"maxCharacters"`
}

type exaFetchPayload struct {
	URLs []string `json:"urls"`
	Text exaText  `json:"text"`
}

func exaSearchPayloadOf(query Query) exaSearchPayload {
	contents := exaSearchContents{Highlights: true, Summary: true}
	if query.NeedContent {
		contents.Text = &exaText{MaxCharacters: exaTextCharacters}
	}
	payload := exaSearchPayload{
		Query:    query.Text,
		Type:     "auto",
		Contents: contents,
	}
	if query.Count > 0 {
		payload.NumResults = query.Count
	}
	return payload
}
