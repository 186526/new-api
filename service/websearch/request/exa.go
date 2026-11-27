package request

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

const (
	exaSearchPath = "/search"
	exaFetchPath  = "/contents"
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
	apiKey, err := exaAPIKey(channel)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	payload, err := common.Marshal(exaSearchPayloadOf(query))
	if err != nil {
		return Upstream{}, notSent(err)
	}

	upstream, err := Forward(ctx, channel, func(requestCtx context.Context) (*http.Request, error) {
		return exaRequest(requestCtx, exaBaseURL(query)+exaSearchPath, apiKey, payload, query.Prepare)
	}, ForwardOptions{
		Search: true,
		// Exa's answer depends on the text and on the result count; the count is
		// part of the key because the same text at a different size is a different
		// upstream call.
		CacheKey: query.Text + "\x00" + strconv.Itoa(query.Count),
	})
	if err != nil {
		return Upstream{}, err
	}
	upstream.Family = exaRequester{}.Name()
	return upstream, nil
}

func (exaRequester) Fetch(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
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

	upstream, err := Forward(ctx, channel, func(requestCtx context.Context) (*http.Request, error) {
		return exaRequest(requestCtx, exaBaseURL(query)+exaFetchPath, apiKey, payload, query.Prepare)
	}, ForwardOptions{CacheKey: query.URL})
	if err != nil {
		return Upstream{}, err
	}
	upstream.Family = exaRequester{}.Name()
	return upstream, nil
}

// exaAPIKey prefers the channel's own key, which is what a channel that binds
// Exa holds, and falls back to the global key that lets Exa serve channels
// without one.
func exaAPIKey(channel abstract.ChannelContext) (string, error) {
	if key := strings.TrimSpace(channel.Key); key != "" {
		return key, nil
	}
	if key := strings.TrimSpace(websearch_setting.Get().ExaAPIKey); key != "" {
		return key, nil
	}
	return "", errors.New("websearch: exa has no api key")
}

// exaBaseURL lets the relay layer point a request at its own upstream, such as a
// self-hosted proxy, and otherwise sends it to the configured Exa endpoint.
func exaBaseURL(query Query) string {
	base := strings.TrimSpace(query.Endpoint)
	if base == "" {
		base = websearch_setting.ExaEndpoint()
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
