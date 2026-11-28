package request

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
)

func forwardManaged(ctx context.Context, channel abstract.ChannelContext, query Query, build func(context.Context) (*http.Request, error), options ForwardOptions) (Upstream, error) {
	options.cacheSubject = forwardSubject(query.Text, query.URL)
	return Forward(ctx, channel, build, options)
}

func forwardCached(ctx context.Context, subject, key string, ttl time.Duration, fetch func() (forwardEntry, error)) (forwardEntry, bool, error) {
	if redaction.IsProtectedRequest(ctx) || ttl <= 0 {
		entry, err := fetch()
		return entry, false, err
	}
	key, err := forwardGenerations.key(ctx, cacheRedis(), subject, tenantCacheKey(ctx, key))
	if err != nil {
		entry, err := fetch()
		return entry, false, err
	}
	return forwardThrough(key, ttl, fetch)
}

func tenantCacheKey(ctx context.Context, key string) string {
	policy, _ := abstract.RequestRoutingPolicy(ctx)
	return CacheKeyFor(strconv.Itoa(abstract.CacheTenant(ctx)), policy.Group+"\x00"+key)
}

func redirectManagedKey(ctx context.Context, target string) (string, error) {
	return redirectGenerations.key(ctx, cacheRedis(), redirectCacheKey(target), tenantCacheKey(ctx, ""))
}
