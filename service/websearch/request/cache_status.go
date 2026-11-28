package request

import (
	"context"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/go-redis/redis/v8"
)

const (
	cacheStatusScanLimit  = 128
	cacheStatusEntryLimit = 10000
)

func (scope *cacheGenerations) status(ctx context.Context, client *redis.Client) (CacheStatus, error) {
	status := CacheStatus{Scope: scope.scope, Backend: "memory", Coverage: "local_instance"}
	var targets map[string]string
	if client == nil {
		scope.mu.RLock()
		defer scope.mu.RUnlock()
		status.Generation, targets = scope.global, scope.targets
	} else {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		ctx = ctxWithTimeout
		status.Backend, status.Coverage = "redis", "shared_redis"
		var err error
		targets, err = client.HGetAll(ctx, scope.redisKey()).Result()
		if err != nil {
			return CacheStatus{}, err
		}
		status.Generation = targets["global"]
		delete(targets, "global")
	}
	status.ExactInvalidations = int64(len(targets))
	if err := countCacheEntries(ctx, client, targets, &status); err != nil {
		return CacheStatus{}, err
	}
	return status, nil
}

func countCacheEntries(ctx context.Context, client *redis.Client, targets map[string]string, status *CacheStatus) error {
	namespace := forwardCacheNamespace
	if status.Scope == "redirect" {
		namespace = redirectCacheNamespace
	}
	prefix := namespace + ":g" + status.Generation + ":"
	if client == nil {
		if status.Scope == "redirect" {
			return countMemoryEntries(getRedirectCache(), prefix, targets, status)
		}
		return countMemoryEntries(getForwardCache(), prefix, targets, status)
	}

	seen := make(map[string]struct{})
	var cursor uint64
	for range cacheStatusScanLimit {
		keys, next, err := client.Scan(ctx, cursor, prefix+"*", 256).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			if cacheEntryIsCurrent(key, prefix, targets) {
				seen[key] = struct{}{}
				if len(seen) == cacheStatusEntryLimit {
					status.ValidEntries, status.EntriesTruncated = len(seen), true
					return nil
				}
			}
		}
		if next == 0 {
			status.ValidEntries = len(seen)
			return nil
		}
		cursor = next
	}
	status.ValidEntries, status.EntriesTruncated = len(seen), true
	return nil
}

func countMemoryEntries[V any](cache *cachex.HybridCache[V], prefix string, targets map[string]string, status *CacheStatus) error {
	keys, err := cache.Keys()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if !cacheEntryIsCurrent(key, prefix, targets) {
			continue
		}
		_, found, err := cache.Get(key)
		if err != nil {
			return err
		}
		if found {
			status.ValidEntries++
		}
	}
	return nil
}

func cacheEntryIsCurrent(key, prefix string, targets map[string]string) bool {
	suffix, hasPrefix := strings.CutPrefix(key, prefix)
	if !hasPrefix {
		return false
	}
	target, rest, hasTarget := strings.Cut(suffix, ":")
	exact, digest, hasExact := strings.Cut(rest, ":")
	return hasTarget && hasExact && len(digest) == 64 && exact == targets[target]
}
