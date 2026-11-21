package model

import (
	"math"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
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
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchEnabled
		for i := range BatchUpdateTypeCount {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
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
	require.NoError(t, DecreaseUserQuota(user.Id, common.MaxQuota, false))
	require.NoError(t, DecreaseUserQuota(user.Id, common.MaxQuota, false))

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
// them breaks the signature, and a hash that fails verification never
// authorizes a spend: it is discarded and answered from the database.
func TestReserveRefusesAForgedSignedTotal(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 5)
	require.NoError(t, populateUserCache(user))

	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "QuotaCredited", 1_000_000).Err())

	reserved, err := TryReserveUserQuota(user.Id, 100)
	require.NoError(t, err)
	assert.False(t, reserved, "a raised signed total breaks its MAC and is answered from the database")
	assert.Equal(t, 5, getUserQuotaFromDB(t, user.Id))
	assert.Zero(t, common.RDB.Exists(t.Context(), getUserCacheKey(user.Id)).Val(), "the unverified hash is discarded")

	// The next reservation republishes the ledger from the database balance.
	reserved, err = TryReserveUserQuota(user.Id, 4)
	require.NoError(t, err)
	assert.True(t, reserved)
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

	// QuotaSpentBase is signed: moving it re-opens the signed total unless the
	// writer also re-signs, which they cannot do.
	require.NoError(t, common.RDB.HSet(t.Context(), getUserCacheKey(user.Id), "QuotaSpentBase", 0).Err())

	reserved, err := TryReserveUserQuota(user.Id, 5)
	require.NoError(t, err)
	assert.False(t, reserved, "a lowered baseline breaks its MAC and cannot re-open the total")
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the wallet records one spend, not two")
	assert.Zero(t, common.RDB.Exists(t.Context(), getUserCacheKey(user.Id)).Val(), "the unverified hash is discarded")
}

// Property 3: a writer cannot put the unsigned counter back on its signed
// baseline and spend the same quota twice. Spending the signed total raises the
// baseline to the live counter, so the reset lands below the signature.
func TestReserveRefusesACounterRolledBackToItsBaseline(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	key := getUserCacheKey(user.Id)

	reserved, err := TryReserveUserQuota(user.Id, 10)
	require.NoError(t, err)
	require.True(t, reserved)
	assert.Equal(t, "10", cacheQuotaField(t, key, "QuotaSpentBase"),
		"spending the signed total raises the baseline to the live counter")

	// The attack: put the counter and the mirror back to the pre-spend state.
	require.NoError(t, common.RDB.HSet(t.Context(), key,
		"QuotaSpent", 0, "Quota", 10).Err())
	reserved, err = TryReserveUserQuota(user.Id, 10)
	require.NoError(t, err)
	assert.False(t, reserved, "a counter rolled back under its baseline cannot be spent again")
	assert.Equal(t, 0, getUserQuotaFromDB(t, user.Id), "the database records the one spend, not two")
	assert.Zero(t, common.RDB.Exists(t.Context(), key).Val(),
		"the contradicted ledger is discarded, so the reset cannot leave a spendable hash behind")
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
		"QuotaCredited", 10, "QuotaSpent", 0, "QuotaSpentBase", 0, "Quota", 10,
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

func TestTokenReserveRefusesAForgedSignedTotal(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 2)
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	require.NoError(t, common.RDB.HSet(t.Context(), getTokenCacheKey(token.Key), "QuotaGranted", 1_000_000).Err())
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 100, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Zero(t, common.RDB.Exists(t.Context(), getTokenCacheKey(token.Key)).Val(),
		"the unverified token hash is discarded")
	assert.Equal(t, int64(1), common.RDB.Exists(t.Context(), getTokenCacheFenceKey(token.Key)).Val(),
		"the discard raises the mutation fence so the forged hash is not republished")
	assert.Equal(t, 2, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestTokenRepairSeedsTheLedgerFromTheSnapshot(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 40)
	require.NoError(t, token.Update())
	_, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	// Raising the unsigned counters alone does not break the metadata MAC, so
	// the seed must not trust them.
	require.NoError(t, common.RDB.HSet(t.Context(), getTokenCacheKey(token.Key),
		"RemainQuota", 10_000, "UsedQuota", 0).Err())
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
