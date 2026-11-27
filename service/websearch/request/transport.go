package request

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/websearch/abstract"
	"github.com/QuantumNous/new-api/setting/websearch_setting"

	"github.com/samber/hot"
	"golang.org/x/sync/singleflight"
)

const (
	forwardCacheNamespace = "websearch:forward:v1"
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
}

// Forward sends one request to one provider and returns its answer. It is the
// only place the retry, cache and size policies live, so every requester behaves
// the same way under a refusing upstream.
//
// A retryable refusal (408, 429, 5xx) is retried up to the configured attempt
// count, and the last answer is returned rather than an error: the client then
// reads the upstream's own status code through status_code_mapping, which is
// what it read before this package existed. Only a transport failure that
// prevented the request from being answered at all becomes an error, and that
// error reports WasSent false so the caller may try another candidate.
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
	attempts := settings.RetryAttempts
	if attempts <= 0 {
		attempts = 1
	}
	baseWait := time.Duration(settings.RetryBaseDelayMilliseconds) * time.Millisecond
	if baseWait <= 0 {
		baseWait = 500 * time.Millisecond
	}

	ttl := forwardCacheTTL(options, settings)
	key := forwardKey(build, options)

	entry, cached, err := forwardThrough(key, ttl, func() (forwardEntry, error) {
		return forwardUncached(ctx, channel, build, headerWait, attempts, baseWait)
	})
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
		// A refusal is not cached: the retry budget belongs to this request, and
		// caching a 429 would hand the same refusal to a caller that would have
		// succeeded a second later.
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

// forwardUncached runs the attempt loop. Each attempt gets its own request
// because an http.Request cannot be replayed once its body was read.
func forwardUncached(ctx context.Context, channel abstract.ChannelContext, build func(context.Context) (*http.Request, error), timeout time.Duration, attempts int, baseWait time.Duration) (forwardEntry, error) {
	client := channelHTTPClient(channel)

	var (
		lastEntry forwardEntry
		lastErr   error
		wait      = baseWait
	)
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return forwardEntry{}, ctx.Err()
			case <-time.After(wait):
			}
			wait *= 2
		}

		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		entry, retryable, err := forwardOnce(attemptCtx, client, build)
		cancel()

		if err == nil {
			return entry, nil
		}
		if !retryable {
			// The request reached the upstream and was refused in a way that
			// repeating it would only bill again. Hand the refusal back as an
			// answer so the handler keeps the upstream's status code.
			if entry.StatusCode != 0 {
				return entry, nil
			}
			return forwardEntry{}, err
		}
		lastEntry, lastErr = entry, err
	}
	if lastEntry.StatusCode != 0 {
		// The retry budget is spent. The client reads the last upstream answer,
		// not a local error, so status_code_mapping still applies.
		return lastEntry, nil
	}
	return forwardEntry{}, notSent(lastErr)
}

// forwardOnce sends one attempt. It reports whether the failure is retryable:
// a refusal that a repeat cannot fix is not.
func forwardOnce(ctx context.Context, client *http.Client, build func(context.Context) (*http.Request, error)) (forwardEntry, bool, error) {
	req, err := build(ctx)
	if err != nil {
		return forwardEntry{}, false, err
	}

	resp, err := client.Do(req)
	if err != nil {
		var timeoutErr interface{ Timeout() bool }
		if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			return forwardEntry{}, true, err
		}
		// A transport failure may or may not have reached the upstream, so this
		// one is retried: the loop's last answer wins, and a body-less failure is
		// reported as not sent.
		return forwardEntry{}, true, err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, forwardResponseLimit))
	entry := forwardEntry{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
		Header:      resp.Header.Clone(),
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if readErr != nil {
			return forwardEntry{}, true, readErr
		}
		return entry, false, nil
	}
	return entry, retryableStatus(resp.StatusCode), fmt.Errorf(
		"websearch: upstream answered %d: %s",
		resp.StatusCode, truncateBody(body),
	)
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
	var unsent *unsentError
	if errors.As(err, &unsent) {
		return false
	}
	var sent sentError
	if errors.As(err, &sent) {
		return true
	}
	// A failure without a marker is treated as sent: refusing to retry costs one
	// refused request, retrying a billed one costs money twice.
	return true
}

func retryableStatus(status int) bool {
	switch {
	case status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		return true
	case status >= 500 && status <= 599:
		return true
	default:
		return false
	}
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
func forwardKey(build func(context.Context) (*http.Request, error), options ForwardOptions) string {
	target := ""
	if req, err := build(context.Background()); err == nil && req != nil {
		target = req.Method + "\x00" + req.URL.String()
	}
	sum := sha256.Sum256([]byte(target + "\x00" + options.CacheKey))
	return hex.EncodeToString(sum[:])
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

func truncateBody(body []byte) string {
	const limit = 300
	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
