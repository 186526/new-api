package request

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

func init() {
	Register(directToolsRequester{websearch_setting.ProviderMoonshot, constant.ChannelTypeMoonshot})
	Register(directToolsRequester{websearch_setting.ProviderPerplexity, constant.ChannelTypePerplexity})
}

type directToolsRequester struct {
	provider    string
	channelType int
}

func (r directToolsRequester) Name() string              { return r.provider }
func (directToolsRequester) Native() []abstract.Contract { return nil }

func (r directToolsRequester) Search(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	text := strings.TrimSpace(query.Text)
	if text == "" {
		return Upstream{}, notSent(fmt.Errorf("websearch: missing search query"))
	}
	count := min(max(query.Count, 0), 20)
	if r.provider == websearch_setting.ProviderMoonshot {
		return r.forward(ctx, channel, query, directToolsCall{"/v1/tools/search", struct {
			Text           string `json:"text_query"`
			Limit          int    `json:"limit,omitempty"`
			IncludeContent bool   `json:"include_content,omitempty"`
		}{text, count, query.NeedContent}, true})
	}
	if len(text) > 8192 {
		return Upstream{}, notSent(fmt.Errorf("websearch: Perplexity search query exceeds 8192 bytes"))
	}
	return r.forward(ctx, channel, query, directToolsCall{"/search", struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results,omitempty"`
	}{text, count}, true})
}

func (r directToolsRequester) Fetch(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	if r.provider != websearch_setting.ProviderMoonshot {
		return Upstream{}, notSent(abstract.ErrUnsupportedChannel)
	}
	target, err := url.Parse(query.URL)
	if err != nil || target.Hostname() == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return Upstream{}, notSent(fmt.Errorf("websearch: expected an HTTP or HTTPS URL"))
	}
	return r.forward(ctx, channel, query, directToolsCall{"/v1/tools/fetch", struct {
		URL string `json:"url"`
	}{query.URL}, false})
}

type directToolsCall struct {
	path    string
	payload any
	search  bool
}

func (r directToolsRequester) forward(ctx context.Context, channel abstract.ChannelContext, query Query, call directToolsCall) (Upstream, error) {
	if channel.ChannelType != r.channelType || strings.TrimSpace(channel.Key) == "" {
		return Upstream{}, notSent(abstract.ErrUnsupportedChannel)
	}
	payload, err := common.Marshal(call.payload)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	build := func(attempt context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(attempt, http.MethodPost, query.endpointOrDefault(channel, call.path, r.channelType), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+channel.Key)
		if err := query.applyPrepare(req); err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	}
	ttl := channel.Settings.WebSearchFetchCacheTTLSeconds
	if call.search {
		ttl = channel.Settings.WebSearchCacheTTLSeconds
	}
	upstream, err := forwardManaged(ctx, channel, query, build, ForwardOptions{
		CacheKey: CacheKeyFor(r.Name(), string(payload)), Search: call.search, CacheTTLSeconds: ttl,
	})
	upstream.Family = r.Name()
	return upstream, err
}
