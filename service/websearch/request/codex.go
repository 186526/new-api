package request

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"
)

func init() {
	Register(&codexRequester{})
}

type codexRequester struct{}

type codexSearchBody struct {
	Model    string `json:"model"`
	Commands struct {
		SearchQuery [1]codexSearchTerm `json:"search_query"`
	} `json:"commands"`
}

type codexSearchTerm struct {
	Query string `json:"q"`
}

func (*codexRequester) Name() string { return websearch_setting.ProviderCodex }

func (*codexRequester) Native() []abstract.Contract {
	return []abstract.Contract{abstract.ContractAlphaSearch}
}

func (r *codexRequester) Search(ctx context.Context, channel abstract.ChannelContext, query Query) (Upstream, error) {
	endpoint := strings.TrimSpace(query.Endpoint)
	if endpoint == "" {
		return Upstream{}, notSent(errors.New("websearch: codex has no channel alpha search endpoint"))
	}

	body, cacheKey, err := codexSearchPayload(channel.ChannelType, query)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	build := func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, notSent(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if err := query.applyPrepare(req); err != nil {
			return nil, notSent(err)
		}
		return req, nil
	}

	upstream, err := Forward(ctx, channel, build, ForwardOptions{
		CacheKey:        cacheKey,
		Search:          true,
		CacheTTLSeconds: channel.Settings.WebSearchCacheTTLSeconds,
	})
	upstream.Family = r.Name()
	return upstream, err
}

func codexSearchPayload(channelType int, query Query) ([]byte, string, error) {
	if query.Contract == abstract.ContractAlphaSearch {
		if len(query.Raw) == 0 {
			return nil, "", errors.New("websearch: codex alpha search body is empty")
		}
		return query.Raw, rawBodyCacheHash(query.Raw), nil
	}
	if channelType != constant.ChannelTypeCodex || query.Prepare == nil {
		return nil, "", errors.New("websearch: codex search rebuild requires a codex channel adaptor")
	}
	model := strings.TrimSpace(query.Model)
	if model == "" || model == constant.ToolsSearchModel {
		return nil, "", errors.New("websearch: codex search rebuild requires a mapped upstream model")
	}

	payload := codexSearchBody{Model: model}
	payload.Commands.SearchQuery[0].Query = query.Text
	body, err := common.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return body, "rebuilt\x00" + rawBodyCacheHash(body), nil
}

func (*codexRequester) Fetch(context.Context, abstract.ChannelContext, Query) (Upstream, error) {
	return Upstream{}, notSent(errors.New("websearch: codex has no fetch contract"))
}
