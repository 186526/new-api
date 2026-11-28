package request

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestToolsAdminCacheGenerations(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		scope := &cacheGenerations{scope: "forward"}
		assertToolsCacheGenerations(t, scope, nil, scope, nil)
	})
	t.Run("shared redis", func(t *testing.T) {
		server := miniredis.RunT(t)
		reader := redis.NewClient(&redis.Options{Addr: server.Addr()})
		writer := redis.NewClient(&redis.Options{Addr: server.Addr()})
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		assertToolsCacheGenerations(t, &cacheGenerations{scope: "forward"}, reader, &cacheGenerations{scope: "forward"}, writer)
	})
}

func assertToolsCacheGenerations(t *testing.T, reader *cacheGenerations, readClient *redis.Client, writer *cacheGenerations, writeClient *redis.Client) {
	t.Helper()
	ctx := t.Context()
	subject := forwardSubject(" query ", "")
	other := forwardSubject("other", "")
	key := func(subject, key string) string {
		value, err := reader.key(ctx, readClient, subject, key)
		require.NoError(t, err)
		return value
	}
	first := key(subject, "channel A")
	second := key(subject, "channel B")
	unaffected := key(other, "channel A")
	store := func(key string) {
		if writeClient == nil {
			storeForward(key, forwardEntry{StatusCode: 200, Body: []byte("cached")}, time.Minute)
			return
		}
		require.NoError(t, writeClient.Set(ctx, forwardCacheNamespace+":"+key, "cached", time.Minute).Err())
	}
	if writeClient == nil {
		require.NoError(t, writer.invalidate(ctx, nil, ""))
		first, second, unaffected = key(subject, "channel A"), key(subject, "channel B"), key(other, "channel A")
	}
	store(second)
	store(unaffected)
	initial, err := reader.status(ctx, readClient)
	require.NoError(t, err)
	require.Equal(t, 2, initial.ValidEntries)
	require.NoError(t, writer.invalidate(ctx, writeClient, subject))
	store(first) // An in-flight response captured before the invalidation.
	require.NotEqual(t, first, key(subject, "channel A"))
	require.NotEqual(t, second, key(subject, "channel B"))
	require.Equal(t, unaffected, key(other, "channel A"))
	status, err := reader.status(ctx, readClient)
	require.NoError(t, err)
	require.EqualValues(t, 1, status.ExactInvalidations)
	require.Equal(t, 1, status.ValidEntries)
	require.Equal(t, initial.Generation, status.Generation)
	require.NoError(t, writer.invalidate(ctx, writeClient, ""))
	require.NotEqual(t, first, key(subject, "channel A"))
	require.NotEqual(t, unaffected, key(other, "channel A"))
	status, err = reader.status(ctx, readClient)
	require.NoError(t, err)
	require.Zero(t, status.ExactInvalidations)
	require.Zero(t, status.ValidEntries)
	require.NotEqual(t, initial.Generation, status.Generation)
}

func TestToolsAdminCacheSelection(t *testing.T) {
	forward := &cacheGenerations{scope: "forward"}
	redirect := &cacheGenerations{scope: "redirect"}
	subject, err := (CacheSelection{Query: "  keywords  "}).target(forward)
	require.NoError(t, err)
	require.Equal(t, forwardSubject("keywords", ""), subject)
	_, err = (CacheSelection{Query: "keywords"}).target(redirect)
	require.ErrorIs(t, err, ErrInvalidCacheSelection)
	_, err = (CacheSelection{Query: "keywords", URL: "https://example.com"}).target(forward)
	require.ErrorIs(t, err, ErrInvalidCacheSelection)
}
