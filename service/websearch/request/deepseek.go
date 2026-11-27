package request

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

const (
	deepSeekSearchPath = "/v1/tools/search"
	deepSeekFetchPath  = "/v1/tools/fetch"
)

func init() {
	Register(&deepSeekRequester{})
}

// deepSeekRequester speaks the DeepSeek tools endpoints. It implements both
// halves because search and fetch share one base URL, one credential and one
// retry policy, all of which Forward already applies.
type deepSeekRequester struct{}

func (*deepSeekRequester) Name() string { return websearch_setting.ProviderDeepSeek }

func (*deepSeekRequester) Native() []abstract.Contract {
	return []abstract.Contract{abstract.ContractToolsSearch, abstract.ContractToolsFetch}
}

// deepSeekSearchBody is the request rebuilt for a query that did not arrive as
// a DeepSeek body, such as one a channel's websearch_provider setting routed
// here. Count is omitted when zero because the upstream reads a missing count as
// its own default.
type deepSeekSearchBody struct {
	Query       string `json:"query"`
	NeedContent bool   `json:"need_content"`
	Count       int    `json:"count,omitempty"`
}

type deepSeekFetchBody struct {
	URL string `json:"url"`
}

func (r *deepSeekRequester) Search(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	native := query.isNative(r, abstract.ContractToolsSearch)

	build := func(ctx context.Context) (*http.Request, error) {
		var body []byte
		if native {
			// The client reached its own upstream and must keep sending exactly
			// what it wrote: re-encoding would reorder members and drop the
			// fields this package does not model.
			body = query.Raw
		} else {
			rebuilt, err := common.Marshal(deepSeekSearchBody{
				Query:       query.Text,
				NeedContent: query.NeedContent,
				Count:       query.Count,
			})
			if err != nil {
				return nil, err
			}
			body = rebuilt
		}
		return deepSeekRequest(ctx, channel, query, deepSeekSearchPath, body)
	}

	cacheKey := deepSeekSearchCacheKey(query, native)
	upstream, err := Forward(ctx, channel, build, ForwardOptions{
		CacheKey:        cacheKey,
		Search:          true,
		CacheTTLSeconds: channel.Settings.WebSearchCacheTTLSeconds,
	})
	upstream.Family = r.Name()
	return upstream, err
}

func (r *deepSeekRequester) Fetch(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	native := query.isNative(r, abstract.ContractToolsFetch)

	build := func(ctx context.Context) (*http.Request, error) {
		var body []byte
		if native {
			body = query.Raw
		} else {
			rebuilt, err := common.Marshal(deepSeekFetchBody{URL: query.URL})
			if err != nil {
				return nil, err
			}
			body = rebuilt
		}
		return deepSeekRequest(ctx, channel, query, deepSeekFetchPath, body)
	}

	cacheKey := query.URL
	if native {
		cacheKey += "\x00raw\x00" + rawBodyCacheHash(query.Raw)
	}
	upstream, err := Forward(ctx, channel, build, ForwardOptions{
		CacheKey:        cacheKey,
		Search:          false,
		CacheTTLSeconds: channel.Settings.WebSearchFetchCacheTTLSeconds,
	})
	upstream.Family = r.Name()
	return upstream, err
}

// deepSeekRequest builds one attempt. The default headers identify this package
// to the upstream; Prepare runs last because only the channel's adaptor knows
// the credential it signs with and must be able to replace them.
func deepSeekRequest(ctx context.Context, channel abstract.ChannelContext, query Query, path string, body []byte) (*http.Request, error) {
	endpoint := query.endpointOrDefault(channel, path, constant.ChannelTypeDeepSeek)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if channel.Key != "" {
		req.Header.Set("Authorization", "Bearer "+channel.Key)
	}
	if err := query.applyPrepare(req); err != nil {
		return nil, err
	}
	return req, nil
}

// deepSeekSearchCacheKey separates a forwarded body from a rebuilt one. Two
// clients can send different bodies to the same endpoint, and Forward's key
// already covers the URL only, so the raw body must be part of the key or the
// two would read each other's answer.
func deepSeekSearchCacheKey(query Query, native bool) string {
	key := query.Text + "\x00" + strconv.Itoa(query.Count) + "\x00" + strconv.FormatBool(query.NeedContent)
	if native {
		key += "\x00raw\x00" + rawBodyCacheHash(query.Raw)
	}
	return key
}

// rawBodyCacheHash is the digest a forwarded body contributes to a cache key.
// The body itself is too large for a key, and its bytes are the only input that
// distinguishes two native requests to the same URL.
func rawBodyCacheHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// isNative reports whether this request carries the provider's own document and
// an upstream to send it to. Without an Endpoint the request is served here from
// Text or URL, because the provider was selected through a global credential
// rather than through one of its channels.
func (q Query) isNative(requester Requester, contract abstract.Contract) bool {
	if len(q.Raw) == 0 || q.Endpoint == "" {
		return false
	}
	for _, known := range requester.Native() {
		if known == contract {
			return true
		}
	}
	return false
}

// endpointOrDefault resolves where one request goes: the endpoint the relay
// layer injected, then the channel's own base URL, then the provider default.
func (q Query) endpointOrDefault(channel abstract.ChannelContext, path string, channelType int) string {
	if endpoint := strings.TrimSpace(q.Endpoint); endpoint != "" {
		return endpoint
	}
	base := strings.TrimRight(strings.TrimSpace(channel.BaseURL), "/")
	if base == "" {
		base = strings.TrimRight(constant.GetChannelBaseURL(channelType), "/")
	}
	return base + path
}

// applyPrepare lets the relay layer sign the request. It runs after the default
// headers so the adaptor's own credential wins.
func (q Query) applyPrepare(req *http.Request) error {
	if q.Prepare == nil {
		return nil
	}
	return q.Prepare(req)
}
