package model

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// userCacheSchemaVersion 4 adds the signed quota ledger described on UserBase;
// a hash written under an earlier version carries none (or an older layout) and
// is reloaded from the database.
const userCacheSchemaVersion = 4

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
	if userCache.Id != userId || userCache.CacheSchema != userCacheSchemaVersion || userCache.AuthVersion <= 0 {
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
