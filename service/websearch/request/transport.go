package request

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"

	"github.com/QuantumNous/new-api/service/redaction"
	"github.com/samber/hot"
	"golang.org/x/sync/singleflight"
)

const (
	forwardCacheNamespace = "websearch:forward:v3"
	forwardCacheCapacity  = 5000
	// forwardResponseLimit caps one upstream body. Every provider this package
	// speaks to answers with a search result list or one extracted page, so 8
	// MiB is far beyond a legitimate body and bounds a misbehaving upstream.
	forwardResponseLimit = 8 << 20
)

// forwardEntry is what the cache stores: the parts of an answer a renderer
// reads. Header is kept because a native pass-through returns the upstream's own
// headers to the client.
type forwardEntry struct {
	StatusCode  int         `json:"status_code"`
	ContentType string      `json:"content_type"`
	Body        []byte      `json:"body"`
	Header      http.Header `json:"header"`
}

var (
	forwardCacheOnce sync.Once
	forwardCache     *cachex.HybridCache[forwardEntry]
	forwardFlights   singleflight.Group
)

func getForwardCache() *cachex.HybridCache[forwardEntry] {
	forwardCacheOnce.Do(func() {
		forwardCache = cachex.NewHybridCache[forwardEntry](cachex.HybridCacheConfig[forwardEntry]{
			Namespace:  cachex.Namespace(forwardCacheNamespace),
			Redis:      common.RDB,
			RedisCodec: cachex.JSONCodec[forwardEntry]{},
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			Memory: func() *hot.HotCache[string, forwardEntry] {
				return hot.NewHotCache[string, forwardEntry](hot.LRU, forwardCacheCapacity).
					WithTTL(time.Duration(effectiveSearchTTL(0)) * time.Second).
					WithJanitor().
					Build()
			},
		})
	})
	return forwardCache
}

// ForwardOptions tunes one forwarded request. Every field falls back to the
// global setting when it is zero, so a caller only fills what it overrides.
type ForwardOptions struct {
	// CacheKey is what the answer depends on beyond the URL and the body, such
	// as the query text.
	CacheKey string
	// Search selects the search cache TTL over the fetch one.
	Search bool
	// CacheTTLSeconds overrides the TTL for this request. Zero follows the
	// global setting, a negative value skips the cache entirely.
	CacheTTLSeconds int
	// TimeoutSeconds overrides the header wait. Zero follows the global setting.
	TimeoutSeconds int
	cacheSubject   string
	stableBodyKey  bool // CacheKey replaces a body containing generated, nonsemantic fields.
	normalize      func(context.Context, forwardEntry) (forwardEntry, error)
}

// Forward returns the first upstream response, including refusals. Only a
// failure known to precede transmission permits another candidate.
func Forward(ctx context.Context, channel abstract.ChannelContext, build func(context.Context) (*http.Request, error), options ForwardOptions) (Upstream, error) {
	settings := websearch_setting.Get()

	timeout := options.TimeoutSeconds
	if timeout <= 0 {
		timeout = settings.ResponseHeaderTimeoutSeconds
	}
	if timeout <= 0 {
		timeout = 25
	}
	headerWait := time.Duration(timeout) * time.Second
	fetch := func() (forwardEntry, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, headerWait)
		defer cancel()
		entry, err := forwardOnce(attemptCtx, channelHTTPClient(channel), build)
		if err != nil || options.normalize == nil {
			return entry, err
		}
		return options.normalize(attemptCtx, entry)
	}
	ttl := forwardCacheTTL(options, settings)
	if redaction.IsProtectedRequest(ctx) || ttl <= 0 {
		entry, err := fetch()
		return entryUpstream(entry, false), err
	}

	key, err := forwardKey(ctx, channel, build, options)
	if err != nil {
		return Upstream{}, notSent(err)
	}
	entry, cached, err := forwardCached(ctx, options.cacheSubject, key, ttl, fetch)
	if err != nil {
		return Upstream{}, err
	}
	return entryUpstream(entry, cached), nil
}

// forwardThrough answers one request from the shared cache, and otherwise runs
// fetch once on behalf of every caller that arrived with the same key.
//
// It is the path every provider takes, including the one that fetches in
// process and therefore builds no *http.Request: a page read locally is cached
// and merged exactly like a page an upstream was asked for.
func forwardThrough(key string, ttl time.Duration, fetch func() (forwardEntry, error)) (forwardEntry, bool, error) {
	if ttl > 0 {
		if entry, found := lookupForward(key); found {
			return entry, true, nil
		}
	}

	value, err, _ := forwardFlights.Do(key, func() (any, error) {
		if ttl > 0 {
			if entry, found := lookupForward(key); found {
				return entry, nil
			}
		}
		entry, fetchErr := fetch()
		if fetchErr != nil {
			return nil, fetchErr
		}
		// A refusal applies only to this call; a later caller may succeed.
		if ttl > 0 && entry.StatusCode >= 200 && entry.StatusCode < 300 && len(entry.Body) > 0 {
			storeForward(key, entry, ttl)
		}
		return entry, nil
	})
	if err != nil {
		return forwardEntry{}, false, err
	}
	entry, _ := value.(forwardEntry)
	return entry, false, nil
}

// CacheKeyFor builds the key a provider that has no request of its own uses,
// so its answers live in the same cache as the rest without colliding with
// another provider's entry for the same input.
func CacheKeyFor(namespace, input string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + input))
	return hex.EncodeToString(sum[:])
}

// CacheTTL resolves the lifetime a provider that builds no request of its own
// uses, from the same two levels Forward reads.
func CacheTTL(search bool, override int) time.Duration {
	return forwardCacheTTL(ForwardOptions{Search: search, CacheTTLSeconds: override}, websearch_setting.Get())
}

func entryUpstream(entry forwardEntry, cached bool) Upstream {
	return Upstream{
		StatusCode: entry.StatusCode,
		Header:     entry.Header,
		Body:       entry.Body,
		Cached:     cached,
	}
}

func forwardOnce(ctx context.Context, client *http.Client, build func(context.Context) (*http.Request, error)) (forwardEntry, error) {
	req, err := build(ctx)
	if err != nil {
		return forwardEntry{}, notSent(err)
	}
	if err := ctx.Err(); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return forwardEntry{}, notSent(err)
	}

	// The client is shared with other relays. Do not change their redirect policy.
	oneShot := *client
	oneShot.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// The paid providers send POST bodies. Do not give the transport a copy to replay.
	req.GetBody = nil
	resp, err := oneShot.Do(req)
	if err != nil {
		return forwardEntry{}, sentError{err}
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, forwardResponseLimit))
	if readErr != nil {
		return forwardEntry{}, sentError{readErr}
	}
	return forwardEntry{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
		Header:      resp.Header.Clone(),
	}, nil
}

// sentError marks a failure that may already have been billed, so the caller
// stops trying candidates instead of paying twice for one client request.
type sentError struct{ error }

func (e sentError) Unwrap() error { return e.error }

// notSent marks a failure that provably never reached the upstream: a bad
// request built here, missing credentials, or a dial failure.
func notSent(err error) error {
	if err == nil {
		return nil
	}
	return &unsentError{error: err}
}

type unsentError struct{ error }

func (e *unsentError) Unwrap() error { return e.error }

// WasSent reports whether the request may have reached the upstream, which is
// the only question that decides whether another candidate may be tried.
func WasSent(err error) bool {
	if err == nil {
		return false
	}
	var sent sentError
	if errors.As(err, &sent) {
		return true
	}
	var unsent *unsentError
	return !errors.As(err, &unsent)
}

// channelHTTPClient builds the client a channel's proxy and transport settings
// ask for, which is the same client the relay paths use so a channel's timeout
// and proxy settings apply here too.
func channelHTTPClient(channel abstract.ChannelContext) *http.Client {
	client, err := service.GetHttpClientWithProxySettings(channel.Settings.Proxy, channel.Settings)
	if err != nil || client == nil {
		return http.DefaultClient
	}
	return client
}

// forwardCacheTTL resolves the cache lifetime for one request. A channel
// setting wins over the global default, and a negative value on either level
// disables the cache.
func forwardCacheTTL(options ForwardOptions, settings websearch_setting.WebSearchSetting) time.Duration {
	if options.CacheTTLSeconds < 0 {
		return 0
	}
	ttlSeconds := options.CacheTTLSeconds
	if ttlSeconds == 0 {
		ttlSeconds = settings.FetchCacheTTLSeconds
		if options.Search {
			ttlSeconds = settings.SearchCacheTTLSeconds
		}
	}
	if ttlSeconds < 0 {
		return 0
	}
	if ttlSeconds == 0 {
		ttlSeconds = 600
		if !options.Search {
			ttlSeconds = 3600
		}
	}
	return time.Duration(ttlSeconds) * time.Second
}

// effectiveSearchTTL keeps the memory cache's default lifetime in step with the
// configured search TTL, which is the shorter of the two the cache serves.
func effectiveSearchTTL(ttlSeconds int) int {
	if ttlSeconds <= 0 {
		ttlSeconds = websearch_setting.SearchCacheTTL()
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 600
	}
	return ttlSeconds
}

// forwardKey separates the provider from its input, so two providers that
// answer the same query never read each other's entry.
func forwardKey(ctx context.Context, channel abstract.ChannelContext, build func(context.Context) (*http.Request, error), options ForwardOptions) (string, error) {
	req, err := build(ctx)
	if err != nil {
		return "", err
	}
	if req.Body != nil {
		defer req.Body.Close()
	}
	policy, _ := abstract.RequestRoutingPolicy(ctx)
	hash := sha256.New()
	fmt.Fprintf(hash, "http:%d:%d:%s\x00%s\x00%s\x00%s\x00", channel.ChannelType, channel.ChannelID, policy.Group, req.Method, req.URL.String(), options.CacheKey)
	if err := req.Header.Write(hash); err != nil {
		return "", err
	}
	if req.Body != nil && !options.stableBodyKey {
		if _, err := io.Copy(hash, req.Body); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func lookupForward(key string) (forwardEntry, bool) {
	entry, found, err := getForwardCache().Get(key)
	if err != nil {
		logger.LogWarn(context.Background(), "websearch: forward cache read failed: %v", err)
		return forwardEntry{}, false
	}
	return entry, found
}

func storeForward(key string, entry forwardEntry, ttl time.Duration) {
	if err := getForwardCache().SetWithTTL(key, entry, ttl); err != nil {
		logger.LogWarn(context.Background(), "websearch: forward cache write failed: %v", err)
	}
}
