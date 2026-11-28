package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/service/websearch/request"
)

func init() {
	request.Register(hostedTools{"claude", claudeSearch, claudeFetch})
	request.Register(hostedTools{"gemini", groundedSearch, nil})
	request.Register(hostedTools{"vertex", groundedSearch, nil})
	request.Register(hostedTools{"openai", responsesSearch("openai"), nil})
	request.Register(hostedTools{"azure", responsesSearch("azure"), nil})
	request.Register(hostedTools{"xai", responsesSearch("xai"), nil})
	request.Register(hostedTools{"ali", responsesSearch("ali"), nil})
	abstract.RegisterParser(hostedFamily, hostedParser{})
}

const hostedFamily = "hosted_tools_normalized"

type hostedCall struct {
	payload any
	decode  func([]byte) (hostedBody, error)
}

type hostedTools struct {
	name   string
	search func(request.Query) hostedCall
	fetch  func(request.Query) hostedCall
}

func (provider hostedTools) Name() string       { return provider.name }
func (hostedTools) Native() []abstract.Contract { return nil }
func (provider hostedTools) Search(ctx context.Context, selected abstract.ChannelContext, query request.Query) (request.Upstream, error) {
	if provider.search == nil || strings.TrimSpace(query.Text) == "" || query.Model == "" || !provider.prepared(query) {
		return unsupportedHostedRequest()
	}
	return provider.forward(ctx, selected, query, provider.search(query))
}

func (provider hostedTools) Fetch(ctx context.Context, selected abstract.ChannelContext, query request.Query) (request.Upstream, error) {
	if provider.fetch == nil || !provider.prepared(query) || parsedWebURL(query.URL) == nil || (provider.name == "claude" && (len(query.URL) > 250 || query.Model == "")) {
		return unsupportedHostedRequest()
	}
	if err := service.ValidateSSRFProtectedFetchURL(query.URL); err != nil {
		return hostedPreparationError(errors.New("websearch: fetch target is not permitted"))
	}
	return provider.forward(ctx, selected, query, provider.fetch(query))
}

func (provider hostedTools) prepared(query request.Query) bool {
	return query.EndpointProvider == provider.name && query.Endpoint != "" && query.Prepare != nil
}

func unsupportedHostedRequest() (request.Upstream, error) {
	return hostedPreparationError(abstract.ErrUnsupportedChannel)
}

func hostedPreparationError(err error) (request.Upstream, error) {
	return request.Forward(context.Background(), abstract.ChannelContext{}, func(context.Context) (*http.Request, error) {
		return nil, err
	}, request.ForwardOptions{CacheTTLSeconds: -1})
}

func (provider hostedTools) forward(ctx context.Context, selected abstract.ChannelContext, query request.Query, call hostedCall) (request.Upstream, error) {
	payload, err := json.Marshal(call.payload)
	if err != nil {
		return hostedPreparationError(err)
	}
	build := func(attempt context.Context) (*http.Request, error) {
		upstream, err := http.NewRequestWithContext(attempt, http.MethodPost, query.Endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, errors.New("websearch: invalid prepared endpoint")
		}
		if err := query.Prepare(upstream); err != nil {
			return nil, err
		}
		upstream.Header.Set("Content-Type", "application/json")
		return upstream, nil
	}
	// Provider envelopes can contain generated prose, encrypted sources and tool errors.
	// Cache only after the response can be normalized; until Forward exposes that hook,
	// keep these new requesters out of the shared cache.
	answer, err := request.Forward(ctx, selected, build, request.ForwardOptions{Search: query.Contract.IsSearch(), CacheTTLSeconds: -1})
	answer.Family = hostedFamily
	if err != nil || answer.StatusCode < 200 || answer.StatusCode >= 300 {
		return answer, err
	}
	body, err := call.decode(answer.Body)
	if err != nil {
		return answer, err
	}
	answer.Body, err = json.Marshal(body)
	answer.Header.Del("Content-Type")
	answer.Header.Del("Content-Encoding")
	answer.Header.Del("Content-Length")
	return answer, err
}

type hostedBody struct {
	Hits []abstract.Result `json:"hits,omitempty"`
	Page abstract.Page     `json:"page,omitempty"`
}

type hostedParser struct{}

func (hostedParser) ParseSearch(raw []byte) ([]abstract.Result, error) {
	var body hostedBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if len(body.Hits) == 0 {
		return nil, abstract.ErrEmptyResults
	}
	return body.Hits, nil
}

func (hostedParser) ParseFetch(raw []byte) (abstract.Page, error) {
	var body hostedBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return abstract.Page{}, err
	}
	if strings.TrimSpace(body.Page.Content) == "" {
		return abstract.Page{}, abstract.ErrEmptyContent
	}
	return body.Page, nil
}

func parsedWebURL(target string) *url.URL {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil
	}
	return parsed
}

func sameWebTarget(first, second string) bool {
	left, right := parsedWebURL(first), parsedWebURL(second)
	return left != nil && right != nil && left.Scheme == right.Scheme && strings.EqualFold(left.Host, right.Host) && left.EscapedPath() == right.EscapedPath() && left.RawQuery == right.RawQuery
}

func addHostedHit(hits []abstract.Result, seen map[string]bool, target, title string) []abstract.Result {
	target = strings.TrimSpace(target)
	if seen[target] || parsedWebURL(target) == nil {
		return hits
	}
	seen[target] = true
	return append(hits, abstract.Result{URL: target, Title: strings.TrimSpace(title), Authority: -1})
}

func refused(raw json.RawMessage) bool {
	return len(raw) != 0 && string(raw) != "null"
}

func encodeSearchInput(query string) string {
	input, _ := json.Marshal(strings.TrimSpace(query))
	return "Use the available web search tool for this search query. Return no prose. Query: " + string(input)
}

func zhipuReader(query request.Query) hostedCall {
	return hostedCall{map[string]any{"url": strings.TrimSpace(query.URL), "return_format": "markdown", "retain_images": false}, func(raw []byte) (hostedBody, error) {
		var response struct {
			Error  json.RawMessage                       `json:"error"`
			Result *struct{ URL, Title, Content string } `json:"reader_result"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Result == nil || refused(response.Error) {
			return hostedBody{}, errors.New("websearch: Zhipu Reader refused or returned an invalid response")
		}
		if !sameWebTarget(query.URL, response.Result.URL) {
			return hostedBody{}, errors.New("websearch: Zhipu Reader returned a different URL")
		}
		return hostedBody{Page: abstract.Page{Title: response.Result.Title, Content: response.Result.Content}}, nil
	}}
}

type zhipuReaderRequester struct{ request.Requester }

func (provider zhipuReaderRequester) Fetch(ctx context.Context, selected abstract.ChannelContext, query request.Query) (request.Upstream, error) {
	return (hostedTools{provider.Name(), nil, zhipuReader}).Fetch(ctx, selected, query)
}

func init() {
	if provider, err := request.For("zhipu"); err == nil {
		request.Register(zhipuReaderRequester{provider})
	}
}
