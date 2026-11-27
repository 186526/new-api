package request

import (
	"bytes"
	"context"
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

// codexAlphaSearchPath is the Codex search document. A New API or Sub2API
// channel serves the same document at /v1/alpha/search, which is why the path
// only applies to the provider default base URL.
const codexAlphaSearchPath = "/backend-api/codex/alpha/search"

func init() {
	Register(&codexRequester{})
}

// codexRequester forwards the Codex alpha search document. Codex publishes no
// fetch contract and its credential is an OAuth JSON blob that only the channel
// adaptor can read, so it can serve nothing outside its own channel.
type codexRequester struct{}

func (*codexRequester) Name() string { return websearch_setting.ProviderCodex }

func (*codexRequester) Native() []abstract.Contract {
	return []abstract.Contract{abstract.ContractAlphaSearch}
}

func (r *codexRequester) Search(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	if !query.isNative(r, abstract.ContractAlphaSearch) {
		// Either the caller reached Codex without one of its channels, or it
		// asked for the search by text. Codex has no standalone endpoint and no
		// way to sign a rebuilt body here, so the request must not be sent: the
		// caller may then try another candidate without a double charge.
		return Upstream{}, notSent(errors.New("websearch: codex has no standalone endpoint"))
	}

	build := func(ctx context.Context) (*http.Request, error) {
		endpoint := query.endpointOrDefault(channel, codexAlphaSearchPath, constant.ChannelTypeCodex)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(query.Raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if err := query.applyPrepare(req); err != nil {
			return nil, err
		}
		return req, nil
	}

	upstream, err := Forward(ctx, channel, build, ForwardOptions{
		// The forwarded body is the only input besides the URL, so its digest
		// alone keeps two different searches apart.
		CacheKey:        rawBodyCacheHash(query.Raw),
		Search:          true,
		CacheTTLSeconds: channel.Settings.WebSearchCacheTTLSeconds,
	})
	upstream.Family = r.Name()
	return upstream, err
}

func (*codexRequester) Fetch(context.Context, abstract.ChannelContext, Query) (Upstream, error) {
	return Upstream{}, notSent(errors.New("websearch: codex has no fetch contract"))
}
