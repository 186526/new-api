package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestTokenRedactionConflictLeavesAllFieldsUnchanged(t *testing.T) {
	truncateTables(t)
	token := createReserveTestToken(t, 100)
	stale := token
	token.Name = "new name"
	require.NoError(t, token.UpdateWithRedaction(true))
	stale.Name = "stale name"
	stale.RedactionEnabled = true
	require.ErrorIs(t, stale.UpdateWithRedaction(true), ErrTokenRedactionConflict)
	stored, err := GetTokenByIds(token.Id, token.UserId)
	require.NoError(t, err)
	require.Equal(t, "new name", stored.Name)
	require.EqualValues(t, 1, stored.RedactionRevision)
	require.False(t, stored.RedactionEnabled)
}

func TestTokenCachePolicyMutationPreservesSignedQuota(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)
	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -70)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	const policy = `{"strategy":"typed_placeholder"}`
	require.NoError(t, token.StoreRedactionConfig([]byte(policy)))
	token.RedactionEnabled = true
	require.NoError(t, token.UpdateWithRedaction(true))

	fenced, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.Equal(t, 30, fenced.RemainQuota)
	require.Equal(t, 70, fenced.UsedQuota)
	require.True(t, fenced.RedactionEnabled)
	decoded, err := fenced.RedactionConfigBytes()
	require.NoError(t, err)
	require.Equal(t, policy, string(decoded))
	require.NotContains(t, fenced.RedactionConfig, policy)

	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	_, err = GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	require.Equal(t, 30, cached.RemainQuota)
	require.Equal(t, fenced.RedactionConfig, cached.RedactionConfig)
	require.True(t, cached.RedactionEnabled)

	require.NoError(t, common.RDB.HDel(context.Background(), getTokenCacheKey(token.Key), "RedactionEnabled", "RedactionConfig").Err())
	_, err = cacheGetTokenByKey(token.Key)
	require.ErrorIs(t, err, errTokenCacheIntegrity)
}

func TestTokenCacheLegacyMetadataMAC(t *testing.T) {
	useUserCacheMiniRedis(t)
	ctx := context.Background()
	emptyIPs := ""
	token := Token{Id: 42, UserId: 9, Key: "legacy-token-cache-test", AutoGroups: "default", AllowIps: &emptyIPs, RemainQuota: 100}
	cacheKey := getTokenCacheKey(token.Key)
	_, err := cacheInitToken(token)
	require.NoError(t, err)

	var cached Token
	require.NoError(t, common.RedisHGetObj(cacheKey, &cached))
	// The metadata payload and normalization used by f77225142.
	legacy := cached
	legacy.Key, legacy.AllowIps = "", nil
	legacy.RemainQuota, legacy.UsedQuota, legacy.AccessedTime = 0, 0, 0
	legacyMAC, err := authCacheMAC(cacheKey, struct {
		Token          Token
		AutoGroups     string
		CacheExpiresAt int64
	}{legacy, legacy.AutoGroups, legacy.CacheExpiresAt})
	require.NoError(t, err)
	require.Equal(t, legacyMAC, cached.CacheMAC)

	require.NoError(t, common.RDB.HDel(ctx, cacheKey, "RedactionEnabled", "RedactionConfig").Err())
	read, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	require.Equal(t, 100, read.RemainQuota)
	for _, field := range []struct{ name, value string }{
		{"RedactionEnabled", "true"},
		{"RedactionConfig", "unsigned-policy"},
	} {
		require.NoError(t, common.RDB.HSet(ctx, cacheKey, field.name, field.value).Err())
		_, err := cacheGetTokenByKey(token.Key)
		require.ErrorIs(t, err, errTokenCacheIntegrity)
		require.NoError(t, common.RDB.HDel(ctx, cacheKey, field.name).Err())
	}
}
