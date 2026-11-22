package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func (token Token) cacheMAC() (string, error) {
	key := getTokenCacheKey(token.Key)
	token.Key = ""
	token.RemainQuota = 0
	token.UsedQuota = 0
	token.AccessedTime = 0
	if token.AllowIps != nil && *token.AllowIps == "" {
		token.AllowIps = nil
	}
	return authCacheMAC(key, struct {
		Token          Token
		AutoGroups     string
		CacheExpiresAt int64
	}{token, token.AutoGroups, token.CacheExpiresAt})
}

func getTokenCacheKey(key string) string {
	return fmt.Sprintf("token:%s", common.GenerateHMAC(key))
}

// The fence uses the cache key as its hash tag so cacheInitToken's script
// reads both keys from one slot on sharded Redis.
func getTokenCacheFenceKey(key string) string {
	return fmt.Sprintf("token:fence:{%s}", getTokenCacheKey(key))
}

func tokenCacheTTLSeconds() int {
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		return 60
	}
	return ttl
}

// tokenCacheFenceSeconds must outlive a token mutation's database write plus
// any in-flight reader's DB-read-to-cache-init gap. The fence is not deleted
// after commit; it expires naturally so a reader holding a pre-mutation
// snapshot cannot publish it right after the mutation cleared the cache.
// While the fence exists readers simply serve the database without caching.
const tokenCacheFenceSeconds = 10

// invalidateTokenCacheForMutation is called before a token metadata mutation
// writes to the database: it raises the fence and drops the cached hash so no
// reader can act on (or re-publish) the pre-mutation state.
func invalidateTokenCacheForMutation(key string) error {
	if !common.RedisEnabled || key == "" {
		return nil
	}
	ctx := context.Background()
	err := common.RDB.Set(ctx, getTokenCacheFenceKey(key), 1, time.Duration(tokenCacheFenceSeconds)*time.Second).Err()
	if err != nil {
		return err
	}
	return common.RDB.Del(ctx, getTokenCacheKey(key)).Err()
}

// errTokenCacheNeedsRepair reports a token hash that carries no ledger signature
// this build produced. GetTokenByKey answers it from the database row and
// republishes the hash, as GetUserCache does for "user cache schema is stale".
// The caller re-reads the cache afterwards and answers from the signature the
// republish produced, because the row's own balance is the pre-debit one under
// batch accumulation.
var errTokenCacheNeedsRepair = errors.New("token cache carries no signed quota ledger")

// cacheInitToken publishes database metadata when the hash is cold or fails
// verification. Existing quota counters survive repairs because pre-consume
// can update Redis before the database catches up; counters that are no
// longer integers are replaced from the snapshot.
//
// The quota ledger is seeded from the snapshot, never from the counters already
// in the hash: the metadata MAC covers neither RemainQuota nor UsedQuota, so a
// writer with Redis access can raise them without breaking it, and seeding from
// them would sign the forgery. A hash that predates the ledger therefore gets
// its counters reset to the snapshot once. A hash whose live counter is already
// above the snapshot is signed at that counter, and the seed script only runs
// while the hash still holds it, so an unflushed batch spend is neither lost nor
// signed under a value nobody verified.
//
// The seed only runs on a hash that carries no QuotaLedgerMAC at all. A hash that
// still carries its MAC but lost a counter is not republished: the seed would
// have to sign a counter it cannot see, and the only value left to sign is the
// snapshot's pre-debit one. Such a hash keeps the mirror answer for the rest of
// its TTL and the reservation refuses it (ErrDamagedTokenQuotaLedger).
//
// A negative UsedQuota is signed, not refused. IncreaseTokenQuota refunds a
// charge by lowering used_quota, so refunding one that never reached the row
// leaves a funded token with a counter below zero; refusing to seed it published
// a hash with no ledger, which every reservation refused until the hash expired
// and the next republish refused it the same way.
//
// The counters are compared as decimal strings and the remainder is derived
// with HINCRBY, because a sum near the wallet ceiling loses its last digit when
// Lua holds it as a double.
// Returns 0 when fenced, 1 after initialization, or 2 for an existing hash.
func cacheInitToken(token Token) (int, error) {
	if !common.RedisEnabled {
		return 0, nil
	}
	allowIps := ""
	if token.AllowIps != nil {
		allowIps = *token.AllowIps
	}
	cacheKey := getTokenCacheKey(token.Key)
	var existing Token
	readErr := common.RedisHGetObj(cacheKey, &existing)
	if readErr != nil {
		// Decoding stops at the first unreadable field, before CacheMAC.
		existing.CacheMAC, _ = common.RDB.HGet(context.Background(), cacheKey, "CacheMAC").Result()
	}
	existing.Key = token.Key
	existingMAC, macErr := existing.cacheMAC()
	repair := readErr != nil || macErr != nil || !validAuthCacheMAC(existingMAC, existing.CacheMAC, existing.CacheExpiresAt)
	token.CacheExpiresAt = time.Now().Add(time.Duration(tokenCacheTTLSeconds()) * time.Second).UnixMilli()
	mac, err := token.cacheMAC()
	if err != nil {
		return 0, err
	}
	granted := int64(token.RemainQuota) + int64(token.UsedQuota)
	// The used counter the seed signs is the one the hash already carries when
	// it holds an unflushed spend, because the signature has to cover the value
	// the script leaves behind. Reading it here and re-checking it in the script
	// keeps a seed from signing a counter it did not see.
	seedUsed := int64(token.UsedQuota)
	if liveUsed, liveErr := common.RDB.HGet(context.Background(), cacheKey, "UsedQuota").Result(); liveErr == nil {
		if value, parseErr := strconv.ParseInt(liveUsed, 10, 64); parseErr == nil && value > seedUsed && value <= granted {
			seedUsed = value
		}
	}
	// Under batch accumulation the row the snapshot came from lags the spend the
	// accumulator still owes it. Signing that row as the remainder would publish
	// a ledger authorizing quota already spent, so the owed delta raises the
	// signed used counter instead: granted never moves, the remainder the seed
	// signs is the one the wallet holds.
	snapshotRemain := int(granted - seedUsed)
	liveRemain, balanceErr := quotaBalanceHeldByAccumulator(token.Id, BatchUpdateTypeTokenQuota, func() (int, error) {
		return snapshotRemain, nil
	})
	if balanceErr != nil {
		return 0, balanceErr
	}
	if liveRemain < snapshotRemain {
		seedUsed += int64(snapshotRemain - liveRemain)
	}
	ledgerMAC, ledgerExp, ledgerErr := tokenQuotaLedgerSeedArgs(token.Key, token.Id,
		granted, seedUsed, tokenCacheTTLSeconds())
	if ledgerErr != nil {
		ledgerMAC = ""
	}
	const script = quotaLedgerLuaHelpers + `
local args = cjson.decode(ARGV[23])
if redis.call('EXISTS', KEYS[2]) == 1 then
  return 0
end
local seedLedger = function()
  if args.seed.mac == '' or redis.call('HEXISTS', KEYS[1], 'QuotaLedgerMAC') == 1 then
    return
  end
  -- The signature covers args.seed.used, so the seed only runs while the hash
  -- still holds that counter. A spend that landed since Go read it leaves the
  -- hash without a ledger instead of signing a value nobody verified.
  local liveUsed = redis.call('HGET', KEYS[1], 'UsedQuota')
  if liveUsed ~= nil and liveUsed ~= args.seed.used then
    return
  end
  -- The remainder is derived with HINCRBY because Lua would round a sum near
  -- 2^53.
  redis.call('HSET', KEYS[1],
    'QuotaGranted', args.seed.granted, 'UsedQuota', args.seed.used,
    'RemainQuota', args.seed.granted,
    'QuotaLedgerMAC', args.seed.mac, 'QuotaLedgerExp', args.seed.expiresAt)
  subtractSigned(KEYS[1], 'RemainQuota', args.seed.used)
end
	local exists = redis.call('EXISTS', KEYS[1])
if exists == 1 and (ARGV[21] ~= 'true' or (redis.call('HGET', KEYS[1], 'CacheMAC') or '') ~= ARGV[22]) then
  seedLedger()
  -- The hash only earns a longer life while it carries a ledger the reservation
  -- can spend from, which Go decided from the signature before this script ran. A
  -- repair that declined leaves a hash nobody can spend, and renewing its TTL
  -- here would keep it alive past the row that has since caught up: the refusal
  -- would then repeat for as long as traffic kept refreshing it. Leaving the TTL
  -- alone lets the hash expire and the next read rebuild it from the flushed row.
  if ARGV[24] == 'true' then
    redis.call('EXPIRE', KEYS[1], ARGV[18])
  end
  return 2
end
redis.call('HDEL', KEYS[1], 'DeletedAt')
redis.call('HSET', KEYS[1],
  'Id', ARGV[1], 'UserId', ARGV[2], 'Status', ARGV[3], 'Name', ARGV[4],
  'CreatedTime', ARGV[5], 'ExpiredTime', ARGV[7],
  'UnlimitedQuota', ARGV[8], 'ModelLimitsEnabled', ARGV[9], 'ModelLimits', ARGV[10],
  'AllowIps', ARGV[11], 'Group', ARGV[12], 'CrossGroupRetry', ARGV[13],
  'AutoGroups', ARGV[14], 'AllowUserQuotaQuery', ARGV[17],
  'CacheExpiresAt', ARGV[19], 'CacheMAC', ARGV[20])
-- UsedQuota is filled with the counter the seed signs, not the snapshot's. The
-- two differ when the hash vanished while a batch debit was still queued: Go
-- raises the signed used counter by the delta the accumulator owes, which the
-- snapshot's row has not reached. Filling the row's value first then left
-- seedLedger comparing it against the raised counter, so it declined and the
-- fresh hash carried no ledger — and the reservation refuses a hash with no
-- signed ledger instead of answering from the row.
local seedUsed = ARGV[16]
if args.seed.mac ~= '' then
  seedUsed = args.seed.used
end
for _, counter in ipairs({{'AccessedTime', ARGV[6]}, {'RemainQuota', ARGV[15]}, {'UsedQuota', seedUsed}}) do
  if not string.match(redis.call('HGET', KEYS[1], counter[1]) or '', '^-?%d+$') then
    redis.call('HSET', KEYS[1], counter[1], counter[2])
  end
end
seedLedger()
redis.call('EXPIRE', KEYS[1], ARGV[18])
if exists == 1 then
  return 2
end
return 1`

	ledgerArgs, err := common.Marshal(struct {
		Seed struct {
			Granted   string `json:"granted"`
			UsedBase  string `json:"used"`
			ExpiresAt string `json:"expiresAt"`
			MAC       string `json:"mac"`
		} `json:"seed"`
	}{struct {
		Granted   string `json:"granted"`
		UsedBase  string `json:"used"`
		ExpiresAt string `json:"expiresAt"`
		MAC       string `json:"mac"`
	}{ledgerDecimal(granted), ledgerDecimal(seedUsed), ledgerDecimal(ledgerExp), ledgerMAC}})
	if err != nil {
		return 0, err
	}
	// The hash keeps its lifetime only while it carries a ledger the reservation
	// can spend from. tokenQuotaLedgerFieldsCarryASignature answers that from the
	// signature alone, so a genuine ledger whose window has closed still counts:
	// the reservation answers it from the row plus the accumulator's owed delta,
	// which is why closing the window must not shorten the hash below it.
	var liveFields map[string]string
	liveFields, liveErr := common.RDB.HGetAll(context.Background(), cacheKey).Result()
	liveLedgerHolds := liveErr == nil && tokenQuotaLedgerFieldsCarryASignature(liveFields, token.Id, token.Key)
	return common.RDB.Eval(context.Background(), script, []string{
		cacheKey, getTokenCacheFenceKey(token.Key),
	},
		token.Id, token.UserId, token.Status, token.Name,
		token.CreatedTime, token.AccessedTime, token.ExpiredTime,
		strconv.FormatBool(token.UnlimitedQuota), strconv.FormatBool(token.ModelLimitsEnabled),
		token.ModelLimits, allowIps, token.Group, strconv.FormatBool(token.CrossGroupRetry),
		token.AutoGroups, token.RemainQuota, token.UsedQuota,
		strconv.FormatBool(token.AllowUserQuotaQuery),
		tokenCacheTTLSeconds(),
		token.CacheExpiresAt, mac, strconv.FormatBool(repair), existing.CacheMAC,
		string(ledgerArgs), strconv.FormatBool(liveLedgerHolds),
	).Int()
}

// cacheGetTokenByKey 从缓存读取 token；不完整的哈希（如仅有配额字段）会被拒绝。
// The quota counters come from the signed ledger, as they do for a user: the
// mirror fields are outside the metadata MAC, so a writer holding only Redis
// access can set them, and every reader of this snapshot (the panel, the trust
// bypass) would otherwise be handed that forgery.
func cacheGetTokenByKey(key string) (*Token, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	if err := common.RedisHGetObj(getTokenCacheKey(key), &token); err != nil {
		return nil, err
	}
	if token.Id <= 0 {
		return nil, fmt.Errorf("token cache is incomplete")
	}
	token.Key = key
	mac, err := token.cacheMAC()
	if err != nil || !validAuthCacheMAC(mac, token.CacheMAC, token.CacheExpiresAt) {
		return nil, fmt.Errorf("token cache integrity check failed")
	}
	if ledger, result, ledgerErr := verifiedTokenQuotaLedger(token.Id, key); result == cacheQuotaOK && ledgerErr == nil {
		token.RemainQuota = int(ledger.granted - ledger.used)
		token.UsedQuota = int(ledger.used)
		return &token, nil
	} else if errors.Is(ledgerErr, ErrPreLedgerTokenHashCarriesNoSignature) && !errors.Is(ledgerErr, ErrDamagedTokenQuotaLedger) {
		// The hash carries no ledger signature at all: the shape a hash written
		// before this build signed token ledgers has. errTokenCacheNeedsRepair tells
		// the caller to republish it from the row, as the user twin's
		// "user cache schema is stale" does at cacheGetUserBase, and the caller
		// answers from the signature that comes back.
		//
		// The republish is taken only when it lands. cacheInitToken signs the used
		// counter the hash still holds, which keeps a debit recorded only in the
		// ledger — the row never reached, the accumulator in another pod — but its
		// seed declines when the hash no longer holds that counter, because then the
		// only value it could sign is the row's pre-debit one.
		return nil, errTokenCacheNeedsRepair
	}
	// The ledger is damaged, or its signature is genuine but past the window. The
	// mirror is the row as it was published, and under batch accumulation the row
	// lags the spend the accumulator still owes it, so the mirror alone hands back
	// quota already spent. The reported remainder is the mirror plus that owed
	// delta, read as one instant.
	return tokenFromItsMirror(&token)
}

// tokenFromItsMirror reports a token hash's counters when its ledger cannot answer
// for them: the mirror the hash carries plus the delta the accumulator still owes
// that row, read as one instant. It exists as its own function because
// GetTokenByKey needs it after a repair it could not use, and the row it just read
// is exactly what must not be handed back there.
func tokenFromItsMirror(token *Token) (*Token, error) {
	balance, balanceErr := quotaBalanceHeldByAccumulator(token.Id, BatchUpdateTypeTokenQuota, func() (int, error) {
		return token.RemainQuota, nil
	})
	if balanceErr != nil {
		return nil, balanceErr
	}
	if token.RemainQuota != balance {
		token.UsedQuota += token.RemainQuota - balance
		token.RemainQuota = balance
	}
	return token, nil
}
