package request

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

func init() {
	Register(restSearchRequester{websearch_setting.ProviderZhipu, constant.ChannelTypeZhipu_v4})
	Register(restSearchRequester{websearch_setting.ProviderBaiduV2, constant.ChannelTypeBaiduV2})
}

type restSearchRequester struct {
	provider    string
	channelType int
}

func (requester restSearchRequester) Name() string      { return requester.provider }
func (restSearchRequester) Native() []abstract.Contract { return nil }
func (restSearchRequester) Fetch(context.Context, abstract.ChannelContext, Query) (Upstream, error) {
	return Upstream{}, notSent(abstract.ErrUnsupportedChannel)
}

func (requester restSearchRequester) Search(ctx context.Context, selected abstract.ChannelContext, query Query) (Upstream, error) {
	if selected.ChannelType != requester.channelType || !abstract.RESTSearchSupportsBase(selected.ChannelType, selected.BaseURL) || !abstract.RESTSearchSupportsKey(selected.ChannelType, selected.Key) {
		return Upstream{}, notSent(abstract.ErrUnsupportedChannel)
	}
	text := strings.TrimSpace(query.Text)
	if text == "" {
		return Upstream{}, notSent(errors.New("websearch: missing search query"))
	}
	path, payload, err := requester.encode(text, min(max(query.Count, 0), 50))
	if err != nil {
		return Upstream{}, notSent(err)
	}
	build := func(attempt context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(attempt, http.MethodPost, query.endpointOrDefault(selected, path, requester.channelType), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+selected.Key)
		if err := query.applyPrepare(req); err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}
	parser, err := abstract.ParserFor(requester.provider)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	upstream, err := forwardManaged(ctx, selected, query, build, ForwardOptions{
		CacheKey: CacheKeyFor(requester.provider, string(payload)), Search: true, CacheTTLSeconds: selected.Settings.WebSearchCacheTTLSeconds,
		normalize: func(_ context.Context, entry forwardEntry) (forwardEntry, error) {
			if entry.StatusCode >= 200 && entry.StatusCode < 300 {
				if _, err := parser.ParseSearch(entry.Body); err != nil && !errors.Is(err, abstract.ErrEmptyResults) {
					return entry, err
				}
			}
			return entry, nil
		},
	})
	upstream.Family = requester.provider
	return upstream, err
}

func (requester restSearchRequester) encode(text string, count int) (string, []byte, error) {
	if requester.channelType == constant.ChannelTypeZhipu_v4 {
		if utf8.RuneCountInString(text) > 70 {
			return "", nil, errors.New("websearch: Zhipu search query exceeds 70 characters")
		}
		payload, err := common.Marshal(struct {
			Query  string `json:"search_query"`
			Engine string `json:"search_engine"`
			Intent bool   `json:"search_intent"`
			Count  int    `json:"count,omitempty"`
		}{text, "search_std", false, count})
		return "/api/paas/v4/web_search", payload, err
	}
	type message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type resource struct {
		Type string `json:"type"`
		TopK int    `json:"top_k"`
	}
	wire := struct {
		Messages  []message  `json:"messages"`
		Source    string     `json:"search_source"`
		Resources []resource `json:"resource_type_filter,omitempty"`
	}{Messages: []message{{"user", text}}, Source: "baidu_search_v2"}
	if count > 0 {
		wire.Resources = []resource{{"web", count}}
	}
	payload, err := common.Marshal(wire)
	return "/v2/ai_search/web_search", payload, err
}
