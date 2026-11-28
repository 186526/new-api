package model

import (
	"testing"
	"time"

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
}
