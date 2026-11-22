package model

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createReserveTestUser(t *testing.T, quota int) User {
	t.Helper()
	user := User{
		Username:    "reserve-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       quota,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func createReserveTestToken(t *testing.T, remainQuota int) Token {
	t.Helper()
	token := Token{
		UserId:      1,
		Key:         "reserve-token-" + common.GetRandomString(8),
		Name:        "reserve-test",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: remainQuota,
	}
	require.NoError(t, token.Insert())
	return token
}

func getUserQuotaFromDB(t *testing.T, id int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").First(&user, id).Error)
	return user.Quota
}

func getTokenFromDB(t *testing.T, id int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, id).Error)
	return token
}

func resetBatchUpdateTestState(t *testing.T) {
	t.Helper()
	oldBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateInFlight[i] = nil
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchEnabled
		for i := range BatchUpdateTypeCount {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
			batchUpdateInFlight[i] = nil
			batchUpdateLocks[i].Unlock()
		}
	})
}

func TestTryReserveQuotaWithoutRedis(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)

	user := createReserveTestUser(t, 100)
	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))

	token := createReserveTestToken(t, 80)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 25, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reloaded := getTokenFromDB(t, token.Id)
	assert.Equal(t, 55, reloaded.RemainQuota)
	assert.Equal(t, 25, reloaded.UsedQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 56, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 55, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestRedisBatchReserveNeverFallsBackToStaleDatabaseBalance(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 10)
	reserved, err := TryReserveUserQuota(user.Id, 8)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 10, getUserQuotaFromDB(t, user.Id), "batch delta is not flushed yet")

	reserved, err = TryReserveUserQuota(user.Id, 3)
	require.NoError(t, err)
	assert.False(t, reserved, "stale DB balance must not authorize a second spend")
	cachedUser, err := GetUserCache(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 2, cachedUser.Quota)

	token := createReserveTestToken(t, 9)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 3, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 9, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id))
	reloadedToken := getTokenFromDB(t, token.Id)
	assert.Equal(t, 2, reloadedToken.RemainQuota)
	assert.Equal(t, 7, reloadedToken.UsedQuota)
}

func TestBatchUpdateAccumulatesTwoMaximumRequestCharges(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, common.MaxQuota*2+100)
	// The persist call queues the same delta DecreaseUserQuota would, without the
	// gopool goroutine that outlives this test and reads the Redis globals the
	// next test replaces.
	queueCommittedUserQuotaDebit(t, user.Id, common.MaxQuota)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -common.MaxQuota)

	batchUpdate()
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))
}

func TestBatchUpdateAccumulatorSaturatesOverflow(t *testing.T) {
	resetBatchUpdateTestState(t)

	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MaxInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, 1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MaxInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()

	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	batchUpdateStores[BatchUpdateTypeUserQuota] = make(map[int]int)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MinInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, -1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MinInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
}

func TestReserveFallsBackToDatabaseWhenRedisIsUnavailable(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 20)
	require.NoError(t, populateUserCache(user))
	server.Close()

	// Redis 故障时降级为数据库条件更新：服务保持可用且不会超扣。
	reserved, err := TryReserveUserQuota(user.Id, 5)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 15, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 16)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 15, getUserQuotaFromDB(t, user.Id))
}

func TestSynchronousReserveCompensatesCacheWhenPersistenceFails(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, DB.Delete(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 6)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cached, cacheErr := cacheGetUserBase(user.Id)
	require.NoError(t, cacheErr)
	assert.Equal(t, 10, cached.Quota)

	token := createReserveTestToken(t, 12)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&token).Error)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cachedToken, cacheErr := cacheGetTokenByKey(token.Key)
	require.NoError(t, cacheErr)
	assert.Equal(t, 12, cachedToken.RemainQuota)
	assert.Zero(t, cachedToken.UsedQuota)
}

// The cached quota counters live in a Redis hash whose metadata MAC covers
// neither Quota nor the ledger's unsigned counter. These cases hold the reserve
// path to the signed ledger: a writer with Redis access may rewrite any of those
// fields and must not gain spendable quota from it.

// cacheQuotaField reads one hash field and answers "" when the field is absent,
// so a case can assert on a ledger field that is not there.
func cacheQuotaField(t *testing.T, key string, field string) string {
	t.Helper()
	fields, err := common.RDB.HGetAll(t.Context(), key).Result()
	require.NoError(t, err)
	return fields[field]
}

// Property 1: Quota is only a mirror of the signed ledger. A writer that raises
// it without re-signing gains nothing, because the reservation never reads it.
func TestReserveIgnoresAForgedQuotaMirror(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 50)
	require.NoError(t, populateUserCache(user))

	// This is the field the pre-ledger reserve script read and authorized on.
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "Quota", 1_000_000).Err())

	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	assert.False(t, reserved, "the signed total caps the spend at 50")
	assert.Equal(t, 50, getUserQuotaFromDB(t, user.Id), "no database spend either")

	// The read path must not hand the forgery back either: the reservation would
	// refuse it, but the balance is also what the request context and the
	// billing session trust.
	readBack, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 50, readBack, "a forged mirror is not the balance the ledger implies")

	reserved, err = TryReserveUserQuota(user.Id, 50)
	require.NoError(t, err)
	require.True(t, reserved)

	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "Quota", 1_000_000).Err())
	reserved, err = TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	assert.False(t, reserved, "the signed total is exhausted, so the mirror buys nothing")
	readBack, err = GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 0, readBack)
}

// Property 2: the fields a reservation trusts are signed. Raising either of
// them breaks the signature, and a hash whose ledger signature does not hold is
// refused outright. It is indistinguishable from a hash that never carried a
// ledger, and answering either from the database row would hand the row's
// pre-debit balance back. The refusal lasts until the hash expires: the seed that
// republishes a ledger refuses to sign over an existing QuotaLedgerMAC, so a
// forged marker cannot be healed by a read.
func TestReserveRefusesAForgedSignedTotal(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 5)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	require.NoError(t, common.RDB.HSet(t.Context(), key, "QuotaCredited", 1_000_000).Err())

	reserved, err := TryReserveUserQuota(user.Id, 100)
	require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
		"a raised signed total breaks its MAC, and an unsigned ledger is refused rather than answered from the row")
	assert.False(t, reserved)
	assert.Equal(t, 5, getUserQuotaFromDB(t, user.Id), "no database spend either")

	// Reads keep working: the read path answers from the row with the owed delta
	// folded in, so the account stays usable while its spends are refused.
	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 5, balance)

	// The refusal lasts until the hash's own TTL takes it away, and the rebuild
	// then signs the row's balance.
	expireUserHash(t, server, user.Id)

	reserved, err = TryReserveUserQuota(user.Id, 4)
	require.NoError(t, err)
	assert.True(t, reserved, "a rebuilt hash signs the row's balance and the wallet works again")
	assert.Equal(t, 1, getUserQuotaFromDB(t, user.Id))
	assert.Equal(t, "4", cacheQuotaField(t, getUserCacheKey(user.Id), "QuotaSpent"))
}

func TestReserveRefusesAForgedSpentBaseline(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 5)
	require.NoError(t, populateUserCache(user))
	require.True(t, func() bool {
		reserved, err := TryReserveUserQuota(user.Id, 5)
		require.NoError(t, err)
		return reserved
	}())

	// The spent counter is signed: moving it re-opens the signed total unless
	// the writer also re-signs, which they cannot do. The broken signature leaves
	// the hash indistinguishable from one that never carried a ledger, so the
	// reservation refuses instead of answering from the row.
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "QuotaSpent", 0).Err())

	reserved, err := TryReserveUserQuota(user.Id, 5)
	require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
		"a rolled-back counter breaks its MAC, so the hash is refused instead of answered from the row")
	assert.False(t, reserved)
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the wallet records one spend, not two")
}

// Property 3: a writer cannot put the unsigned counter back on its signed
// baseline and spend the same quota twice. Spending the signed total raises the
// baseline to the live counter, so the reset lands below the signature, which
// leaves the hash unsigned and therefore unusable.
func TestReserveRefusesACounterRolledBackToItsBaseline(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 10)
	require.NoError(t, err)
	require.True(t, reserved)
	assert.Equal(t, "10", cacheQuotaField(t, key, "QuotaSpent"),
		"the signed counter is the one the wallet now holds")

	// The attack: put the counter and the mirror back to the pre-spend state.
	require.NoError(t, common.RDB.HSet(t.Context(), key,
		"QuotaSpent", 0, "Quota", 10).Err())
	reserved, err = TryReserveUserQuota(user.Id, 10)
	require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
		"a counter rolled back from its signed value breaks the ledger, so the hash cannot be spent from")
	assert.False(t, reserved)
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the database records the one spend, not two")

	// The forged hash stays refused across a read: the seed will not sign over an
	// existing QuotaLedgerMAC, so nothing hands the pre-spend balance back.
	reserved, err = TryReserveUserQuota(user.Id, 10)
	require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature, "the forged hash is still refused after a read")

	// Only the TTL takes it away, and the rebuild then signs the row, which holds
	// the spend.
	expireUserHash(t, server, user.Id)
	reserved, err = TryReserveUserQuota(user.Id, 10)
	require.NoError(t, err)
	assert.False(t, reserved, "the rebuilt ledger holds the spend, so the rollback leaves nothing to spend")
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id))
}

// Property 4: a copy of the hash ages out of its own signature. Every write
// signs an expiry strictly later than the one already on the hash, and a ledger
// past its signed expiry never authorizes a spend, so replaying a hash that has
// not been used for one ledger window is refused instead of restoring its
// balance.
//
// Residual, stated plainly: an attacker who restores a *recent* whole hash
// (counters, signature and expiry together) presents a ledger nobody can tell
// from the genuine one, and can repeat that inside the window. No bookkeeping
// key inside Redis closes that, because the attacker can roll that key back the
// same way; the signed expiry bounds how long a capture stays usable, it does
// not stop the repetition.
func TestReplayedHashThatAgedOutOfItsWindowIsRefused(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 4)
	require.NoError(t, err)
	require.True(t, reserved)
	assert.Equal(t, 6, getUserQuotaFromDB(t, user.Id))

	// A capture whose signature closed while the wallet moved on. The counters
	// below are the ones the capture held, and the expiry is genuinely signed —
	// just in the past — so the refusal comes from the window, not from a
	// signature that fails to match.
	expired := time.Now().Add(-time.Second).UnixMilli()
	staleMAC, err := (UserBase{
		Id: user.Id, AuthVersion: user.AuthVersion, CacheSchema: userCacheSchemaVersion,
		QuotaCredited: 10, QuotaLedgerExp: expired,
	}).quotaLedgerMAC()
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(t.Context(), key,
		"QuotaCredited", 10, "QuotaSpent", 0, "Quota", 10,
		"QuotaLedgerMAC", staleMAC, "QuotaLedgerExp", expired).Err())

	reserved, err = TryReserveUserQuota(user.Id, 7)
	require.NoError(t, err)
	assert.False(t, reserved, "a copy whose signed window closed does not authorize a spend")
	assert.Equal(t, 6, getUserQuotaFromDB(t, user.Id), "the wallet holds one spend, not two")
	assert.Zero(t, common.RDB.Exists(t.Context(), key).Val(), "the expired ledger is discarded")

	// The republished ledger comes from the database and offers only the six.
	reserved, err = TryReserveUserQuota(user.Id, 7)
	require.NoError(t, err)
	assert.False(t, reserved, "the republished ledger offers only the remaining six")
	assert.Equal(t, 6, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 6)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id))
}

// The expiry check is what refuses a copy whose window closed, so it has to
// stand on its own: a ledger with a correct signature and an expired expiry is
// unverified, and one signed into the future is not.
func TestExpiredLedgerSignatureIsRefused(t *testing.T) {
	userID := 42
	ledger := UserBase{
		Id: userID, AuthVersion: 1, CacheSchema: userCacheSchemaVersion, QuotaCredited: 10,
	}
	sign := func(expiresAt int64) UserBase {
		ledger.QuotaLedgerExp = expiresAt
		mac, err := ledger.quotaLedgerMAC()
		require.NoError(t, err)
		ledger.QuotaLedgerMAC = mac
		return ledger
	}

	expired := sign(time.Now().Add(-time.Second).UnixMilli())
	_, err := expired.verifiedQuotaLedger()
	assert.ErrorIs(t, err, ErrQuotaLedgerUnverified, "an expired signature must not verify")

	live := sign(time.Now().Add(time.Minute).UnixMilli())
	balance, err := live.verifiedQuotaLedger()
	require.NoError(t, err)
	assert.Equal(t, 10, balance)
}

// Every write must sign an expiry strictly later than the one the hash already
// carries, so a copy taken before a write always presents an expiry the live
// hash has moved past. Signing "now + TTL" satisfies monotonicity only while the
// clock moves forward by more than the gap between two writes, so this pins the
// strict-greater rule directly, including the case where the hash already
// carries an expiry in the future.
func TestLedgerExpiryStrictlyAdvances(t *testing.T) {
	now := time.Now().UnixMilli()
	window := quotaLedgerWindowMillis(60)

	assert.Greater(t, nextLedgerExpiry(0, 60), now,
		"a fresh ledger gets a window from now")
	assert.GreaterOrEqual(t, nextLedgerExpiry(0, 60), now+window,
		"the window is counted from now, not shortened by a hash TTL of zero")

	// A hash already signed far enough ahead that now+TTL would land on or before
	// it: a now+TTL expiry would return the same value and two writes would then
	// share an expiry, so a copy taken between them would still verify.
	future := now + 10*window
	assert.Greater(t, nextLedgerExpiry(future, 60), future,
		"the next expiry must be strictly later than the one on the hash")

	// The same rule holds when a test-sized TTL floor kicks in.
	assert.Greater(t, nextLedgerExpiry(future, 1), future)

	// Two consecutive writes never collide.
	first := nextLedgerExpiry(future, 60)
	assert.Greater(t, nextLedgerExpiry(first, 60), first)
}

// The ledger's retry loop must never answer a contended reservation from the
// database, because a database spend the ledger never sees is what made the
// pre-ledger branch charge 170 against a wallet of 100 (M68 P1-1). Contention
// is answered as insufficient, so the total authorized stays inside the wallet.
func TestConcurrentReservesNeverExceedTheSignedLedger(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))

	const attempts = 500
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reserved, err := TryReserveUserQuota(user.Id, 1)
			if err != nil {
				return
			}
			if reserved {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, accepted, 100, "no more unit reservations than the wallet holds")

	// Whatever the burst accepted stays accepted: the ledger must not hand out
	// the same units again once the burst is over.
	for range 200 {
		reserved, err := TryReserveUserQuota(user.Id, 1)
		require.NoError(t, err)
		if !reserved {
			break
		}
		mu.Lock()
		accepted++
		mu.Unlock()
	}
	assert.LessOrEqual(t, accepted, 100, "the wallet is never charged past its balance")

	batchUpdate()
	remaining := getUserQuotaFromDB(t, user.Id)
	assert.GreaterOrEqual(t, remaining, 0, "the wallet never goes negative")
	assert.Equal(t, 100-accepted, remaining)
}

// The same rule on the token ledger.
func TestConcurrentTokenReservesNeverExceedTheSignedLedger(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		accepted int
	)
	for range 500 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reserved, err := TryReserveTokenQuota(token.Id, token.Key, 1, false)
			if err != nil {
				return
			}
			if reserved {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	for range 200 {
		reserved, err := TryReserveTokenQuota(token.Id, token.Key, 1, false)
		require.NoError(t, err)
		if !reserved {
			break
		}
		mu.Lock()
		accepted++
		mu.Unlock()
	}
	assert.LessOrEqual(t, accepted, 100, "no more unit reservations than the token holds")

	batchUpdate()
	reloaded := getTokenFromDB(t, token.Id)
	assert.GreaterOrEqual(t, reloaded.RemainQuota, 0, "the token never goes negative")
}

// A release hands back a reservation this process took and could not persist.
// It must move the unsigned counter back and nothing else: the signed total is
// what caps spending, so a release can never widen it (M68 P1-3).
func TestReleaseCannotWidenTheSignedTotal(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 8)
	require.NoError(t, err)
	require.True(t, reserved)
	assert.Equal(t, "8", cacheQuotaField(t, key, "QuotaSpent"))
	// The commit succeeded here, so the wallet already records the spend. A
	// release is the path taken when it did not, which is why the database is
	// expected to keep this eight.
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id))

	result, err := cacheReleaseUserQuota(user.Id, 8)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)
	assert.Equal(t, "0", cacheQuotaField(t, key, "QuotaSpent"))
	assert.Equal(t, "10", cacheQuotaField(t, key, "QuotaCredited"), "a release is not a credit")
	assert.Equal(t, "10", cacheQuotaField(t, key, "Quota"))

	// The counter is back, but the signed total still caps the wallet at ten.
	reserved, err = TryReserveUserQuota(user.Id, 11)
	require.NoError(t, err)
	assert.False(t, reserved, "the release did not raise the signed total")

	reserved, err = TryReserveUserQuota(user.Id, 10)
	require.NoError(t, err)
	assert.True(t, reserved)
}

// A release bigger than the spend it claims to undo is refused, so it cannot be
// used to push the counter below the baseline it was signed at.
func TestReleaseBeyondTheSpentCounterIsRefused(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 3)
	require.NoError(t, err)
	require.True(t, reserved)

	result, err := cacheReleaseUserQuota(user.Id, 5)
	assert.Equal(t, cacheQuotaInsufficient, result)
	assert.ErrorIs(t, err, errQuotaLedgerBoundsRefused)
	assert.Equal(t, "3", cacheQuotaField(t, key, "QuotaSpent"), "the refused release changed nothing")
}

// The counters travel as decimal strings so the scripts can compare them
// exactly: a Lua number is a double, and one unit disappears at 2^53, the
// ceiling the wallet itself is capped at. A committed spend that carries a
// counter past that point must land on the exact value, or the cached balance
// drifts by a unit and the next reservation authorizes quota the database never
// granted.
func TestLedgerCountersStayExactPastTheDoubleCeiling(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 1)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	// 2^53-1 is odd and exactly representable; the sum below is odd and is not,
	// because every double from 2^53 on has an even significand.
	ceiling := int64(common.MaxWalletQuota)
	for _, delta := range []int64{-ceiling, -2} {
		result, err := cacheApplyUserQuotaDelta(user.Id, delta)
		require.NoError(t, err)
		require.Equal(t, cacheQuotaOK, result)
	}

	assert.Equal(t, "9007199254740993", cacheQuotaField(t, key, "QuotaSpent"),
		"a sum past 2^53 keeps its last digit")
	assert.Equal(t, "-9007199254740992", cacheQuotaField(t, key, "Quota"),
		"the mirror is derived from the counters, so it keeps the digit too")

	// A release works on the same counter and has to stay exact as well.
	result, err := cacheReleaseUserQuota(user.Id, 2)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)
	assert.Equal(t, "9007199254740991", cacheQuotaField(t, key, "QuotaSpent"))
	// credited is still the snapshot's one, so the mirror is one minus the
	// counter: the debt the two deltas recorded, not the released amount.
	assert.Equal(t, "-9007199254740990", cacheQuotaField(t, key, "Quota"))
}

// The token twin of TestReserveRefusesAForgedSignedTotal. Raising a counter the
// signature covers breaks the signature, and a hash that does not carry this
// build's signature is refused outright: it is indistinguishable from one that
// never carried a ledger, and answering either from the database row would hand
// back the pre-debit balance. Measured before the pre-parse refusal existed, this
// case answered reserved=false with no error and discarded the hash, which raised
// the mutation fence and reserved from the row; the refusal now leaves the hash.
func TestTokenReserveRefusesAForgedSignedTotal(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 2)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	require.NoError(t, common.RDB.HSet(t.Context(), getTokenCacheKey(token.Key), "QuotaGranted", 1_000_000).Err())
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 100, false)
	require.ErrorIs(t, err, ErrPreLedgerTokenHashCarriesNoSignature,
		"a raised signed total breaks its MAC, so the hash is refused rather than answered from the row")
	assert.False(t, reserved)
	assert.Equal(t, int64(1), common.RDB.Exists(t.Context(), getTokenCacheKey(token.Key)).Val(),
		"the refusal leaves the hash in place for its own TTL, unlike the discard it replaced")
	assert.Zero(t, common.RDB.Exists(t.Context(), getTokenCacheFenceKey(token.Key)).Val(),
		"no discard runs, so no mutation fence is raised")
	assert.Equal(t, 2, getTokenFromDB(t, token.Id).RemainQuota, "and no row spend either")
}

func TestTokenRepairSeedsTheLedgerFromTheSnapshot(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 40)
	require.NoError(t, token.Update())
	// Update leaves a mutation fence behind, and a fenced publish writes nothing.
	// The seed is what this case is about, so the fence is waited out first.
	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	// Raising the unsigned counters alone does not break the metadata MAC, so
	// the seed must not trust them.
	require.NoError(t, common.RDB.HSet(t.Context(), getTokenCacheKey(token.Key),
		"RemainQuota", 10_000, "UsedQuota", 0).Err())
	// The metadata MAC still verifies, so this read republishes the snapshot's
	// metadata and seeds the ledger the hash no longer carries.
	_, err = GetTokenByKey(token.Key, false)
	require.NoError(t, err)

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 41, false)
	require.NoError(t, err)
	assert.False(t, reserved, "the ledger is seeded from the snapshot, not from the forged counters")
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 40, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 1, false)
	require.NoError(t, err)
	assert.False(t, reserved)
}

func TestTokenCacheInitPreservesLiveQuotaAndFenceBlocksStaleSnapshot(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 100)
	loaded, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	stale := *loaded

	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -70)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	// 已存在的哈希只刷新 TTL：数据库快照不得覆盖已被原子预扣的余额。
	code, err := cacheInitToken(stale)
	require.NoError(t, err)
	assert.Equal(t, 2, code)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 30, cached.RemainQuota)

	// 变更期间：fence 删除缓存并拦截并发读者手中的过期快照。
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	code, err = cacheInitToken(stale)
	require.NoError(t, err)
	assert.Zero(t, code, "the pre-mutation snapshot must not be published while fenced")
	_, err = cacheGetTokenByKey(token.Key)
	assert.Error(t, err)

	// fence 过期后可重新从数据库水合。
	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	fresh, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, fresh.RemainQuota)
	cached, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.RemainQuota)
}

// Property 3 for a partial spend: the attack does not have to exhaust the
// wallet first. Spending part of the balance and then writing the counter back
// must not re-open the quota that was already spent. The rollback leaves the
// ledger unsigned, so the reservation refuses rather than answering from a row
// whose balance the accumulator has already spent for real.
func TestReserveRefusesAPartialSpendRolledBack(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 4)
	require.NoError(t, err)
	require.True(t, reserved)
	assert.Equal(t, 6, getUserQuotaFromDB(t, user.Id))
	spent := cacheQuotaField(t, key, "QuotaSpent")

	// The attack: put the unsigned counter and the mirror back to the pre-spend
	// state, touching nothing that is signed.
	require.NoError(t, common.RDB.HSet(t.Context(), key,
		"QuotaSpent", 0, "Quota", 10).Err())

	reserved, err = TryReserveUserQuota(user.Id, 10)
	require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
		"a counter rolled back from %s breaks the ledger, so the hash cannot be spent from", spent)
	assert.False(t, reserved)
	assert.Equal(t, 6, getUserQuotaFromDB(t, user.Id), "the database records the one spend, not two")

	// The read still answers from the row, so the account stays usable while its
	// spends are refused, and the forged hash stays refused across it.
	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 6, balance)
	reserved, err = TryReserveUserQuota(user.Id, 10)
	require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature, "the forged hash is still refused after a read")

	// Only the TTL takes the forged hash away; the rebuild then signs the row,
	// which holds the spend, so the remaining six is all the wallet offers.
	expireUserHash(t, server, user.Id)
	reserved, err = TryReserveUserQuota(user.Id, 7)
	require.NoError(t, err)
	assert.False(t, reserved, "the rebuilt ledger offers only the remaining six")
	reserved, err = TryReserveUserQuota(user.Id, 6)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id))
}

// An exhausting reservation that cannot be persisted has to be refunded in the
// cache too, on both ledgers. The reservation took the whole balance, so the
// compensation is the only thing that brings the cached balance back to the
// database's.
func TestExhaustingReserveCompensatesCacheWhenPersistenceFails(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, DB.Delete(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 10)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	cached, cacheErr := cacheGetUserBase(user.Id)
	require.NoError(t, cacheErr)
	assert.Equal(t, 10, cached.Quota, "the refunded reservation leaves the cached balance at the database's")

	token := createReserveTestToken(t, 12)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&token).Error)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 12, false)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cachedToken, cacheErr := cacheGetTokenByKey(token.Key)
	require.NoError(t, cacheErr)
	assert.Equal(t, 12, cachedToken.RemainQuota, "the refunded token reservation leaves the cached remainder at the database's")
	assert.Zero(t, cachedToken.UsedQuota)
}

// A credit built against a snapshot the hash has moved past must not publish a
// signature that describes another counter. Between the read that verified the
// ledger and the script that writes it, a committed delta can land; the script
// has to notice and refuse rather than sign a state it never saw.
func TestCreditBuiltOnAStaleSnapshotDoesNotPublishAnotherBaseline(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	ledger, result, err := verifiedUserQuotaLedger(user.Id)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	// The arguments the credit path builds right after that verification.
	const credit = int64(5)
	expiresAt := nextLedgerExpiry(ledger.expiresAt, userCacheTTLSeconds())
	nextMAC, err := (UserBase{
		Id: user.Id, AuthVersion: ledger.authVersion, CacheSchema: ledger.schema,
		QuotaCredited: ledger.credited + credit, QuotaSpent: ledger.spent,
		QuotaLedgerExp: expiresAt,
	}).quotaLedgerMAC()
	require.NoError(t, err)

	// A committed spend lands in that gap, through the real path, so the hash
	// stays internally consistent and only the snapshot the credit was built on
	// is out of date.
	deltaResult, deltaErr := cacheApplyUserQuotaDelta(user.Id, -7)
	require.NoError(t, deltaErr)
	require.Equal(t, cacheQuotaOK, deltaResult)
	assert.Equal(t, "7", cacheQuotaField(t, key, "QuotaSpent"))

	raw, evalErr := evalLedgerScript(userQuotaCreditScript, key, userQuotaLedgerArgs{
		Amount: ledgerDecimal(credit), Id: user.Id, Schema: ledger.schema, AuthVersion: ledger.authVersion,
		Credited: ledgerDecimal(ledger.credited), Spent: ledgerDecimal(ledger.spent),
		NextCredited: ledgerDecimal(ledger.credited + credit), NextSpent: ledgerDecimal(ledger.spent),
		NextExpiry: ledgerDecimal(expiresAt), MAC: ledger.mac, NextMAC: nextMAC,
	})
	require.NoError(t, evalErr)
	assert.NotEqual(t, 1, raw, "a credit that lost the race must not be applied")

	_, result, err = verifiedUserQuotaLedger(user.Id)
	assert.Equal(t, cacheQuotaOK, result,
		"whatever the credit wrote must still verify: %v", err)
}

// A committed delta exists in the database already, so the cache must end up
// holding it. Before this bound the delta shared the reservation's four-attempt
// CAS budget, and losing it left the cache authorizing the same quota twice.
// The committed deltas of one burst must all land. This width (64) is exactly
// the retry budget, so it proves the loop absorbs a burst up to the cap; it does
// not exercise the cap's failure mode. The 512-wide test below is the one that
// does.
func TestCommittedDeltasAllLandInTheCache(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	const width = 64
	user := createReserveTestUser(t, width)
	require.NoError(t, populateUserCache(user))

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		applied int
		failed  []error
	)
	for range width {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The order DecreaseUserQuota uses: commit first, then apply.
			if err := persistUserQuotaDelta(user.Id, -1); err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
				return
			}
			result, err := cacheApplyUserQuotaDelta(user.Id, -1)
			mu.Lock()
			if err == nil && result == cacheQuotaOK {
				applied++
			} else {
				failed = append(failed, err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Empty(t, failed, "no committed delta may be dropped")
	assert.Equal(t, width, applied, "every committed delta reaches the cache")

	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, cached.Quota, "the cache holds the balance the database holds")
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id))
}

// The same on the token ledger.
func TestCommittedTokenDeltasAllLandInTheCache(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	const width = 64
	token := createReserveTestToken(t, width)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		applied int
		failed  []error
	)
	for range width {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := persistTokenQuotaDelta(token.Id, -1); err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
				return
			}
			result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -1)
			mu.Lock()
			if err == nil && result == cacheQuotaOK {
				applied++
			} else {
				failed = append(failed, err)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	assert.Empty(t, failed, "no committed token delta may be dropped")
	assert.Equal(t, width, applied)

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Zero(t, cached.RemainQuota, "the cached remainder follows the database")
	assert.Equal(t, width, cached.UsedQuota)
}

// The batch window the accumulator cannot close: it flushes between the queue
// and the cache write, so the re-apply leaves the cache one debit below the row.
// This pins the invariant the comment on applyUserQuotaDebit states: the drift
// never authorizes a spend, and reseeding from the row heals it.
func TestTheBatchFlushWindowDriftsBelowTheRowAndHeals(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())

	require.NoError(t, persistUserQuotaDelta(user.Id, -10)) // queues the debit
	batchUpdate()                                           // the flush that lands in the window
	_, err := cacheApplyUserQuotaDelta(user.Id, -10)
	require.NoError(t, err)

	cached, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	row := getUserQuotaFromDB(t, user.Id)
	assert.Equal(t, 90, row)
	assert.Equal(t, 80, cached, "the window leaves the cache one debit below the row")

	// The drift refuses rather than authorizes: the row funds 90 but the cache
	// only knows 80.
	reserved, err := TryReserveUserQuota(user.Id, 90)
	require.NoError(t, err)
	assert.False(t, reserved, "the drifted cache never authorizes more than the row holds")

	// Reseeding from the row heals it.
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())
	cached, err = GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, row, cached, "a reseed from the row heals the drift")
}

// In non-batch mode the caller writes the row before handing the delta to the
// cache, and the row then holds the debit. A hash absent at that moment (for
// example because its TTL lapsed) must not be republished and re-debited: doing
// that seeds the ledger from a row that already includes the debit and
// subtracts it a second time, leaving the cache below the row and refusing a
// wallet that is funded. The cache may legitimately carry nothing at that
// instant; the invariant is that the first read equals the row, and a reserve
// of the row's own balance succeeds.
func TestNonBatchDeltaWithNoHashLeavesTheCacheEqualToTheRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = false

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())

	// The order DecreaseUserQuota uses in non-batch mode: the row is written
	// first, then the same delta is handed to the cache.
	require.NoError(t, persistUserQuotaDelta(user.Id, -10))
	result, err := cacheApplyUserQuotaDelta(user.Id, -10)
	require.NoError(t, err, "a delta the row already holds is not an error")
	assert.Equal(t, cacheQuotaOK, result)

	// No hash means nothing to compare yet, so the first read republishes the
	// row and must agree with it.
	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 90, balance)
	assert.Equal(t, getUserQuotaFromDB(t, user.Id), balance, "the cache equals the row, not 10 below it")

	// The wallet is funded at 90; a reserve of its whole row balance must land.
	reserved, err := TryReserveUserQuota(user.Id, 90)
	require.NoError(t, err)
	assert.True(t, reserved, "a wallet the row funds must not be refused as empty")
}

// The token twin of the non-batch case above.
func TestNonBatchTokenDeltaWithNoHashLeavesTheCacheEqualToTheRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = false

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(token.Key)).Err())

	require.NoError(t, persistTokenQuotaDelta(token.Id, -10))
	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -10)
	require.NoError(t, err)
	assert.Equal(t, cacheQuotaOK, result)

	balance, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 90, balance.RemainQuota)
	assert.Equal(t, getTokenFromDB(t, token.Id).RemainQuota, balance.RemainQuota, "the cache equals the row")

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 90, false)
	require.NoError(t, err)
	assert.True(t, reserved, "a token the row funds must not be refused as empty")
}

// The width here is well past the retry budget (64), which is where the previous
// revision's discard fired: at 512 the hash was deleted, 323 of 512 writers were
// told they succeeded while doing nothing, and a reserve of already-spent quota
// was then accepted. Batch mode is on because that is what production runs and
// because it is the case where the database does not hold an unflushed debit, so
// a discarded hash destroys the only copy. Nothing here may be dropped, and no
// writer may be told it succeeded without moving the ledger.
func TestAWideBurstOfCommittedDeltasLeavesNothingBehind(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	const width = 512
	user := createReserveTestUser(t, width)
	require.NoError(t, populateUserCache(user))

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		applied  int
		failed   []error
		silently int
	)
	for range width {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := persistUserQuotaDelta(user.Id, -1); err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
				return
			}
			result, err := cacheApplyUserQuotaDelta(user.Id, -1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && result == cacheQuotaOK:
				applied++
			case err == nil:
				// The failure the review measured: success reported, nothing done.
				silently++
			default:
				failed = append(failed, err)
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, failed, "no committed delta may be dropped or reported as contended")
	assert.Zero(t, silently, "a writer that applied nothing must not report success")
	assert.Equal(t, width, applied, "every committed delta reaches the cache")

	// The hash still exists and holds the spend, and the cache agrees with the
	// database once the accumulator has flushed.
	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, cached.Quota, "the cache holds the balance the database holds")
	batchUpdate()
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "every debit reached the row")

	// The unit the burst spent must not be spendable again.
	reserved, err := TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	assert.False(t, reserved, "the spent wallet authorizes nothing more")
}

// The same at the same width on the token ledger, where the discard had the
// same shape.
func TestAWideBurstOfCommittedTokenDeltasLeavesNothingBehind(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	const width = 512
	token := createReserveTestToken(t, width)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		applied  int
		failed   []error
		silently int
	)
	for range width {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := persistTokenQuotaDelta(token.Id, -1); err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
				return
			}
			result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && result == cacheQuotaOK:
				applied++
			case err == nil:
				silently++
			default:
				failed = append(failed, err)
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, failed, "no committed token delta may be dropped or reported as contended")
	assert.Zero(t, silently, "a writer that applied nothing must not report success")
	assert.Equal(t, width, applied)

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Zero(t, cached.RemainQuota, "the cached remainder follows the database")
	assert.Equal(t, width, cached.UsedQuota)
	batchUpdate()
	assert.Equal(t, 0, getTokenFromDB(t, token.Id).RemainQuota, "every debit reached the row")
}

// Contention is a transient, not an empty wallet: a reservation that loses the
// signature race must never come back as a plain "insufficient".
func TestContendedReservesAreNotReportedAsInsufficient(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	const wallet = int64(1_000_000)
	user := createReserveTestUser(t, int(wallet))
	require.NoError(t, populateUserCache(user))

	const width = 32
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		accepted  int
		silent    int
		contended int
		other     []error
	)
	for range width {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reserved, err := TryReserveUserQuota(user.Id, 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil && errors.Is(err, errQuotaLedgerContended):
				contended++
			case err != nil:
				other = append(other, err)
			case reserved:
				accepted++
			default:
				silent++
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, other, "a contended reserve reports contention, not another error")
	assert.Zero(t, silent, "contention is never answered as an empty wallet")

	// A contended reserve is a transient, so retrying it lands: the wallet has
	// room for every one of them.
	for range contended + width {
		if accepted >= width {
			break
		}
		reserved, err := TryReserveUserQuota(user.Id, 1)
		require.NoError(t, err)
		if reserved {
			accepted++
		}
	}
	assert.Equal(t, width, accepted, "every reserve the wallet can pay for lands once retried")
}

// A schema 2 hash is the layout the deployed build wrote, so the upgrade meets
// it on every pod. It carries its balance only in the unsigned Quota mirror, so
// the first read rebuilds it from the database. Under batch accumulation the row
// lags the spend the accumulator still owes it, and hashing the raw row would
// sign the pre-debit balance: the rebuilt ledger must carry the balance the wallet
// holds. This is the shape the read that precedes every reservation produces, so
// the ledger it leaves behind is the one the reservation then trusts.
func TestLiveSchema2HashIsRebuiltOverTheAccumulatorBalance(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	key := seedLiveSchema2UserHash(t, user, 40)
	// The debit the old pod took out of the mirror and queued; the row has not
	// been flushed, so it still reads 100.
	require.NoError(t, setUnflushedQuotaDelta(user.Id, -60))
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the accumulator has not flushed the debit")

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 40, balance, "the rebuild signs the balance the wallet holds, not the row")

	assert.Equal(t, strconv.Itoa(userCacheSchemaVersion), cacheQuotaField(t, key, "CacheSchema"))
	assert.Equal(t, "40", cacheQuotaField(t, key, "QuotaCredited"),
		"the rebuilt ledger carries the debit the row has not seen")
	assert.Equal(t, "0", cacheQuotaField(t, key, "QuotaSpent"))

	// The ledger the rebuild signed is now the authority: the already-spent 60 is
	// not spendable a second time.
	reserved, err := TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved, "41 is past the balance the ledger now carries")
	reserved, err = TryReserveUserQuota(user.Id, 40)
	require.NoError(t, err)
	assert.True(t, reserved, "40 is exactly what the wallet holds")
}

// Once the accumulator flushes, the row and the mirror agree and the rebuild
// signs the same balance without the fold.
func TestLiveSchema2HashIsRebuiltFromTheFlushedRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	key := seedLiveSchema2UserHash(t, user, 40)
	// The post-flush state: the accumulator wrote the 60-unit debit into the row.
	// The row is written directly because DecreaseUserQuota also fires an
	// asynchronous cache decrement, and this pins the read, not that goroutine.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("quota", 40).Error)

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 40, balance, "the rebuilt ledger carries the flushed spend")
	assert.Equal(t, "40", cacheQuotaField(t, key, "QuotaCredited"),
		"the flushed spend is already in the row, so it is the signed total")

	reserved, err := TryReserveUserQuota(user.Id, 40)
	require.NoError(t, err)
	assert.True(t, reserved, "the flushed wallet can spend what is left")
	reserved, err = TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	assert.False(t, reserved)
}

// The shape the upgrade actually meets: the debit lives in the previous pod's
// memory, so this process's accumulator is empty and the fold in
// quotaBalanceHeldByAccumulator cannot see it. The rebuild then signs the row,
// which still reads the pre-debit balance, and the read hands back quota the old
// pod already spent. That residue is documented in new-api/AGENTS.md and
// mitigated by the drained restart, not fixed here, so this test pins it: it
// keeps a change to seedUserQuotaLedger or the read path from silently moving
// the window. Measured 100 with no error, and a 60-unit reservation accepted.
func TestLiveSchema2HashWithNoLocalAccumulatorReadsTheRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	key := seedLiveSchema2UserHash(t, user, 40)
	// Deliberately no setUnflushedQuotaDelta: the spend was queued in the pod that
	// this process replaced, so the accumulator here holds nothing for this user.
	assert.Equal(t, 0, batchUpdateStores[BatchUpdateTypeUserQuota][user.Id],
		"the accumulator is empty, which is what makes this the cross-process shape")

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 100, balance,
		"with an empty accumulator the rebuild signs the row, so the read returns the pre-debit balance")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the row still holds the pre-debit balance")

	assert.Equal(t, strconv.Itoa(userCacheSchemaVersion), cacheQuotaField(t, key, "CacheSchema"))
	assert.Equal(t, "100", cacheQuotaField(t, key, "QuotaCredited"),
		"the rebuilt ledger signs the row, because this process cannot see the old pod's debit")

	// The residue is spendable: 60 on top of a wallet that really holds 40.
	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	assert.True(t, reserved,
		"the cross-process debit is invisible here, so the reservation is accepted")
	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved, "what the ledger now carries is the 100 it was signed from")
}

// An unsigned schema raise must not inflate the balance a read reports. CacheSchema
// sits inside the metadata MAC, so one HSET invalidates the metadata signature and
// the read rejects the hash and republishes it from the database without deleting
// the key. The balance must still be 90 of 100, not the row's 100. Two things keep
// it there, and this pins the pair: the ledger fields survive the rebuild because
// seedUserQuotaLedger refuses to sign over an existing QuotaLedgerMAC, and the read
// path folds the accumulator's owed delta in when it has to answer from the row
// instead. Measured before the fold existed, the same HSET reported 100 and put 100
// into the request context.
func TestAnUnsignedSchemaRaiseKeepsTheSignedBalance(t *testing.T) {
	for _, raised := range []int{ledgerV2SchemaVersion, userCacheSchemaVersion + 1} {
		t.Run(fmt.Sprintf("CacheSchema set to %d", raised), func(t *testing.T) {
			truncateTables(t)
			resetBatchUpdateTestState(t)
			useUserCacheMiniRedis(t)
			common.BatchUpdateEnabled = true

			user := createReserveTestUser(t, 100)
			require.NoError(t, populateUserCache(user))
			reserved, err := TryReserveUserQuota(user.Id, 10)
			require.NoError(t, err)
			require.True(t, reserved)

			key := getUserCacheKey(user.Id)
			require.NoError(t, common.RDB.HSet(t.Context(), key, "CacheSchema", raised).Err())

			balance, err := GetUserQuota(user.Id, false)
			require.NoError(t, err)
			assert.Equal(t, 90, balance, "the live balance, not the row's 100")

			assert.Equal(t, strconv.Itoa(userCacheSchemaVersion), cacheQuotaField(t, key, "CacheSchema"),
				"the metadata is republished under the current schema")
			assert.Equal(t, "10", cacheQuotaField(t, key, "QuotaSpent"),
				"the ledger the seed refuses to overwrite still holds the spend")

			reserved, err = TryReserveUserQuota(user.Id, 95)
			require.NoError(t, err)
			assert.False(t, reserved, "the wallet holds 90, not the row's 100")
		})
	}
}

// A schema 3 hash is a layout no deployed image ever wrote, so nothing migrates
// it. The version 3 ledger could record a wallet in arrears, which the database
// row never holds: that is the one shape where the two paths answer differently,
// so the signed subtest uses it. A migration would carry the debt across and
// report −20; the rebuild reports the row, which is the same 100 here only
// because nothing was spent yet (see TestLiveSchema2HashIsRebuiltOverTheAccumulatorBalance
// for the shape where the fold makes the rebuild answer the spend). The forged
// subtest covers the hash whose ledger signature verifies nothing, a shape the
// schema comparison already diverts but no other test pins.
func TestSchema3HashIsRebuiltNotMigrated(t *testing.T) {
	// The signed shape: the version 3 ledger agrees with itself and records a
	// wallet in arrears, which a migration would carry across.
	t.Run("signed ledger", func(t *testing.T) {
		truncateTables(t)
		resetBatchUpdateTestState(t)
		useUserCacheMiniRedis(t)
		common.BatchUpdateEnabled = true

		user := createReserveTestUser(t, 100)
		key := getUserCacheKey(user.Id)
		require.NoError(t, populateUserCache(user))

		expiresAt := nextLedgerExpiry(time.Now().UnixMilli(), userCacheTTLSeconds())
		v3MAC, err := authCacheMAC(key, struct {
			Id             int
			AuthVersion    int64
			CacheSchema    int
			QuotaCredited  int64
			QuotaSpentBase int64
			ExpiresAt      int64
		}{user.Id, user.AuthVersion, 3, 30, 0, expiresAt})
		require.NoError(t, err)
		var seeded UserBase
		require.NoError(t, common.RedisHGetObj(key, &seeded))
		seeded.CacheSchema = 3
		metadataMAC, err := seeded.cacheMAC()
		require.NoError(t, err)
		require.NoError(t, common.RDB.HSet(t.Context(), key,
			"CacheSchema", 3, "QuotaCredited", 30, "QuotaSpent", 50, "QuotaSpentBase", 0,
			"Quota", -20, "QuotaLedgerMAC", v3MAC, "QuotaLedgerExp", expiresAt,
			"CacheMAC", metadataMAC, "CacheExpiresAt", seeded.CacheExpiresAt).Err())

		balance, err := GetUserQuota(user.Id, false)
		require.NoError(t, err)
		assert.Equal(t, 100, balance,
			"the hash is rebuilt from the row; a migration would have carried the debt across as -20")
		assert.Equal(t, strconv.Itoa(userCacheSchemaVersion), cacheQuotaField(t, key, "CacheSchema"))
	})

	// The forged shape: the ledger signature verifies nothing, and the counters
	// claim ten times what the row holds, so any path that read them would answer
	// 1000 and any path that fell back to the row would answer 100. A hash with no
	// valid signature is refused rather than answered from either.
	t.Run("forged ledger", func(t *testing.T) {
		truncateTables(t)
		resetBatchUpdateTestState(t)
		useUserCacheMiniRedis(t)

		user := createReserveTestUser(t, 100)
		key := getUserCacheKey(user.Id)
		require.NoError(t, populateUserCache(user))

		expiresAt := nextLedgerExpiry(time.Now().UnixMilli(), userCacheTTLSeconds())
		require.NoError(t, common.RDB.HSet(t.Context(), key,
			"CacheSchema", 3, "QuotaCredited", 1000, "QuotaSpent", 0, "QuotaSpentBase", 0,
			"Quota", 1000, "QuotaLedgerMAC", "not-a-signature", "QuotaLedgerExp", expiresAt).Err())

		balance, err := GetUserQuota(user.Id, false)
		require.NoError(t, err)
		assert.Equal(t, 100, balance, "the read rebuilds from the row, so the forged counters never answer")
		assert.Equal(t, strconv.Itoa(userCacheSchemaVersion), cacheQuotaField(t, key, "CacheSchema"))
		assert.Equal(t, "not-a-signature", cacheQuotaField(t, key, "QuotaLedgerMAC"),
			"the rebuild refuses to sign over an existing marker, so the forged hash stays refused")

		reserved, err := TryReserveUserQuota(user.Id, 101)
		require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
			"a ledger that verifies nothing is refused, not spent from the row")
		assert.False(t, reserved)
		assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "no database spend either")
	})
}

// seedLiveSchema2UserHash rewrites a populated hash into the exact shape the
// deployed build (schema 2) wrote: metadata signed under schema 2, the balance in
// the unsigned Quota mirror and no ledger fields at all.
func seedLiveSchema2UserHash(t *testing.T, user User, mirror int) string {
	t.Helper()
	key := getUserCacheKey(user.Id)
	require.NoError(t, populateUserCache(user))
	var seeded UserBase
	require.NoError(t, common.RedisHGetObj(key, &seeded))
	seeded.CacheSchema = ledgerV2SchemaVersion
	metadataMAC, err := seeded.cacheMAC()
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(t.Context(), key,
		"CacheSchema", ledgerV2SchemaVersion, "Quota", mirror,
		"CacheMAC", metadataMAC, "CacheExpiresAt", seeded.CacheExpiresAt).Err())
	require.NoError(t, common.RDB.HDel(t.Context(), key,
		"QuotaCredited", "QuotaSpent", "QuotaLedgerMAC", "QuotaLedgerExp").Err())
	return key
}

// setUnflushedQuotaDelta queues a user quota delta without writing the row, which
// is the state a batch-accumulating pod holds between a spend and the next flush.
func setUnflushedQuotaDelta(userID int, delta int) error {
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	defer batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	batchUpdateStores[BatchUpdateTypeUserQuota][userID] += delta
	return nil
}

func TestACommittedDeltaWithNoHashIsReportedNotSilentlyAccepted(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	// Batch accumulation is the only mode where an absent hash loses the debit:
	// the row predates it, so the cache is the only place it can live yet.
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())
	require.NoError(t, SetUserAuthVersionFence(user.Id, user.AuthVersion+1))

	result, err := cacheApplyUserQuotaDelta(user.Id, -1)
	assert.Error(t, err, "a delta that cannot be applied is not a success")
	assert.NotEqual(t, cacheQuotaOK, result)
	assert.ErrorIs(t, err, errQuotaLedgerHashGone)
}

// Under batch accumulation the row lags every spend until the next flush. When
// the signed ledger is no longer usable, the row may still answer, but only with
// the accumulator's owed debit folded in: measured before the fix, wallet 100
// with two accumulated spends of 30 accepted a reserve of 100 (the whole
// pre-debit balance) and took the row to 0. The folded comparison answers 40.
func TestBatchExpiredLedgerDoesNotAuthorizeFromTheLaggingRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	reserved, err := TryReserveUserQuota(user.Id, 30)
	require.NoError(t, err)
	require.True(t, reserved)
	reserved, err = TryReserveUserQuota(user.Id, 30)
	require.NoError(t, err)
	require.True(t, reserved)

	cached, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	require.Equal(t, 40, cached, "the ledger holds the live balance")
	require.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the row has not been flushed yet")

	// The signed window closes while the hash itself is still there. The expiry is
	// part of the ledger MAC, so it is re-signed with a past value: the signature
	// matches and only the window has gone, which is what makes this the expiry
	// case rather than a forged one.
	expireSignedUserLedger(t, user.Id)

	reserved, err = TryReserveUserQuota(user.Id, 100)
	require.NoError(t, err, "the row answers, with the owed debit folded in")
	assert.False(t, reserved, "the pre-debit balance is not spendable")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "no row spend either")

	// The balance the accumulator implies is 40, so 40 is spendable and 41 is not.
	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved, "41 is past the implied balance")
	reserved, err = TryReserveUserQuota(user.Id, 40)
	require.NoError(t, err)
	assert.True(t, reserved, "40 is exactly what the wallet holds")

	// After the flush the row holds every spend and the numbers still agree.
	batchUpdate()
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id))
	reserved, err = TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	assert.False(t, reserved, "the flushed row is drained")
}

// The token twin of the case above.
func TestBatchExpiredLedgerDoesNotAuthorizeFromTheLaggingTokenRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 30, false)
	require.NoError(t, err)
	require.True(t, reserved)

	live, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	require.Equal(t, 70, live.RemainQuota)
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota, "the row has not been flushed")

	// The signed window closes while the hash itself is still there. The expiry is
	// part of the ledger MAC, so it is re-signed with a past value: the signature
	// matches and only the window has gone, which is what makes this the expiry
	// case rather than a forged one — and the one case a reservation still answers
	// from the row plus the accumulator's owed delta.
	expireSignedTokenLedger(t, token.Key)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 100, false)
	require.NoError(t, err, "the row answers, with the owed debit folded in")
	assert.False(t, reserved, "the pre-debit balance is not spendable")
	assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 71, false)
	require.NoError(t, err)
	assert.False(t, reserved, "71 is past the implied balance")
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 70, false)
	require.NoError(t, err)
	assert.True(t, reserved, "70 is exactly what the token holds")

	batchUpdate()
	assert.Equal(t, 0, getTokenFromDB(t, token.Id).RemainQuota)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 1, false)
	require.NoError(t, err)
	assert.False(t, reserved, "the flushed row is drained")
}

// A metadata write refreshes the hash but not the ledger, so the hash must not
// be allowed to outlive the signature that makes its counters spendable. The
// TTL is capped at the signed expiry, which keeps the hash alive only as long
// as it can be trusted. Measured before the fix the hash was renewed to a full
// TTL while the ledger had ~5s left.
func TestUserHashNeverOutlivesItsSignedLedger(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	// A TTL longer than the remaining signed window, so a refresh that only
	// touched the hash would outlive its ledger. useUserCacheMiniRedis pins
	// SyncFrequency to 2s, which is shorter than this window and would hide it.
	oldSync := common.SyncFrequency
	common.SyncFrequency = 30
	t.Cleanup(func() { common.SyncFrequency = oldSync })

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))

	// Put the signed expiry 3 seconds out, then refresh the metadata.
	expiresAt := time.Now().Add(3 * time.Second).UnixMilli()
	mac, err := (UserBase{
		Id: user.Id, AuthVersion: user.AuthVersion, CacheSchema: userCacheSchemaVersion,
		QuotaCredited: 100, QuotaSpent: 0, QuotaLedgerExp: expiresAt,
	}).quotaLedgerMAC()
	require.NoError(t, err)
	key := getUserCacheKey(user.Id)
	require.NoError(t, common.RDB.HSet(t.Context(), key,
		"QuotaLedgerExp", expiresAt, "QuotaLedgerMAC", mac).Err())

	var fresh User
	require.NoError(t, DB.First(&fresh, user.Id).Error)
	require.NoError(t, updateUserCache(fresh))

	ttl := common.RDB.PTTL(t.Context(), key).Val()
	remaining := time.Until(time.UnixMilli(expiresAt))
	assert.Positive(t, ttl, "the refresh kept the hash alive")
	assert.LessOrEqual(t, ttl, remaining+time.Second,
		"the hash TTL is capped at the signed expiry, so it cannot outlive its ledger")
}

// A normal security-factor change must leave a live ledger verifiable. The
// authentication version moves on its own: IncrementUserAuthVersionWithTx raises
// the column for a password, 2FA or passkey change, and BumpUserAuthVersion's
// PublishUserAuthCache then re-signs the metadata under the new version without
// touching the ledger. While the ledger MAC signed the version, that write broke
// a perfectly good ledger, and the broken ledger sent every reservation to the
// database row — the pre-debit balance this file exists to stop it spending.
//
// Batch accumulation is what makes it observable: the ten units already spent are
// still queued, so the row reads its pre-debit 100 while the wallet holds 90. A
// 95-unit reservation must be refused for lack of funds. Measured on the parent
// commit, it was accepted and took the row to 5.
func TestLedgerSurvivesAnAuthenticationVersionBump(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 10)
	require.NoError(t, err)
	require.True(t, reserved)
	require.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the spend is queued, not flushed")

	version, err := BumpUserAuthVersion(user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 2, version, "the version really moved")

	var bumped UserBase
	require.NoError(t, common.RedisHGetObj(key, &bumped))
	require.Equal(t, int64(2), bumped.AuthVersion, "and the hash carries the new version")
	require.NotEmpty(t, bumped.QuotaLedgerMAC, "with its ledger still on it")
	balance, ledgerErr := bumped.verifiedQuotaLedger()
	require.NoError(t, ledgerErr, "the ledger still verifies after the bump")
	assert.Equal(t, 90, balance, "and still reports the ten already spent")

	reserved, err = TryReserveUserQuota(user.Id, 95)
	require.NoError(t, err)
	assert.False(t, reserved, "95 must not fit in a wallet holding 90")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "no row was written for a refused reservation")

	reserved, err = TryReserveUserQuota(user.Id, 90)
	require.NoError(t, err)
	assert.True(t, reserved, "the wallet keeps working after a security-factor change")

	// A metadata write that only stamps this build's schema is the same kind of
	// write, and it must leave the ledger alone too.
	refreshed, err := GetUserById(user.Id, false)
	require.NoError(t, err)
	refreshed.Group = "vip"
	require.NoError(t, updateUserCache(*refreshed))
	reserved, err = TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	assert.False(t, reserved, "the wallet is still the ledger's zero, so it cannot spend one more")
}

// No unsigned field may move a pre-ledger hash into the permitting branch. A
// writer holding Redis access and no secret can add a QuotaLedgerMAC marker or
// raise CacheSchema, because cacheMAC zeroes the ledger fields before signing and
// stamps the schema number on every metadata write. Neither field is covered by a
// signature the writer can produce, so neither may decide whether the hash is
// spendable: both shapes stay refused, and the forged hash stays refused until its
// own TTL rebuilds it from the database.
//
// The starting shape is the one the upgrade meets: a live schema-2 hash holding a
// debit the accumulator still owes the row (wallet 100, mirror 40, row 100). If a
// forged field could reach the permitting branch, the reservation would answer
// from the row and spend the already-spent 60 a second time.
func TestNoUnsignedFieldMovesAPreLedgerHashIntoThePermissiveBranch(t *testing.T) {
	t.Run("added ledger marker", func(t *testing.T) {
		truncateTables(t)
		resetBatchUpdateTestState(t)
		server := useUserCacheMiniRedis(t)
		common.BatchUpdateEnabled = true

		user := createReserveTestUser(t, 100)
		key := seedLiveSchema2UserHash(t, user, 40)
		require.NoError(t, setUnflushedQuotaDelta(user.Id, -60))
		// A marker is not a signature: the ledger MAC commits to both counters and
		// the expiry, so a value nobody computed cannot make them verify.
		require.NoError(t, common.RDB.HSet(t.Context(), key, "QuotaLedgerMAC", "x").Err())

		_, err := cacheGetUserBase(user.Id)
		require.Error(t, err, "the read must not answer from the pre-debit row")

		reserved, err := TryReserveUserQuota(user.Id, 60)
		require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
			"a marker on an unsigned ledger is still an unsigned ledger")
		assert.False(t, reserved)
		assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id),
			"the already-spent 60 must not reach the pre-debit row")

		// The refusal outlives a read, because the seed will not sign over the
		// marker the writer left behind.
		_, err = GetUserQuota(user.Id, false)
		require.NoError(t, err)
		reserved, err = TryReserveUserQuota(user.Id, 60)
		require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
			"a read cannot heal a forged marker into a signature")
		assert.False(t, reserved)
		assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))

		// Only the hash's own TTL rebuilds it, and the rebuild signs the balance the
		// accumulator holds, so the spend is still counted.
		expireUserHash(t, server, user.Id)
		balance, err := GetUserQuota(user.Id, false)
		require.NoError(t, err)
		assert.Equal(t, 40, balance, "the rebuilt ledger carries the unflushed 60")
		reserved, err = TryReserveUserQuota(user.Id, 41)
		require.NoError(t, err)
		assert.False(t, reserved, "41 is past the wallet's 40")
	})

	t.Run("added ledger fields with the schema number raised", func(t *testing.T) {
		truncateTables(t)
		resetBatchUpdateTestState(t)
		server := useUserCacheMiniRedis(t)
		common.BatchUpdateEnabled = true

		user := createReserveTestUser(t, 100)
		key := seedLiveSchema2UserHash(t, user, 40)
		require.NoError(t, setUnflushedQuotaDelta(user.Id, -60))
		// Every unsigned field raised at once, in the layout this build writes: a
		// current schema, a signed-looking total and a marker. The signature is what
		// fails, not the shape.
		require.NoError(t, common.RDB.HSet(t.Context(), key,
			"CacheSchema", userCacheSchemaVersion,
			"QuotaCredited", 100, "QuotaSpent", 0,
			"QuotaLedgerMAC", "x").Err())

		_, err := cacheGetUserBase(user.Id)
		require.Error(t, err, "a raised schema and counters do not replace a signature")

		reserved, err := TryReserveUserQuota(user.Id, 60)
		require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature)
		assert.False(t, reserved)
		assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id),
			"the raised fields cannot authorize a spend from the pre-debit row")

		expireUserHash(t, server, user.Id)
		balance, err := GetUserQuota(user.Id, false)
		require.NoError(t, err)
		assert.Equal(t, 40, balance, "the rebuild signs the balance the accumulator holds")
	})
}

// A signed ledger with a field deleted must be refused, not answered from the
// row. requireLedgerInt used to run before the layout decision, so a hash whose
// Id, CacheSchema or AuthVersion had been deleted failed as
// ErrQuotaLedgerUnverified, and reserveUserQuotaCached routes that error to
// discardUnverifiedUserQuotaCache plus reserveUserQuotaFromRow. Under batch
// accumulation that row is the pre-debit balance the guard exists to keep
// unspendable, so deleting one field handed the spend back.
//
// The shape here puts the debit in the signed ledger alone: wallet 100, ledger
// credited 100 and spent 60, row still 100, and this process's accumulator empty
// because the debit sits in another pod. A reservation of 60 against the row
// fits and spends quota the wallet no longer holds.
//
// Measured on the parent commit, four sub-cases failed and three already passed.
// deleted Id, deleted CacheSchema, deleted AuthVersion and zeroed AuthVersion
// each returned reserved=true with a nil error and took the row from 100 to 40,
// the log naming the missing field and saying it was reserving from the database.
// deleted QuotaCredited, deleted QuotaSpent and deleted QuotaLedgerExp passed
// then as they pass now, because the layout question parses exactly those three
// and answered false for them.
func TestReserveRefusesASignedLedgerWithADeletedField(t *testing.T) {
	seedLedgerOnlyDebit := func(t *testing.T) (User, string) {
		t.Helper()
		truncateTables(t)
		resetBatchUpdateTestState(t)
		useUserCacheMiniRedis(t)
		common.BatchUpdateEnabled = true

		user := createReserveTestUser(t, 100)
		require.NoError(t, populateUserCache(user))
		key := getUserCacheKey(user.Id)

		// The debit lives in the signed ledger only: the row still holds the
		// pre-debit 100 and this process's accumulator never saw the spend.
		result, err := cacheApplyUserQuotaDelta(user.Id, -60)
		require.NoError(t, err)
		require.Equal(t, cacheQuotaOK, result)
		require.Equal(t, "60", cacheQuotaField(t, key, "QuotaSpent"))
		require.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the row still holds the pre-debit balance")
		require.Zero(t, unflushedQuotaDelta(user.Id, BatchUpdateTypeUserQuota),
			"the debit is held outside this process's accumulator, which is what makes the row fallback a double spend")
		return user, key
	}

	assertRefused := func(t *testing.T, user User, key string, damage string) {
		t.Helper()
		reserved, err := TryReserveUserQuota(user.Id, 60)
		rowAfter := getUserQuotaFromDB(t, user.Id)
		require.ErrorIs(t, err, ErrPreLedgerHashCarriesNoSignature,
			"a signed ledger %s is refused rather than answered from the row (reserved=%t, row=%d)",
			damage, reserved, rowAfter)
		assert.False(t, reserved)
		assert.Equal(t, 100, rowAfter, "the pre-debit row must not fund the spend")
		assert.Equal(t, int64(1), common.RDB.Exists(t.Context(), key).Val(),
			"the refusal leaves the hash in place for its own TTL to rebuild")
	}

	for _, field := range []string{"Id", "CacheSchema", "AuthVersion", "QuotaCredited", "QuotaSpent", "QuotaLedgerExp"} {
		t.Run("deleted "+field, func(t *testing.T) {
			user, key := seedLedgerOnlyDebit(t)
			require.NoError(t, common.RDB.HDel(t.Context(), key, field).Err())
			assertRefused(t, user, key, "with "+field+" deleted")
		})
	}

	// AuthVersion is outside the ledger MAC, so zeroing it leaves the counters
	// signed; the check that it is positive is all that stands between that hash
	// and the row.
	t.Run("zeroed AuthVersion", func(t *testing.T) {
		user, key := seedLedgerOnlyDebit(t)
		require.NoError(t, common.RDB.HSet(t.Context(), key, "AuthVersion", 0).Err())
		assertRefused(t, user, key, "with AuthVersion zeroed")
	})
}

// The token twin of TestReserveRefusesASignedLedgerWithADeletedField. The token
// ledger signature covers every field the reservation reads — Id, QuotaGranted,
// UsedQuota and the expiry — so damage to any one of them leaves counters that
// are signed and a hash whose layout is not. verifiedTokenQuotaLedger used to
// parse those fields before it asked whether the signature held, so the damage
// came out as ErrQuotaLedgerUnverified and reserveTokenQuotaCached took that
// error's route: discard the hash and reserve from the database row. Under batch
// accumulation that row predates the debit the accumulator still owes it, so
// deleting one field handed the spend back.
//
// The shape here puts the debit in the signed ledger alone: the token holds 100,
// the ledger records 60 used, the row still holds 100, and this process's
// accumulator is empty because the debit sits in another pod. A reservation of 60
// against the row fits and spends quota the token no longer has.
//
// Measured on the parent commit, all five sub-cases failed: each returned
// reserved=true with a nil error, logged the missing field and reserved from the
// database, taking the row from 100 to 40. The pre-ledger sub-case failed the same
// way, because the parent had no refusal to return at all.
func TestTokenReserveRefusesASignedLedgerWithADeletedField(t *testing.T) {
	seedLedgerOnlyTokenDebit := func(t *testing.T) (Token, string) {
		t.Helper()
		truncateTables(t)
		resetBatchUpdateTestState(t)
		useUserCacheMiniRedis(t)
		common.BatchUpdateEnabled = true

		token := createReserveTestToken(t, 100)
		_, err := GetTokenByKey(token.Key, true)
		require.NoError(t, err)
		key := getTokenCacheKey(token.Key)

		// The debit lives in the signed ledger only: the row still holds the
		// pre-debit 100 and this process's accumulator never saw the spend.
		result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -60)
		require.NoError(t, err)
		require.Equal(t, cacheQuotaOK, result)
		require.Equal(t, "60", cacheQuotaField(t, key, "UsedQuota"))
		require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota, "the row still holds the pre-debit balance")
		require.Zero(t, unflushedQuotaDelta(token.Id, BatchUpdateTypeTokenQuota),
			"the debit is held outside this process's accumulator, which is what makes the row fallback a double spend")
		return token, key
	}

	assertRefused := func(t *testing.T, token Token, key string, damage string) {
		t.Helper()
		reserved, err := TryReserveTokenQuota(token.Id, token.Key, 60, false)
		rowAfter := getTokenFromDB(t, token.Id).RemainQuota
		require.ErrorIs(t, err, ErrPreLedgerTokenHashCarriesNoSignature,
			"a signed ledger %s is refused rather than answered from the row (reserved=%t, row=%d)",
			damage, reserved, rowAfter)
		assert.False(t, reserved)
		assert.Equal(t, 100, rowAfter, "the pre-debit row must not fund the spend")
		assert.Equal(t, int64(1), common.RDB.Exists(t.Context(), key).Val(),
			"the refusal leaves the hash in place for its own TTL to rebuild")
	}

	// Every field the signature covers is parsed, so deleting any one of them has
	// to reach the refusal. QuotaLedgerMAC is the signature itself.
	for _, field := range []string{"Id", "QuotaGranted", "UsedQuota", "QuotaLedgerExp", "QuotaLedgerMAC"} {
		t.Run("deleted "+field, func(t *testing.T) {
			token, key := seedLedgerOnlyTokenDebit(t)
			require.NoError(t, common.RDB.HDel(t.Context(), key, field).Err())
			assertRefused(t, token, key, "with "+field+" deleted")
		})
	}
}

// A token hash written before this build signed the token ledger carries no
// ledger fields at all, and the reservation must refuse it rather than discard it
// and answer from the row. The shape is the one a deployed pre-ledger image left
// behind: complete metadata, the RemainQuota mirror, and no QuotaGranted,
// UsedQuota, QuotaLedgerMAC or QuotaLedgerExp. Measured on the parent commit the
// reservation discarded the hash and reserved from the row, which under batch
// accumulation is the pre-debit balance; the refusal leaves the hash for its own
// TTL, after which the next read seeds a ledger from the balance the accumulator
// holds.
func TestTokenReserveRefusesAHashWithNoSignedLedger(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	key := getTokenCacheKey(token.Key)

	require.NoError(t, common.RDB.HDel(t.Context(), key,
		"QuotaGranted", "UsedQuota", "QuotaLedgerMAC", "QuotaLedgerExp").Err())

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 60, false)
	require.ErrorIs(t, err, ErrPreLedgerTokenHashCarriesNoSignature,
		"a token hash with no signed ledger is refused rather than answered from the row")
	assert.False(t, reserved)
	assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota, "no database spend either")
	assert.Equal(t, int64(1), common.RDB.Exists(t.Context(), key).Val(),
		"the refusal leaves the hash for its own TTL, unlike the discard it replaced")

	// The refusal repeats: nothing on the reserve path republishes the hash over
	// the mirrored counters.
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 60, false)
	require.ErrorIs(t, err, ErrPreLedgerTokenHashCarriesNoSignature,
		"the second attempt is refused the same way")
	assert.False(t, reserved)
	assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
}

// The token twin of the user read-path repair. A hash whose ledger signature is
// gone is not spendable, but it is not lost either: the read path reports it
// stale, GetTokenByKey reloads the row and cacheInitToken republishes the hash.
// That republish signs the counter the hash still holds, and the counter is the
// one the seed signs, so the repair keeps the debit instead of handing back the
// row. The shape is the ledger-only one from the refusal test above: the token
// holds 100, the signed ledger records 60 used, the row still holds 100 and this
// process's accumulator is empty because the debit sits in another pod. A repair
// that seeded from the row would hand 60 of spent quota back.
//
// Measured on the parent commit, the reservation after the read failed with
// `cached token hash carries no signed quota ledger` and the hash kept `mac=""`;
// the user twin repaired the same damage on its own read and reserved.
func TestTokenReadRepairsASignedLedgerWithADeletedSignature(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	key := getTokenCacheKey(token.Key)

	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -60)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)
	require.Equal(t, "60", cacheQuotaField(t, key, "UsedQuota"))
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota, "the row still holds the pre-debit balance")
	require.Zero(t, unflushedQuotaDelta(token.Id, BatchUpdateTypeTokenQuota),
		"the debit is held outside this process's accumulator")

	require.NoError(t, common.RDB.HDel(t.Context(), key, "QuotaLedgerMAC").Err())
	_, err = cacheGetTokenByKey(token.Key)
	require.Error(t, err, "a hash with no signed ledger is not answered from its mirror")

	_, err = GetTokenByKey(token.Key, false)
	require.NoError(t, err)

	assert.Equal(t, "60", cacheQuotaField(t, key, "UsedQuota"),
		"the republish kept the counter the hash held rather than the row's")
	assert.Equal(t, "40", cacheQuotaField(t, key, "RemainQuota"),
		"the repaired remainder is the pre-debit balance minus the ledger's spend")
	assert.NotEmpty(t, cacheQuotaField(t, key, "QuotaLedgerMAC"), "the republish signed the ledger")

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 40, cached.RemainQuota)
	assert.Equal(t, 60, cached.UsedQuota)

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 41, false)
	require.NoError(t, err)
	assert.False(t, reserved, "the repaired ledger caps the token at its remaining 40")

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 40, false)
	require.NoError(t, err)
	assert.True(t, reserved, "the next reservation spends from the repaired ledger")
}

// A token row can carry a negative used counter: IncreaseTokenQuota refunds a
// charge by lowering used_quota, so refunding a charge that never reached the row
// leaves it below zero. Such a row is consistent — remain 100 with used -5 means
// the token holds 100 — but tokenQuotaLedgerSeedArgs refused it, cacheInitToken
// turned that into an empty ledger MAC, and the reservation then refused every
// attempt, so the token never became spendable again.
//
// Measured on the parent commit, the published hash carried no QuotaGranted and
// no QuotaLedgerMAC and every reservation failed with
// `cached token hash carries no signed quota ledger`.
func TestTokenWithANegativeUsedCounterIsSpendableAfterARepublish(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 100)
	require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).
		Update("used_quota", -5).Error)

	fresh, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, fresh.RemainQuota)

	key := getTokenCacheKey(token.Key)
	assert.Equal(t, "-5", cacheQuotaField(t, key, "UsedQuota"))
	assert.Equal(t, "95", cacheQuotaField(t, key, "QuotaGranted"),
		"the signed total is remain plus the negative used counter")
	assert.Equal(t, "100", cacheQuotaField(t, key, "RemainQuota"))
	assert.NotEmpty(t, cacheQuotaField(t, key, "QuotaLedgerMAC"),
		"the republish signs the negative counter instead of refusing the row")

	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.RemainQuota)
	assert.Equal(t, -5, cached.UsedQuota)

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 101, false)
	require.NoError(t, err)
	assert.False(t, reserved, "the signed total caps the spend at 95")

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 100, false)
	require.NoError(t, err)
	assert.True(t, reserved, "the negative counter does not make the token unspendable")
}

// A damaged hash answers from its mirror and must not keep being kept alive. The
// repair path calls cacheInitToken on every read of such a hash, and a republish
// that renewed its TTL unconditionally would carry the unspendable hash past the
// row catching up, so the refusal would repeat for as long as traffic arrived.
// Measured before the TTL guard: the hash's TTL went back up on each read, so the
// token stayed unspendable instead of recovering when its own TTL ran out.
func TestDamageTokenHashKeepsItsOwnTTLSoTheRowCanCatchUp(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	key := getTokenCacheKey(token.Key)

	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -60)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	require.NoError(t, common.RDB.HDel(t.Context(), key, "QuotaLedgerMAC", "UsedQuota").Err())
	require.NoError(t, common.RDB.PExpire(t.Context(), key, 2*time.Second).Err())
	ttlBefore := common.RDB.PTTL(t.Context(), key).Val()
	require.Positive(t, ttlBefore)

	server.FastForward(500 * time.Millisecond)
	for range 3 {
		_, err := GetTokenByKey(token.Key, false)
		require.NoError(t, err)
		require.Equal(t, 40, mustReadTokenRemainder(t, token.Key),
			"the read answers from the mirror while the hash is damaged")
	}
	ttlAfter := common.RDB.PTTL(t.Context(), key).Val()
	assert.Less(t, ttlAfter, ttlBefore,
		"a republish that could not sign must leave the TTL alone, so the hash expires (before=%s after=%s)",
		ttlBefore, ttlAfter)
}

func mustReadTokenRemainder(t *testing.T, key string) int {
	t.Helper()
	var cached Token
	require.NoError(t, common.RedisHGetObj(getTokenCacheKey(key), &cached))
	answer, err := tokenFromItsMirror(&cached)
	require.NoError(t, err)
	return answer.RemainQuota
}

// The two row-first callers write the row themselves and then hand the same
// delta to the cache. Measured before the fix under batch accumulation, that
// sequence left cache=60 against row=100 (the debit was applied twice): the
// ledger was rebuilt from a row that already held it and then debited again.
// syncRowCommittedUserQuota never rebuilds, so the cache and the row agree.
func TestRowFirstDebitIsNotCountedTwiceUnderBatchAccumulation(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())

	// PurchaseSubscriptionWithBalance / AdjustUserQuota shape: their transaction
	// writes the row with a direct UPDATE (they do not queue), and only then is
	// the same delta handed to the cache.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", -40)).Error)
	require.NoError(t, syncRowCommittedUserQuota(user.Id, -40))

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 60, balance, "the row already holds the debit, so the cache must not apply it twice")
	assert.Equal(t, getUserQuotaFromDB(t, user.Id), balance)

	// The wallet really holds 60 once the row is flushed, and 100 is refused.
	batchUpdate()
	assert.Equal(t, 60, getUserQuotaFromDB(t, user.Id))
	reserved, err := TryReserveUserQuota(user.Id, 100)
	require.NoError(t, err)
	assert.False(t, reserved, "the flushed row funds 60, not 100")
}

// The same shape for a credit, which is the dangerous direction: counting a
// credit twice would let the user spend quota nobody granted.
func TestRowFirstCreditIsNotCountedTwiceUnderBatchAccumulation(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", 50)).Error)
	require.NoError(t, syncRowCommittedUserQuota(user.Id, 50))

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 150, balance, "the credit is applied once, not twice")

	batchUpdate()
	assert.Equal(t, 150, getUserQuotaFromDB(t, user.Id))
	reserved, err := TryReserveUserQuota(user.Id, 150)
	require.NoError(t, err)
	assert.True(t, reserved, "the row's own balance is spendable")
	reserved, err = TryReserveUserQuota(user.Id, 1)
	require.NoError(t, err)
	assert.False(t, reserved, "the doubled credit is not spendable")
}

// A row-first caller whose hash is absent in non-batch mode: the row is the
// authority, the cache carries nothing until the next read republishes it, and
// that read equals the row. This is the shape r5 established, now reached
// through the row-first entry rather than by flipping the batch flag.
func TestRowFirstDeltaWithNoHashLeavesTheCacheEqualToTheRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = false

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", -40)).Error)
	require.NoError(t, syncRowCommittedUserQuota(user.Id, -40))

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 60, balance)
	assert.Equal(t, 60, getUserQuotaFromDB(t, user.Id))

	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	assert.True(t, reserved, "the row funds 60")
}

// A hash that exists but carries no ledger: the row-first caller must not be
// told the delta landed when there was nothing to land it on, and the value the
// reader sees must still equal the row.
func TestRowFirstDeltaWithAPresentHashThatHasNoLedger(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = false

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, common.RDB.HDel(t.Context(), getUserCacheKey(user.Id),
		"QuotaLedgerMAC", "QuotaLedgerExp").Err())

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", -40)).Error)
	// A present hash whose ledger does not verify is not a rebuild trigger for a
	// row-first caller: the delta is reported rather than silently dropped.
	err := syncRowCommittedUserQuota(user.Id, -40)
	assert.Error(t, err, "a delta that cannot be recorded is not reported as success")

	balance, readErr := GetUserQuota(user.Id, false)
	require.NoError(t, readErr)
	assert.Equal(t, getUserQuotaFromDB(t, user.Id), balance, "the cache equals the row")
}

// A stale hash — one whose signed ledger lags the row by an earlier row-first
// delta — must still take the new delta, and the two converge on the row.
func TestRowFirstDeltaOntoAStaleHashStillConverges(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = false

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))

	// First row-first delta: the ledger is live and moves with the row.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", -20)).Error)
	require.NoError(t, syncRowCommittedUserQuota(user.Id, -20))
	// Second one before any read republished the hash.
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", -30)).Error)
	require.NoError(t, syncRowCommittedUserQuota(user.Id, -30))

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	row := getUserQuotaFromDB(t, user.Id)
	assert.Equal(t, 50, row)
	assert.Equal(t, row, balance, "both deltas landed once each")
}

// The r7 acceptance shape, kept: the folded balance has to hold for every repeat
// of the request, not for one. A retry, a second relay replica or a client that
// resends the same call all arrive as another request, and the first one
// discarded the hash on its way out. Measured before the fix: a retry accepted
// the whole pre-debit balance beyond what the wallet held, leaving the row below
// the spends it already owed.
func TestBatchExpiredLedgerRefusesEveryRepeatOfTheSameReserve(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	for range 2 {
		reserved, err := TryReserveUserQuota(user.Id, 30)
		require.NoError(t, err)
		require.True(t, reserved)
	}
	require.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the accumulator holds both spends")

	expireSignedUserLedger(t, user.Id)

	for attempt := 1; attempt <= 3; attempt++ {
		reserved, err := TryReserveUserQuota(user.Id, 100)
		require.NoError(t, err, "attempt %d must answer, not fail", attempt)
		assert.False(t, reserved, "attempt %d must not spend past the implied balance", attempt)
		assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "attempt %d must not move the row", attempt)
	}
	// The implied balance is 40, so exactly 40 is spendable on every repeat.
	for attempt := 1; attempt <= 3; attempt++ {
		reserved, err := TryReserveUserQuota(user.Id, 41)
		require.NoError(t, err)
		assert.False(t, reserved, "attempt %d cannot spend 41 past the implied 40", attempt)
	}

	batchUpdate()
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
	reserved, err := TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved, "the flushed row funds 40, not 41")
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
	reserved, err = TryReserveUserQuota(user.Id, 40)
	require.NoError(t, err)
	assert.True(t, reserved, "the wallet holds 40 and pays 40")
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id), "a ledger reservation is queued, not written")
	batchUpdate()
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the queued spend lands on the flush")
}

// The token twin: the mutation fence only hides this for ten seconds, and a
// token whose hash was dropped any other way is not covered at all.
func TestBatchExpiredLedgerRefusesEveryRepeatOfTheSameTokenReserve(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 30, false)
	require.NoError(t, err)
	require.True(t, reserved)

	// The signed window closes while the hash is still there, and the expiry is
	// re-signed: only the window may be gone, or the hash would be refused as
	// pre-ledger instead of answered from the row.
	expireSignedTokenLedger(t, token.Key)

	for attempt := 1; attempt <= 3; attempt++ {
		reserved, err := TryReserveTokenQuota(token.Id, token.Key, 71, false)
		require.NoError(t, err, "attempt %d must answer, not fail", attempt)
		assert.False(t, reserved, "attempt %d must not spend past the implied 70", attempt)
		assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota, "attempt %d must not move the row", attempt)
	}

	batchUpdate()
	assert.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 71, false)
	require.NoError(t, err)
	assert.False(t, reserved, "the flushed row funds 70, not 71")
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 70, false)
	require.NoError(t, err)
	assert.True(t, reserved, "the token holds 70 and pays 70")
	batchUpdate()
	assert.Equal(t, 0, getTokenFromDB(t, token.Id).RemainQuota, "the spend lands on the flush")
}

// A hash that is gone rather than expired is a different branch: no signature
// fails, so no discard runs, and the miss used to hydrate straight from the row.
// The PEXPIREAT cap now produces this by design at ledger expiry, and a Redis
// restart produces it unconditionally.
func TestBatchVanishedHashDoesNotAuthorizeFromTheLaggingRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	require.True(t, reserved)
	require.Equal(t, 100, getUserQuotaFromDB(t, user.Id))

	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())

	reserved, err = TryReserveUserQuota(user.Id, 100)
	require.NoError(t, err, "the row answers, with the owed debit folded in")
	assert.False(t, reserved, "a vanished hash cannot make the row the authority")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "no row spend either")

	// The answer is not one-shot: the same miss repeats on every attempt.
	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved, "41 is past the implied 40")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))

	batchUpdate()
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved, "the flushed row funds 40")
	reserved, err = TryReserveUserQuota(user.Id, 40)
	require.NoError(t, err)
	assert.True(t, reserved, "the wallet holds 40 and pays 40")
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id), "a ledger reservation is queued, not written")
	batchUpdate()
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the queued spend lands on the flush")
}

// The token twin of the vanished hash.
func TestBatchVanishedTokenHashDoesNotAuthorizeFromTheLaggingRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 30, false)
	require.NoError(t, err)
	require.True(t, reserved)

	require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(token.Key)).Err())

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 100, false)
	require.NoError(t, err, "the row answers, with the owed debit folded in")
	assert.False(t, reserved, "a vanished hash cannot make the row the authority")
	assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 71, false)
	require.NoError(t, err)
	assert.False(t, reserved, "71 is past the implied 70")
	assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	assert.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
}

// The flush window: batchUpdate swaps the accumulator out before it writes the
// rows, so for the length of one flush the map is empty while the row still lags.
// A guard that only reads the accumulator reads "nothing pending" and authorizes
// the pre-debit balance. The window is driven through the two phases directly
// because the real flush is not interruptible from a test.
func TestBatchFlushWindowDoesNotAuthorizeFromTheLaggingRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	reserved, err := TryReserveUserQuota(user.Id, 60)
	require.NoError(t, err)
	require.True(t, reserved)

	batchUpdateFlushLock.Lock()
	stores, ok := takeBatchUpdateStores()
	require.True(t, ok)
	batchUpdateFlushLock.Unlock()

	// The accumulator is now empty and the row has not been written.
	require.Empty(t, batchUpdateStores[BatchUpdateTypeUserQuota])
	require.Equal(t, 100, getUserQuotaFromDB(t, user.Id))

	require.NoError(t, common.RDB.Del(t.Context(), getUserCacheKey(user.Id)).Err())
	reserved, err = TryReserveUserQuota(user.Id, 100)
	require.NoError(t, err, "a mid-flush row answers with the in-flight debit folded in")
	assert.False(t, reserved, "the in-flight spend still counts")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 41)
	require.NoError(t, err)
	assert.False(t, reserved, "41 is past the 40 the in-flight debit leaves")
	reserved, err = TryReserveUserQuota(user.Id, 40)
	require.NoError(t, err)
	assert.True(t, reserved, "40 is exactly what the wallet holds mid-flush")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the reserve was queued, not written")

	writeBatchUpdateStores(stores)
	clearBatchUpdateInFlight()
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id), "the in-flight spend landed")
	batchUpdate()
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "and the reserve that followed it")
}

// A signed ledger whose window has closed still describes the layout, so no
// signature check fails: only the expiry does. Replacing its balance with the
// row's is what hands back the pre-debit balance. Produced without an attacker:
// a metadata write near expiry refreshes only CacheExpiresAt.
func TestExpiredButGenuinelySignedLedgerDoesNotAnswerFromTheLaggingRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	reserved, err := TryReserveUserQuota(user.Id, 10)
	require.NoError(t, err)
	require.True(t, reserved)

	// Move the signed window into the past and re-sign, so the signature matches.
	expiresAt := time.Now().Add(-time.Second).UnixMilli()
	mac, err := (UserBase{
		Id: user.Id, AuthVersion: user.AuthVersion, CacheSchema: userCacheSchemaVersion,
		QuotaCredited: 100, QuotaSpent: 10, QuotaLedgerExp: expiresAt,
	}).quotaLedgerMAC()
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id),
		"QuotaCredited", 100, "QuotaSpent", 10, "QuotaLedgerExp", expiresAt, "QuotaLedgerMAC", mac).Err())

	reserved, err = TryReserveUserQuota(user.Id, 95)
	require.NoError(t, err, "the row answers with the owed debit folded in")
	assert.False(t, reserved, "a closed window cannot spend from the pre-debit row")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id))

	// The read path must not report 100 either: that value feeds the trust bypass.
	balance, readErr := GetUserQuota(user.Id, false)
	require.NoError(t, readErr)
	assert.Equal(t, 90, balance, "the reader reports the live balance, not the row or the stale ledger")

	batchUpdate()
	assert.Equal(t, 90, getUserQuotaFromDB(t, user.Id))
	reserved, err = TryReserveUserQuota(user.Id, 95)
	require.NoError(t, err)
	assert.False(t, reserved, "the flushed row funds 90")
	reserved, err = TryReserveUserQuota(user.Id, 90)
	require.NoError(t, err)
	assert.True(t, reserved, "the wallet holds 90 and pays 90")
	assert.Equal(t, 90, getUserQuotaFromDB(t, user.Id), "a ledger reservation is queued, not written")
	batchUpdate()
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the queued spend lands on the flush")
}

// A row-first caller hands the committed delta to the ledger. A Redis that
// refuses the script reports a miss with an error attached; treating that as
// success tells the caller the delta landed while nothing moved, and the row
// then lags its own commit. Measured before the fix: a wallet at 100 with the
// row debited to 60 and a Redis error left the caller reading success, after
// which a reserve of 100 was accepted and took the row to -40.
func TestRowFirstDeltaReportsARedisErrorInsteadOfSwallowingIt(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", gorm.Expr("quota + ?", -40)).Error)
	server.SetError("LOADING Redis is loading the dataset in memory")

	err := syncRowCommittedUserQuota(user.Id, -40)
	assert.Error(t, err, "a delta that could not be applied is not reported as success")
}

// The read path under batch accumulation: a ledger that is present but past its
// window must not be replaced by the row. Measured before the fix, wallet 100
// with two accumulated spends of 30 read 100 back instead of 40, and that value
// feeds the trust bypass.
func TestReadAfterLedgerExpiryHoldsTheLiveBalance(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	for range 2 {
		reserved, err := TryReserveUserQuota(user.Id, 30)
		require.NoError(t, err)
		require.True(t, reserved)
	}

	live, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	require.Equal(t, 40, live, "the ledger holds the live balance")

	expireSignedUserLedger(t, user.Id)

	balance, err := GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 40, balance, "the closed window reports the live balance, not the pre-debit row")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the row has not been flushed")

	batchUpdate()
	balance, err = GetUserQuota(user.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 40, balance, "and the flushed row agrees")
}

// pauseNextQuotaUpdate registers a gorm hook that holds the first UPDATE after
// it is called until the returned release function runs, so a test can observe
// what is allowed to happen between a decision and the write it authorizes.
func pauseNextQuotaUpdate(t *testing.T, name string) (entered <-chan struct{}, release func()) {
	t.Helper()
	enteredCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		enteredOnce.Do(func() {
			close(enteredCh)
		})
		<-releaseCh
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCh) })
		DB.Callback().Update().Remove(name)
	})
	return enteredCh, func() { releaseOnce.Do(func() { close(releaseCh) }) }
}

// tryQueueWithin reports whether a delta could be queued into the accumulator
// within window. It calls addNewRecord itself — the exact call
// DecreaseUserQuota and persistUserQuotaDelta make — so a test measures the real
// queuing path with no wrapper and no goroutine that outlives the test.
func tryQueueWithin(kind int, subject int, delta int, window time.Duration) bool {
	queued := make(chan struct{})
	go func() {
		addNewRecord(kind, subject, delta)
		close(queued)
	}()
	select {
	case <-queued:
		return true
	case <-time.After(window):
		return false
	}
}

func unflushedTestDelta(subject int, kind int) int {
	return batchUpdateStores[kind][subject] + batchUpdateInFlight[kind][subject]
}

// expireUserHash advances a miniredis clock past the hash's own TTL, so the next
// read finds no hash and rebuilds it from the database. A ledger write ties the
// key's life to the signed expiry (PEXPIREAT nextExpiry), and that window is at
// least quotaLedgerMinWindowSeconds even when the cache TTL is test-sized, so the
// wait cannot be derived from userCacheTTLSeconds alone.
func expireUserHash(t *testing.T, server *miniredis.Miniredis, userID int) {
	t.Helper()
	ttl := common.RDB.PTTL(t.Context(), getUserCacheKey(userID)).Val()
	require.Positive(t, ttl, "the hash must be alive for its TTL to be waited out")
	server.FastForward(ttl + time.Second)
	require.Zero(t, common.RDB.Exists(t.Context(), getUserCacheKey(userID)).Val(),
		"the hash is gone, which is what makes the next read a rebuild")
}

// expireSignedUserLedger closes the signed window of a live user ledger without
// breaking its signature. The expiry is inside the ledger MAC, so writing a past
// expiry alone leaves a hash whose signature matches nothing; this re-signs the
// counters the hash already carries with an expiry in the past, which is the
// shape a genuine ledger reaches when its window runs out.
func expireSignedUserLedger(t *testing.T, userID int) {
	t.Helper()
	var cached UserBase
	require.NoError(t, common.RedisHGetObj(getUserCacheKey(userID), &cached))
	require.True(t, cached.carriesSignedLedger(), "the hash must carry a live ledger to expire it")

	cached.QuotaLedgerExp = time.Now().Add(-time.Second).UnixMilli()
	mac, err := cached.quotaLedgerMAC()
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(userID),
		"QuotaLedgerExp", cached.QuotaLedgerExp, "QuotaLedgerMAC", mac).Err())

	var reread UserBase
	require.NoError(t, common.RedisHGetObj(getUserCacheKey(userID), &reread))
	require.True(t, reread.carriesSignedLedger(),
		"the re-signed ledger still carries this build's signature, so only its window has closed")
	require.LessOrEqual(t, reread.QuotaLedgerExp, time.Now().UnixMilli(),
		"and that window is in the past")
}

// expireSignedTokenLedger is the token twin of expireSignedUserLedger: it closes
// the signed window of a live token ledger without breaking its signature. The
// expiry is inside the token ledger MAC, so writing a past expiry alone leaves a
// hash whose signature matches nothing; this re-signs the counters the hash
// already carries with an expiry in the past, which is the shape a genuine ledger
// reaches when its window runs out — and the shape a reservation is still
// entitled to answer from the row plus the accumulator's owed delta.
func expireSignedTokenLedger(t *testing.T, key string) {
	t.Helper()
	tokenKey := getTokenCacheKey(key)
	fields := hashFields(t, tokenKey)
	tokenID, err := parseLedgerInt(fields, "Id")
	require.NoError(t, err, "the hash must carry the signed id")
	granted, err := parseLedgerInt(fields, "QuotaGranted")
	require.NoError(t, err, "the hash must carry the signed granted counter")
	used, err := parseLedgerInt(fields, "UsedQuota")
	require.NoError(t, err, "the hash must carry the signed used counter")

	expiresAt := time.Now().Add(-time.Second).UnixMilli()
	mac, err := tokenQuotaLedgerMAC(key, int(tokenID), granted, used, expiresAt)
	require.NoError(t, err)
	require.NoError(t, common.RDB.HSet(t.Context(), tokenKey,
		"QuotaLedgerExp", expiresAt, "QuotaLedgerMAC", mac).Err())

	require.True(t, tokenQuotaLedgerFieldsCarryASignature(hashFields(t, tokenKey), int(tokenID), key),
		"the re-signed ledger still carries this build's signature, so only its window has closed")
}

// hashFields reads one hash whole, for the helpers that re-sign what a hash
// already carries.
func hashFields(t *testing.T, key string) map[string]string {
	t.Helper()
	fields, err := common.RDB.HGetAll(t.Context(), key).Result()
	require.NoError(t, err)
	return fields
}

// queueCommittedUserQuotaDebit and queueCommittedTokenQuotaDebit queue a delta
// the way a committed spend reaches the accumulator, and assert it landed.
//
// They call persistUserQuotaDelta / persistTokenQuotaDelta rather than
// DecreaseUserQuota / DecreaseTokenQuota on purpose. The Decrease wrappers also
// fire a `gopool.Go` cache write, and gopool has no join: that goroutine can
// outlive the test and touch the Redis client and the accumulator global the
// next test replaces, which is a whole-package `-race` failure in the next
// test's name. The persist calls are the same addNewRecord with the same sign
// and no goroutine, so a test that only needs the accumulator to hold the delta
// gets one it can drain.
func queueCommittedUserQuotaDebit(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, persistUserQuotaDelta(id, -quota))
	require.Equal(t, -quota, batchUpdateStores[BatchUpdateTypeUserQuota][id],
		"the spend reaches the accumulator directly, with no goroutine to drain")
}

func queueCommittedUserQuotaCredit(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, persistUserQuotaDelta(id, quota))
	require.Equal(t, quota, batchUpdateStores[BatchUpdateTypeUserQuota][id])
}

func queueCommittedTokenQuotaDebit(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, persistTokenQuotaDelta(id, -quota))
	require.Equal(t, -quota, batchUpdateStores[BatchUpdateTypeTokenQuota][id],
		"the spend reaches the accumulator directly, with no goroutine to drain")
}

func queueCommittedTokenQuotaCredit(t *testing.T, id int, quota int) {
	t.Helper()
	require.NoError(t, persistTokenQuotaDelta(id, quota))
	require.Equal(t, quota, batchUpdateStores[BatchUpdateTypeTokenQuota][id])
}

// The conditional update and the delta it must respect have to be one critical
// section. Queuing a spend takes batchUpdateLocks[kind]; a reservation that lets
// that lock go before it writes can have a committed spend land between the
// balance it decided on and the balance it wrote, and then the wallet pays both.
// Measured before the fix: reserve accepted, spend queued during the paused
// UPDATE, wallet 100 paid 30+100.
//
// The probe queues the spend while the reserve's UPDATE is paused. Every
// goroutine this test starts is joined before it returns, including one that was
// still waiting on the lock when the window closed, so no goroutine of this test
// can touch the accumulator global into the next test.
func TestBatchReserveHoldsTheAccumulatorStillAcrossItsUpdate(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	// The ledger's window has closed, so the reserve has to answer from the row.
	// The expiry is re-signed: only the window may be gone, or the hash would be
	// refused as pre-ledger instead of reaching the row.
	expireSignedUserLedger(t, user.Id)

	entered, release := pauseNextQuotaUpdate(t, "m79:pause-user-reserve")
	defer release()

	var outstanding sync.WaitGroup
	outstanding.Add(1)
	go func() {
		defer outstanding.Done()
		_, _ = TryReserveUserQuota(user.Id, 100)
	}()
	<-entered

	if tryQueueWithin(BatchUpdateTypeUserQuota, user.Id, -30, 150*time.Millisecond) {
		release()
		outstanding.Wait()
		t.Fatal("a committed spend was queued between the balance the reserve decided on and the row it wrote")
	}

	release()
	outstanding.Wait()
	// The queuing attempt above was abandoned while it waited on the lock, so it
	// lands after the reserve's row write; drain it before leaving.
	require.Eventually(t, func() bool {
		batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
		defer batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
		return batchUpdateStores[BatchUpdateTypeUserQuota][user.Id] == -30
	}, 2*time.Second, 5*time.Millisecond, "the spend that waited out the reserve is queued after it")
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the reserve wrote its row")

	batchUpdate()
	assert.Equal(t, -30, getUserQuotaFromDB(t, user.Id), "the spend is ordered after the reserve, not inside it")
}

// The token twin.
func TestBatchTokenReserveHoldsTheAccumulatorStillAcrossItsUpdate(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	// The ledger's window has closed, so the reserve answers from the row. The
	// expiry is re-signed: writing it alone would leave the hash pre-ledger, and
	// that shape is now refused outright instead of reaching the row.
	expireSignedTokenLedger(t, token.Key)

	entered, release := pauseNextQuotaUpdate(t, "m79:pause-token-reserve")
	defer release()

	var outstanding sync.WaitGroup
	outstanding.Add(1)
	go func() {
		defer outstanding.Done()
		_, _ = TryReserveTokenQuota(token.Id, token.Key, 100, false)
	}()
	<-entered

	if tryQueueWithin(BatchUpdateTypeTokenQuota, token.Id, -30, 150*time.Millisecond) {
		release()
		outstanding.Wait()
		t.Fatal("a committed spend was queued between the balance the reserve decided on and the row it wrote")
	}

	release()
	outstanding.Wait()
	require.Eventually(t, func() bool {
		batchUpdateLocks[BatchUpdateTypeTokenQuota].Lock()
		defer batchUpdateLocks[BatchUpdateTypeTokenQuota].Unlock()
		return batchUpdateStores[BatchUpdateTypeTokenQuota][token.Id] == -30
	}, 2*time.Second, 5*time.Millisecond, "the spend that waited out the reserve is queued after it")
	assert.Equal(t, 0, getTokenFromDB(t, token.Id).RemainQuota, "the reserve wrote its row")

	batchUpdate()
	assert.Equal(t, -30, getTokenFromDB(t, token.Id).RemainQuota, "the spend is ordered after the reserve, not inside it")
}

// holdMidFlush writes the row a delta owes and stops before the in-flight
// record is cleared, which is exactly the state batchUpdate passes through
// between writeBatchUpdateStores and clearBatchUpdateInFlight: the row carries
// the delta and the in-flight map still owes it. The caller holds the flush lock
// across this and across the assertion that follows, the way a flush holds it.
func holdMidFlush(t *testing.T, kind int, subject int, delta int, writeRow func()) (release func()) {
	t.Helper()
	batchUpdateFlushLock.Lock()
	var once sync.Once
	// A failing assertion below must not leave the flush lock held: the rest of
	// the package would deadlock on it rather than report this test's failure.
	// The in-flight record this helper wrote is dropped here too, so a failure
	// before the caller flushes cannot leave the delta owed to the next test.
	t.Cleanup(func() {
		batchUpdateLocks[kind].Lock()
		batchUpdateInFlight[kind] = nil
		batchUpdateLocks[kind].Unlock()
		once.Do(batchUpdateFlushLock.Unlock)
	})
	release = func() { once.Do(batchUpdateFlushLock.Unlock) }
	batchUpdateLocks[kind].Lock()
	batchUpdateStores[kind] = map[int]int{subject: delta}
	batchUpdateInFlight[kind] = batchUpdateStores[kind]
	batchUpdateStores[kind] = map[int]int{}
	batchUpdateLocks[kind].Unlock()
	writeRow()
	require.Equal(t, delta, unflushedTestDelta(subject, kind),
		"the in-flight record still owes the delta the row now carries")
	return release
}

// requireBlockedWithin reports whether f does not return within window, and
// returns the channel that closes when it does, so the caller can join it.
func requireBlockedWithin(t *testing.T, name string, window time.Duration, f func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		t.Fatalf("%s returned while the flush had written the row but not cleared its in-flight record", name)
	case <-time.After(window):
	}
	// Bounded on purpose: cleanups run last-in-first-out, so an unbounded wait
	// here could outlive the release that lets f through and hang the package.
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("%s never returned after the test ended", name)
		}
	})
	return done
}

// The flush lock is what stops a reserve from reading the same delta twice.
// batchUpdate swaps the accumulator out, writes the rows, then clears the
// in-flight record; between the write and the clear the row carries the delta
// AND the in-flight map still owes it, so a folded read of row plus owed counts
// it twice. A wallet whose row already reached 150 while the in-flight record
// still holds its +50 therefore reads as 200, and a reserve of 200 would be
// accepted against a wallet that genuinely holds 150.
//
// The swap half of the window needs no lock of its own: takeBatchUpdateStores
// records the in-flight map in the same critical section that empties the
// stores, so a mid-swap read is already right. This test drives the half that
// only the flush lock covers; deleting that lock turns it red.
func TestBatchReserveHoldsTheFlushWindowStillAcrossItsUpdate(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	// Only the window is closed, and the expiry is re-signed so it is: a bare
	// expiry write would make the hash pre-ledger and the reserve would refuse
	// before the row read this test exists to observe.
	expireSignedUserLedger(t, user.Id)

	release := holdMidFlush(t, BatchUpdateTypeUserQuota, user.Id, 50, func() {
		require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).
			Update("quota", gorm.Expr("quota + ?", 50)).Error)
	})
	require.Equal(t, 150, getUserQuotaFromDB(t, user.Id), "the row already holds the credit")

	var reserved bool
	var reserveErr error
	answered := requireBlockedWithin(t, "a reserve", 150*time.Millisecond, func() {
		reserved, reserveErr = TryReserveUserQuota(user.Id, 200)
	})

	clearBatchUpdateInFlight()
	release()

	select {
	case <-answered:
	case <-time.After(2 * time.Second):
		t.Fatal("the reserve did not return after the in-flight record was cleared")
	}
	require.NoError(t, reserveErr)
	assert.False(t, reserved, "the wallet holds 150, so a reserve of 200 is refused")
	assert.Equal(t, 150, getUserQuotaFromDB(t, user.Id), "and the refused reserve moved nothing")

	reserved, reserveErr = TryReserveUserQuota(user.Id, 150)
	require.NoError(t, reserveErr)
	assert.True(t, reserved, "the wallet holds 150 and pays 150")
	assert.Equal(t, 150, getUserQuotaFromDB(t, user.Id), "a ledger reservation is queued, not written")
}

// The token twin of the flush window.
func TestBatchTokenReserveHoldsTheFlushWindowStillAcrossItsUpdate(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	// Only the window is closed, and the expiry is re-signed so it is: a bare
	// expiry write would make the hash pre-ledger and the reserve would refuse
	// before the row read this test exists to observe.
	expireSignedTokenLedger(t, token.Key)

	release := holdMidFlush(t, BatchUpdateTypeTokenQuota, token.Id, 50, func() {
		require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).
			Update("remain_quota", gorm.Expr("remain_quota + ?", 50)).Error)
	})
	require.Equal(t, 150, getTokenFromDB(t, token.Id).RemainQuota)

	var reserved bool
	var reserveErr error
	answered := requireBlockedWithin(t, "a token reserve", 150*time.Millisecond, func() {
		reserved, reserveErr = TryReserveTokenQuota(token.Id, token.Key, 200, false)
	})

	clearBatchUpdateInFlight()
	release()

	select {
	case <-answered:
	case <-time.After(2 * time.Second):
		t.Fatal("the token reserve did not return after the in-flight record was cleared")
	}
	require.NoError(t, reserveErr)
	assert.False(t, reserved, "the token holds 150, so a reserve of 200 is refused")
	assert.Equal(t, 150, getTokenFromDB(t, token.Id).RemainQuota)
}

// A drained wallet whose accumulator holds the spends is still a drained wallet.
// The Redis-error exit used to hand the reservation to the row with no regard
// for the accumulator. Measured before the fix: wallet 100, two spends of 30
// queued, Redis answering LOADING, a reserve of 100 was accepted and the row
// ended at -60.
func TestBatchRedisErrorDoesNotAuthorizeFromTheLaggingRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	queueCommittedUserQuotaDebit(t, user.Id, 30)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -30)
	require.Equal(t, -60, unflushedTestDelta(user.Id, BatchUpdateTypeUserQuota), "the accumulator holds both spends")
	require.Equal(t, 100, getUserQuotaFromDB(t, user.Id))

	server.SetError("LOADING Redis is loading the dataset in memory")

	reserved, err := TryReserveUserQuota(user.Id, 100)
	require.NoError(t, err, "a drained wallet is insufficient, not an internal error")
	assert.False(t, reserved, "40 is left, not 100")
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "a refused reserve does not move the row")

	batchUpdate()
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id))
}

// The token twin of the Redis-error exit.
func TestBatchTokenRedisErrorDoesNotAuthorizeFromTheLaggingRow(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	queueCommittedTokenQuotaDebit(t, token.Id, 30)
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -30)
	require.Equal(t, -60, unflushedTestDelta(token.Id, BatchUpdateTypeTokenQuota), "the accumulator holds both spends")
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)

	server.SetError("LOADING Redis is loading the dataset in memory")

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 100, false)
	require.NoError(t, err)
	assert.False(t, reserved, "40 is left, not 100")
	assert.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	assert.Equal(t, 40, getTokenFromDB(t, token.Id).RemainQuota)
}

// A credit the accumulator has not flushed yet is money the wallet holds. The
// guard used to read any unflushed delta as a debit, refuse, and hand the caller
// an error that `service/billing_session.go` turns into a non-retryable 500.
// Measured before the fix: wallet 100, credit of 50 queued, a reserve of 140 was
// refused and only succeeded after the flush.
//
// The hash is made unverifiable on purpose: it is the state the reserve falls
// back from, and the queued credit is then the only record that the wallet holds
// 150. Without folding it in, the row's 100 refuses a spend the wallet can pay.
func TestBatchQueuedCreditFundsTheReserve(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	require.NoError(t, populateUserCache(user))
	queueCommittedUserQuotaCredit(t, user.Id, 50)
	require.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the credit is queued, not written")
	require.Equal(t, 50, unflushedTestDelta(user.Id, BatchUpdateTypeUserQuota))

	// The ledger's window has closed, so the reserve answers from the row with the
	// accumulator's owed credit folded in. The expiry is re-signed: a bare expiry
	// write would leave the hash pre-ledger, and that shape is refused outright
	// rather than answered from the row.
	expireSignedUserLedger(t, user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 140)
	require.NoError(t, err, "a queued credit funds the reserve; it is not a reason to fail the request")
	assert.True(t, reserved, "the wallet holds 150, so 140 is funded")
	assert.Equal(t, -40, getUserQuotaFromDB(t, user.Id), "the row is written before the credit lands")

	batchUpdate()
	assert.Equal(t, 10, getUserQuotaFromDB(t, user.Id))

	reserved, err = TryReserveUserQuota(user.Id, 11)
	require.NoError(t, err)
	assert.False(t, reserved, "the wallet holds 10")
}

// The token twin, and the production-reachable one: the token ledger runs on the
// proxy with batch accumulation on.
func TestBatchQueuedTokenCreditFundsTheReserve(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	token := createReserveTestToken(t, 100)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	queueCommittedTokenQuotaCredit(t, token.Id, 50)
	require.Equal(t, 100, getTokenFromDB(t, token.Id).RemainQuota)
	require.Equal(t, 50, unflushedTestDelta(token.Id, BatchUpdateTypeTokenQuota))

	// The ledger's window has closed, so the reserve answers from the row with the
	// accumulator's owed credit folded in. A forged field would now be refused
	// outright instead of reaching the row, so the expiry is re-signed.
	expireSignedTokenLedger(t, token.Key)

	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 140, false)
	require.NoError(t, err, "a queued credit funds the reserve; it is not a reason to fail the request")
	assert.True(t, reserved, "the token holds 150, so 140 is funded")
	assert.Equal(t, -40, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	assert.Equal(t, 10, getTokenFromDB(t, token.Id).RemainQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 11, false)
	require.NoError(t, err)
	assert.False(t, reserved, "the token holds 10")
}
