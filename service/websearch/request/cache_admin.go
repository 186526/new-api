package request

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

const maxCacheInvalidationTargets = 4096

var (
	ErrCacheInvalidationCapacity = errors.New("too many exact invalidations; clear the cache scope before retrying")
	ErrInvalidCacheSelection     = errors.New("invalid cache scope or target")
	forwardGenerations           = cacheGenerations{scope: "forward"}
	redirectGenerations          = cacheGenerations{scope: "redirect"}
)

type CacheSelection struct {
	Scope string `json:"scope"`
	Query string `json:"query"`
	URL   string `json:"url"`
}

type CacheStatus struct {
	Scope              string `json:"scope"`
	Backend            string `json:"backend"`
	Coverage           string `json:"coverage"`
	ExactInvalidations int64  `json:"exact_invalidations"`
	Generation         string `json:"generation"`
	ValidEntries       int    `json:"valid_entries"`
	EntriesTruncated   bool   `json:"entries_truncated"`
}

type cacheGenerations struct {
	scope   string
	mu      sync.RWMutex
	global  string
	targets map[string]string
}

func (scope *cacheGenerations) redisKey() string {
	return "websearch:generations:v1:" + scope.scope
}

func cacheRedis() *redis.Client {
	if common.RedisEnabled {
		return common.RDB
	}
	return nil
}

func scopeForCache(name string) (*cacheGenerations, error) {
	switch name {
	case "forward":
		return &forwardGenerations, nil
	case "redirect":
		return &redirectGenerations, nil
	default:
		return nil, ErrInvalidCacheSelection
	}
}

func forwardSubject(query, target string) string {
	if target != "" {
		return CacheKeyFor("url", strings.TrimSpace(target))
	}
	if query != "" {
		return CacheKeyFor("query", strings.TrimSpace(query))
	}
	return ""
}

func (selection CacheSelection) target(scope *cacheGenerations) (string, error) {
	if len(selection.Query) > 8192 || len(selection.URL) > 8192 || (selection.URL != "" && selection.Query != "") || (scope.scope == "redirect" && selection.Query != "") {
		return "", ErrInvalidCacheSelection
	}
	if selection.URL != "" {
		target := strings.TrimSpace(selection.URL)
		parsed, err := url.Parse(target)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return "", ErrInvalidCacheSelection
		}
		if scope.scope == "redirect" {
			return redirectCacheKey(target), nil
		}
		return forwardSubject("", target), nil
	}
	if selection.Query != "" && strings.TrimSpace(selection.Query) == "" {
		return "", ErrInvalidCacheSelection
	}
	return forwardSubject(selection.Query, ""), nil
}

func WebSearchCacheStatus(ctx context.Context, name string) (CacheStatus, error) {
	scope, err := scopeForCache(name)
	if err != nil {
		return CacheStatus{}, err
	}
	client := cacheRedis()
	return scope.status(ctx, client)
}

func InvalidateWebSearchCache(ctx context.Context, selection CacheSelection) (CacheStatus, error) {
	scope, err := scopeForCache(selection.Scope)
	if err != nil {
		return CacheStatus{}, err
	}
	target, err := selection.target(scope)
	if err != nil {
		return CacheStatus{}, err
	}
	client := cacheRedis()
	if err := scope.invalidate(ctx, client, target); err != nil {
		return CacheStatus{}, err
	}
	return scope.status(ctx, client)
}

var advanceCacheGeneration = redis.NewScript(`
local name, next, capacity = ARGV[1], ARGV[2], tonumber(ARGV[3])
if name == '' then
  redis.call('DEL', KEYS[1])
  redis.call('HSET', KEYS[1], 'global', next)
elseif redis.call('HEXISTS', KEYS[1], name) == 1 or
  redis.call('HLEN', KEYS[1]) - redis.call('HEXISTS', KEYS[1], 'global') < capacity then
  redis.call('HSET', KEYS[1], name, next)
else
  return 0
end
return 1
`)

func (scope *cacheGenerations) invalidate(ctx context.Context, client *redis.Client, target string) error {
	generation := uuid.NewString()
	if client != nil {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		changed, err := advanceCacheGeneration.Run(ctx, client, []string{scope.redisKey()}, target, generation, maxCacheInvalidationTargets).Int64()
		if err != nil {
			return err
		}
		if changed == 0 {
			return ErrCacheInvalidationCapacity
		}
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if target == "" {
		scope.global = generation
		scope.targets = nil
		return nil
	}
	if _, exists := scope.targets[target]; !exists && len(scope.targets) >= maxCacheInvalidationTargets {
		return ErrCacheInvalidationCapacity
	}
	if scope.targets == nil {
		scope.targets = make(map[string]string)
	}
	scope.targets[target] = generation
	return nil
}

// Capture before sending so responses that finish after eviction can never
// repopulate the active generation. A Redis outage bypasses cache and merging.
func (scope *cacheGenerations) key(ctx context.Context, client *redis.Client, target, key string) (string, error) {
	var global, exact string
	if client == nil {
		scope.mu.RLock()
		global, exact = scope.global, scope.targets[target]
		scope.mu.RUnlock()
	} else {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		values, err := client.HMGet(ctx, scope.redisKey(), "global", target).Result()
		if err != nil {
			return "", err
		}
		global, _ = values[0].(string)
		exact, _ = values[1].(string)
	}
	return "g" + global + ":" + target + ":" + exact + ":" + CacheKeyFor(scope.scope, key), nil
}
