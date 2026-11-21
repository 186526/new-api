package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// userCacheSchemaVersion 4 adds the signed quota ledger described on UserBase.
//
// Which layout is live matters for the deploy, so it is stated plainly. The
// deployed build runs version 2 (`sha-0c28a7adb`, and master `456f9db7e`, both
// `const userCacheSchemaVersion = 2`), and a version 2 hash carries no ledger
// at all: its only record of a spend is the untrusted Quota mirror. Version 3
// exists only on this branch's own commits, so the migration below never runs
// against production. A version 2 hash is rebuilt from the database, and under
// BATCH_UPDATE_ENABLED the row does not hold the unflushed debit, so the first
// read after the bump answers from the pre-debit balance.
//
// That cannot be fixed from the hash: there is no signed spend to carry across.
// The deploy mitigation is therefore a drained restart (stop the pods, let the
// batch accumulator flush, then start the new build), tracked as M80; the
// schema-version question is M81. This migration is only for a version 3 hash
// left behind by an earlier build of this same branch.
const userCacheSchemaVersion = 4

// ledgerV3SchemaVersion is the layout this branch published before the live
// counter was signed. Its MAC covers the baseline rather than the counter.
const ledgerV3SchemaVersion = 3

// UserBase is the cached user snapshot. Quota is a mirror of the signed quota
// ledger, never an authority: a writer holding only Redis access can set the
// mirror to anything. The ledger is
//
//	QuotaCredited  every quota credited into the hash since it was published
//	QuotaSpent     every quota reserved out of it
//
// and Quota = QuotaCredited - QuotaSpent. Both counters are signed, together
// with the identity, the authentication version and the expiry, so a writer can
// move neither counter without the secret. Signing the live counter rather than
// a baseline is what makes a rollback fail: every write signs the exact value it
// leaves behind, so no earlier value of QuotaSpent carries a signature that
// still matches, whether the writer rolls back part of the spend or all of it.
type UserBase struct {
	Id             int    `json:"id"`
	Group          string `json:"group"`
	Email          string `json:"email"`
	Quota          int    `json:"quota"`
	QuotaCredited  int64  `json:"-"`
	QuotaSpent     int64  `json:"-"`
	QuotaLedgerMAC string `json:"-"`
	QuotaLedgerExp int64  `json:"-"`
	Status         int    `json:"status"`
	Role           int    `json:"role"`
	Username       string `json:"username"`
	Setting        string `json:"setting"`
	AuthVersion    int64  `json:"-"`
	CacheSchema    int    `json:"-"`
	CacheExpiresAt int64  `json:"-"`
	CacheMAC       string `json:"-"`
}

// quotaLedgerMAC signs the fields a reservation must trust: the identity, the
// authentication version, both counters and the expiry. Every write advances
// the expiry, so a copy taken earlier always presents one the live hash has
// moved past.
func (user UserBase) quotaLedgerMAC() (string, error) {
	return authCacheMAC(getUserCacheKey(user.Id), struct {
		Id            int
		AuthVersion   int64
		CacheSchema   int
		QuotaCredited int64
		QuotaSpent    int64
		ExpiresAt     int64
	}{user.Id, user.AuthVersion, user.CacheSchema, user.QuotaCredited, user.QuotaSpent, user.QuotaLedgerExp})
}

// verifiedQuotaLedger checks the ledger this hash carries and returns the
// balance it implies. A hash whose ledger is missing, expired, forged or
// self-contradictory is not usable for a spend.
func (user UserBase) verifiedQuotaLedger() (int, error) {
	if user.QuotaLedgerMAC == "" {
		return 0, fmt.Errorf("%w: the hash carries no quota ledger", ErrQuotaLedgerUnverified)
	}
	if user.QuotaLedgerExp <= time.Now().UnixMilli() {
		return 0, fmt.Errorf("%w: the quota ledger signature has expired", ErrQuotaLedgerUnverified)
	}
	mac, err := user.quotaLedgerMAC()
	if err != nil {
		return 0, err
	}
	if !validAuthCacheMAC(mac, user.QuotaLedgerMAC, user.QuotaLedgerExp) {
		return 0, fmt.Errorf("%w: the quota ledger signature does not match", ErrQuotaLedgerUnverified)
	}
	if user.QuotaSpent < 0 {
		return 0, fmt.Errorf("%w: the quota ledger counters contradict each other", ErrQuotaLedgerUnverified)
	}
	// QuotaCredited may be negative and QuotaSpent may exceed it: both describe a
	// wallet in debt, which is a balance it can hold but not spend from. The
	// difference is the balance either way.
	return int(user.QuotaCredited - user.QuotaSpent), nil
}

// migrateV3QuotaLedger re-signs a version 3 hash under the current layout,
// carrying its spend across. This covers only a version 3 hash, which only an
// earlier build of this branch wrote; the deployed build is version 2 and a
// version 2 hash has no signed spend to carry (see userCacheSchemaVersion). The
// live counter is not signed by the v3 MAC, so the migration keeps it only when
// it sits at or above the signed baseline; a value below it cannot have come
// from this ledger and the hash is left to be rebuilt from the database. The
// counter may sit above the signed total, which is the debt a wallet in arrears
// holds.
func migrateV3QuotaLedger(fields map[string]string, cached UserBase) error {
	spentBase, err := strconv.ParseInt(fields["QuotaSpentBase"], 10, 64)
	if err != nil || spentBase < 0 {
		return fmt.Errorf("%w: the version 3 baseline is unreadable", ErrQuotaLedgerUnverified)
	}
	if cached.QuotaSpent < spentBase {
		return fmt.Errorf("%w: the version 3 counter sits below its baseline", ErrQuotaLedgerUnverified)
	}
	expected, err := authCacheMAC(getUserCacheKey(cached.Id), struct {
		Id             int
		AuthVersion    int64
		CacheSchema    int
		QuotaCredited  int64
		QuotaSpentBase int64
		ExpiresAt      int64
	}{cached.Id, cached.AuthVersion, ledgerV3SchemaVersion, cached.QuotaCredited, spentBase, cached.QuotaLedgerExp})
	if err != nil {
		return err
	}
	if !validAuthCacheMAC(expected, fields["QuotaLedgerMAC"], cached.QuotaLedgerExp) {
		return fmt.Errorf("%w: the version 3 signature does not match", ErrQuotaLedgerUnverified)
	}
	expiresAt := nextLedgerExpiry(time.Now().UnixMilli(), userCacheTTLSeconds())
	migrated := cached
	migrated.CacheSchema = userCacheSchemaVersion
	migrated.QuotaLedgerExp = expiresAt
	mac, err := migrated.quotaLedgerMAC()
	if err != nil {
		return err
	}
	// The metadata MAC signs CacheSchema, so the bump has to be signed again as
	// well: leaving the version 3 signature would fail the metadata check on the
	// read after this one, and that read would answer from a database row the
	// spend has not reached yet.
	metadataMAC, err := migrated.cacheMAC()
	if err != nil {
		return err
	}
	const script = `
redis.call('HSET', KEYS[1], 'CacheSchema', ARGV[1],
  'QuotaLedgerMAC', ARGV[2], 'QuotaLedgerExp', ARGV[3], 'Quota', ARGV[4],
  'CacheMAC', ARGV[5])
redis.call('HDEL', KEYS[1], 'QuotaSpentBase')
return 1`
	balance := strconv.FormatInt(cached.QuotaCredited-cached.QuotaSpent, 10)
	return common.RDB.Eval(context.Background(), script, []string{getUserCacheKey(cached.Id)},
		userCacheSchemaVersion, mac, strconv.FormatInt(expiresAt, 10), balance, metadataMAC).Err()
}

// cacheMAC signs everything a reader trusts except Quota and the ledger: both
// move between metadata writes (Quota mirrors the ledger, and every ledger
// write re-signs with its own MAC), so signing them here would invalidate the
// hash on the first spend and the following miss would reload a balance the
// batch accumulator has not flushed yet.
func (user UserBase) cacheMAC() (string, error) {
	user.Quota = 0
	user.QuotaCredited = 0
	user.QuotaSpent = 0
	user.QuotaLedgerMAC = ""
	user.QuotaLedgerExp = 0
	return authCacheMAC(getUserCacheKey(user.Id), struct {
		User           UserBase
		AuthVersion    int64
		CacheSchema    int
		CacheExpiresAt int64
	}{user, user.AuthVersion, user.CacheSchema, user.CacheExpiresAt})
}

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog(common.LogText("failed to unmarshal setting: %s", err.Error()))
		}
	}
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

func userCacheTTLSeconds() int {
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		return 60
	}
	return ttl
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(getUserCacheKey(userId))
}

// populateUserCache publishes the database snapshot with its quota balance and,
// because the database is authoritative at that moment, the ledger that makes
// the balance spendable. A hash that already carries a ledger keeps it: only a
// publish with no ledger before it derives one from the balance.
func populateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	if err := writeUserCache(user.ToBaseUser(), true); err != nil {
		return err
	}
	return seedUserQuotaLedger(user.Id, user.Quota, user.AuthVersion)
}

// updateUserCache refreshes non-quota user cache fields.
// Quota is maintained by atomic quota delta paths and must not be overwritten
// by stale user snapshots from profile/settings updates.
func updateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	return writeUserCache(user.ToBaseUser(), false)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (*UserBase, error) {
	// Try getting from Redis first
	userCache, err := cacheGetUserBase(userId)
	if err == nil {
		return userCache, nil
	}

	// Redis misses and read failures both fall back to the shared database. A
	// version fence newer than the database is the one exception: allowing that
	// snapshot would re-authorize a user while a restrictive update is pending.
	user, err := GetUserById(userId, false)
	if err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		floor, floorErr := getUserAuthVersionFloor(userId)
		if floorErr == nil && floor > user.AuthVersion {
			return nil, ErrUserAuthCachePending
		}
		if err := populateUserCache(*user); err != nil {
			if errors.Is(err, ErrUserAuthCachePending) {
				return nil, err
			}
			common.SysLog(common.LogText("failed to synchronously populate user cache: %s", err.Error()))
		}
	}
	return user.ToBaseUser(), nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	// Try getting from Redis first
	err := common.RedisHGetObj(getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	if userCache.Id != userId || userCache.AuthVersion <= 0 {
		return nil, fmt.Errorf("user cache schema is stale")
	}
	// A version 3 hash was published by this branch before the live counter was
	// signed. It carries a ledger whose spend has not necessarily reached the
	// database, so it is migrated instead of discarded: discarding it would
	// answer from a row that predates the spend and hand the quota back.
	if userCache.CacheSchema == ledgerV3SchemaVersion {
		// The baseline the version 3 signature covers has no field on UserBase
		// any more, so it is read from the raw hash.
		fields, fieldErr := common.RDB.HGetAll(context.Background(), getUserCacheKey(userId)).Result()
		if fieldErr != nil {
			return nil, fieldErr
		}
		if err := migrateV3QuotaLedger(fields, userCache); err != nil {
			return nil, err
		}
		// The migration re-signed the hash, so the snapshot in hand no longer
		// describes it. Reading it back once is cheaper than reconstructing the
		// two signed fields here.
		if err := common.RedisHGetObj(getUserCacheKey(userId), &userCache); err != nil {
			return nil, err
		}
	} else if userCache.CacheSchema != userCacheSchemaVersion {
		return nil, fmt.Errorf("user cache schema is stale")
	}
	mac, err := userCache.cacheMAC()
	if err != nil || !validAuthCacheMAC(mac, userCache.CacheMAC, userCache.CacheExpiresAt) {
		return nil, fmt.Errorf("user cache integrity check failed")
	}
	// Quota is outside the metadata MAC, so the stored value is only a mirror.
	// A hash carrying a valid ledger reports the balance the ledger implies;
	// one that carries none or contradicts its signature reports the balance the
	// database holds, never the mirror.
	balance, ledgerErr := userCache.verifiedQuotaLedger()
	if ledgerErr != nil {
		user, err := GetUserById(userId, false)
		if err != nil {
			return nil, err
		}
		balance = user.Quota
	}
	userCache.Quota = balance
	floor, err := getUserAuthVersionFloor(userId)
	if err != nil {
		return nil, err
	}
	if floor > userCache.AuthVersion {
		return nil, ErrUserAuthCachePending
	}
	return &userCache, nil
}

// Add atomic quota operations using hash fields.
// 通过守卫式 Lua 脚本执行：哈希不存在时直接跳过（下次读取会从数据库水合），
// 不会像裸 HINCRBY 那样创建只含 Quota 字段的残缺哈希。
func cacheIncrUserQuota(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	_, err := cacheApplyUserQuotaDelta(userId, delta)
	return err
}

func cacheDecrUserQuota(userId int, delta int64) error {
	return cacheIncrUserQuota(userId, -delta)
}

// syncCreditUserQuotaCache 在授信事务（充值/兑换等）提交后同步把增量补进缓存
// 余额。预扣以缓存值为准（存在期间），授信不能绕过它，否则新到账的额度在
// 缓存过期前不可用；缓存未命中无需处理，下次读取会从已提交的数据库余额水合。
func syncCreditUserQuotaCache(userId int, quota int, operation string) {
	if quota <= 0 {
		return
	}
	if err := cacheIncrUserQuota(userId, int64(quota)); err != nil {
		common.SysLog(common.LogText("failed to sync %s credit to user quota cache: %s", operation, err.Error()))
	}
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

// getUserQuotaCache answers the wallet balance from the cache, which reports the
// signed ledger's balance rather than the Quota mirror (see cacheGetUserBase).
func getUserQuotaCache(userId int) (int, error) {
	if RegionEnabled() {
		return regionLeaseAvailable(RegionSubjectUser, strconv.Itoa(userId))
	}
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Quota, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// RefreshUserGroupCache publishes current user metadata after a group change.
func RefreshUserGroupCache(userId int) error {
	return PublishUserAuthCache(userId)
}

// GetUserLanguage returns the user's language preference from cache
// Uses the existing GetUserCache mechanism for efficiency
func GetUserLanguage(userId int) string {
	userCache, err := GetUserCache(userId)
	if err != nil {
		return ""
	}
	return userCache.GetSetting().Language
}
