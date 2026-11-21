package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type cacheQuotaResult int

const (
	cacheQuotaInsufficient cacheQuotaResult = iota
	cacheQuotaOK
	cacheQuotaMiss
	// cacheQuotaUnverified means the hash answered but its quota counters
	// contradict the signature they were published with.
	cacheQuotaUnverified
)

// ErrQuotaLedgerUnverified reports a cached quota counter that contradicts its
// signature or its own invariants. It is not a cache failure: Redis answered,
// but the balance derived from that state must not authorize a spend.
var ErrQuotaLedgerUnverified = errors.New("cached quota ledger failed verification")

// errQuotaLedgerBoundsRefused reports a committed delta the signed ledger cannot
// hold. The counter is left alone so the cached balance stays below the
// committed one until the hash is reloaded from the database.
var errQuotaLedgerBoundsRefused = errors.New("cached quota ledger cannot hold the committed delta")

// errQuotaLedgerContended reports that every attempt lost the signature race to
// a concurrent writer. The caller answers the reservation as insufficient
// instead of falling back to the database, because a database spend the ledger
// never sees is exactly the over-spend the ledger exists to prevent.
var errQuotaLedgerContended = errors.New("cached quota ledger contention")

// The cached quota counters sit in a hash whose metadata MAC zeroes them on
// purpose: they move on every spend, so signing them there would invalidate the
// hash on the first reservation and the following miss would reload a balance
// the batch accumulator has not flushed yet. The spend authority is therefore a
// separate ledger with its own signature:
//
//	QuotaCredited  every quota credited into the hash since it was published
//	QuotaSpentBase QuotaSpent at the moment the ledger was last signed
//	QuotaSpent     every quota reserved out of it; unsigned, monotone
//
// The spendable balance is QuotaCredited - QuotaSpent, and Quota only mirrors
// it. The ledger MAC binds the user, the authentication version, the schema and
// the two fields a reservation may not move, together with its own expiry. The
// reserve script enforces the invariants an unsigned counter owes:
//
//	spent >= spentBase         a reservation can neither be undone nor roll a
//	                           counter back past its signature
//	spent + amount <= credited the signed total covers the reservation
//
// so a forged Quota is never read, and a forged QuotaCredited or QuotaSpentBase
// fails the MAC. Those rules alone would let a writer reset QuotaSpent to its
// signed baseline and spend again, so a reservation may raise the baseline but
// never lower it: when the balance is exhausted the baseline moves to the live
// counter and the ledger is re-signed. Re-signing stays off the hot path
// because it happens only on that last reservation.
//
// The counters are decimal strings and the scripts never do arithmetic on
// them: Lua numbers are doubles, and a sum near the wallet ceiling (2^53-1)
// silently loses its last digit. Go passes every limit already formatted, the
// scripts compare magnitudes digit by digit, and every increment runs through
// HINCRBY, which adds inside Redis's own int64 domain. A cap is enforced by
// applying the increment, comparing the exact counter Redis reported, and
// rolling it back when it does not fit, so the comparison and the write still
// happen in one script. The mirror is then rewritten from the exact counters
// instead of incremented, so it can never keep an offset a forger gave it.
const quotaLedgerLuaHelpers = `
local function magnitude(raw)
  if raw == nil then return nil end
  local sign, digits = string.match(raw, '^(%-?)(%d+)$')
  if sign == nil or #digits > 19 then return nil end
  if #digits > 1 and string.sub(digits, 1, 1) == '0' then return nil end
  return sign, digits
end
local function compareMagnitude(left, right)
  if #left ~= #right then return #left < #right and -1 or 1 end
  if left == right then return 0 end
  return left < right and -1 or 1
end
local function positiveMagnitude(raw)
  local sign, digits = magnitude(raw)
  if sign == nil or sign == '-' or digits == '0' then return nil end
  return digits
end
local function rollback(key, field, amount)
  redis.call('HINCRBY', key, field, '-' .. amount)
end
local function unsubtract(key, field, amount)
  redis.call('HINCRBY', key, field, amount)
end
local function mirrorFrom(key, mirror, total, used)
  redis.call('HSET', key, mirror, redis.call('HGET', key, total))
  local usedSign, usedDigits = magnitude(redis.call('HGET', key, used))
  -- HINCRBY has no "-0", and subtracting zero adds nothing, so a zero counter
  -- is left alone instead of subtracted.
  if usedSign ~= nil and usedSign == '' and usedDigits ~= '0' then
    redis.call('HINCRBY', key, mirror, '-' .. usedDigits)
  end
end
`

const userQuotaLedgerScriptHeader = quotaLedgerLuaHelpers + `
local args = cjson.decode(ARGV[1])
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= args.id
  or tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') ~= args.schema
  or tonumber(redis.call('HGET', KEYS[1], 'AuthVersion') or '0') ~= args.authVersion then
  return -1
end
local credited = redis.call('HGET', KEYS[1], 'QuotaCredited')
local spent = redis.call('HGET', KEYS[1], 'QuotaSpent')
local spentBase = redis.call('HGET', KEYS[1], 'QuotaSpentBase')
local expiry = redis.call('HGET', KEYS[1], 'QuotaLedgerExp')
local creditedSign, creditedDigits = magnitude(credited)
local spentSign, spentDigits = magnitude(spent)
local baseSign, baseDigits = magnitude(spentBase)
local expirySign, expiryDigits = magnitude(expiry)
-- credited may be negative and spent may exceed it: a wallet holds a debt, and
-- the difference is the balance either way. Neither counter may be negative,
-- and spent may not sit below the value the ledger was signed with, so no write
-- can roll the counter back and spend the same quota twice.
if creditedSign == nil or spentSign == nil or baseSign == nil or expirySign == nil
  or spentSign == '-' or baseSign == '-' or expirySign == '-' then
  return -2
end
if compareMagnitude(spentDigits, baseDigits) < 0 then
  return -2
end
if (redis.call('HGET', KEYS[1], 'QuotaLedgerMAC') or '') ~= args.mac
  or credited ~= args.credited or spentBase ~= args.spentBase then
  return -1
end
local amountDigits = positiveMagnitude(args.amount)
if amountDigits == nil then
  return -1
end
local function advancesExpiry()
  return compareMagnitude(args.nextExpiry, expiryDigits) > 0
end
local function rebalance()
  mirrorFrom(KEYS[1], 'Quota', 'QuotaCredited', 'QuotaSpent')
end
`

const userQuotaReserveScript = userQuotaLedgerScriptHeader + `
if not advancesExpiry() then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'QuotaSpent', amountDigits)
local after = redis.call('HGET', KEYS[1], 'QuotaSpent')
local afterSign, afterDigits = magnitude(after)
-- spent + amount <= credited, read back from the counter Redis actually holds.
-- A negative credited is a wallet in debt: it can pay for nothing.
if afterSign == nil or afterSign == '-' or creditedSign == '-'
  or compareMagnitude(afterDigits, creditedDigits) > 0 then
  rollback(KEYS[1], 'QuotaSpent', amountDigits)
  return 0
end
rebalance()
redis.call('HSET', KEYS[1], 'QuotaLedgerExp', args.nextExpiry)
if after == credited then
  redis.call('HSET', KEYS[1], 'QuotaSpentBase', after, 'QuotaLedgerMAC', args.exhaustedMAC)
else
  redis.call('HSET', KEYS[1], 'QuotaLedgerMAC', args.nextMAC)
end
return 1`

// userQuotaDeltaScript applies a committed spend to the unsigned counter. It
// does not bound the spend by the signed total: the database already committed
// this delta, and refusing it would leave the cached balance above the
// committed one. A debit balance is representable, so spent may pass credited.
const userQuotaDeltaScript = userQuotaLedgerScriptHeader + `
redis.call('HINCRBY', KEYS[1], 'QuotaSpent', amountDigits)
rebalance()
return 1`

// userQuotaReleaseScript returns a reservation this process took but could not
// persist. It is not a credit: it only moves the unsigned counter back, and
// never past the value the ledger was signed with.
const userQuotaReleaseScript = userQuotaLedgerScriptHeader + `
redis.call('HINCRBY', KEYS[1], 'QuotaSpent', '-' .. amountDigits)
local after = redis.call('HGET', KEYS[1], 'QuotaSpent')
local afterSign, afterDigits = magnitude(after)
if afterSign == nil or afterSign == '-' or compareMagnitude(afterDigits, baseDigits) < 0 then
  unsubtract(KEYS[1], 'QuotaSpent', amountDigits)
  return -3
end
rebalance()
return 1`

// userQuotaCreditScript credits quota and re-signs the ledger, re-basing the
// unsigned counter at the same moment. It applies only while the hash still
// carries the signature the caller verified, so a concurrent reservation makes
// the attempt stale and the caller retries instead of losing a spend.
const userQuotaCreditScript = userQuotaLedgerScriptHeader + `
if not advancesExpiry() then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'QuotaCredited', amountDigits)
-- The caller signed the total it computed; the increment has to reproduce it
-- exactly, or the signature written below would describe another ledger.
if redis.call('HGET', KEYS[1], 'QuotaCredited') ~= args.nextCredited then
  rollback(KEYS[1], 'QuotaCredited', amountDigits)
  return -1
end
rebalance()
redis.call('HSET', KEYS[1], 'QuotaSpentBase', spent,
  'QuotaLedgerMAC', args.nextMAC, 'QuotaLedgerExp', args.nextExpiry)
return 1`

// userQuotaLedgerSeedScript publishes a ledger for a hash that has none. Only a
// database snapshot may seed one, so the credited total comes from the snapshot
// and the script refuses to run when the hash is already signed. The total is
// written as the exact decimal Go formatted, because Lua would round it to a
// double.
const userQuotaLedgerSeedScript = quotaLedgerLuaHelpers + `
if redis.call('HEXISTS', KEYS[1], 'QuotaLedgerMAC') == 1 then
  return 0
end
local args = cjson.decode(ARGV[1])
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= args.id
  or tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') ~= args.schema
  or tonumber(redis.call('HGET', KEYS[1], 'AuthVersion') or '0') ~= args.authVersion then
  return -1
end
if magnitude(args.credited) == nil or magnitude(args.nextExpiry) == nil then
  return -1
end
-- credited is the database balance, which a wallet in debt carries as a
-- negative total. spent starts at zero and only ever grows, so the balance
-- credited - spent reproduces the snapshot and stays correct afterwards.
redis.call('HSET', KEYS[1], 'QuotaCredited', args.credited, 'QuotaSpent', 0, 'QuotaSpentBase', 0,
  'QuotaLedgerExp', args.nextExpiry, 'QuotaLedgerMAC', args.nextMAC, 'Quota', args.credited)
return 1`

// The token ledger mirrors the user one on the token hash: QuotaGranted and
// QuotaUsedBase are signed, UsedQuota is the unsigned monotone counter, and
// RemainQuota only mirrors QuotaGranted - UsedQuota. The token key is part of
// the MAC, so a signature cannot be replayed onto another token.
const tokenQuotaLedgerScriptHeader = quotaLedgerLuaHelpers + `
local args = cjson.decode(ARGV[1])
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= args.id then
  return -1
end
local granted = redis.call('HGET', KEYS[1], 'QuotaGranted')
local used = redis.call('HGET', KEYS[1], 'UsedQuota')
local usedBase = redis.call('HGET', KEYS[1], 'QuotaUsedBase')
local expiry = redis.call('HGET', KEYS[1], 'QuotaLedgerExp')
local grantedSign, grantedDigits = magnitude(granted)
local usedSign, usedDigits = magnitude(used)
local baseSign, baseDigits = magnitude(usedBase)
local expirySign, expiryDigits = magnitude(expiry)
-- used may exceed granted: the remainder is then a debt, as on the user side,
-- so only the counters themselves have to stay non-negative.
if grantedSign == nil or usedSign == nil or baseSign == nil or expirySign == nil
  or grantedSign == '-' or usedSign == '-' or baseSign == '-' or expirySign == '-' then
  return -2
end
if compareMagnitude(usedDigits, baseDigits) < 0 then
  return -2
end
if (redis.call('HGET', KEYS[1], 'QuotaLedgerMAC') or '') ~= args.mac
  or granted ~= args.granted or usedBase ~= args.usedBase then
  return -1
end
local amountDigits = positiveMagnitude(args.amount)
if amountDigits == nil then
  return -1
end
local function advancesExpiry()
  return compareMagnitude(args.nextExpiry, expiryDigits) > 0
end
local function rebalance()
  mirrorFrom(KEYS[1], 'RemainQuota', 'QuotaGranted', 'UsedQuota')
end
`

const tokenQuotaReserveScript = tokenQuotaLedgerScriptHeader + `
if not advancesExpiry() then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'UsedQuota', amountDigits)
local after = redis.call('HGET', KEYS[1], 'UsedQuota')
local afterSign, afterDigits = magnitude(after)
if afterSign == nil or afterSign == '-' or compareMagnitude(afterDigits, grantedDigits) > 0 then
  rollback(KEYS[1], 'UsedQuota', amountDigits)
  return 0
end
rebalance()
redis.call('HSET', KEYS[1], 'AccessedTime', args.accessedTime, 'QuotaLedgerExp', args.nextExpiry)
if after == granted then
  redis.call('HSET', KEYS[1], 'QuotaUsedBase', after, 'QuotaLedgerMAC', args.exhaustedMAC)
else
  redis.call('HSET', KEYS[1], 'QuotaLedgerMAC', args.nextMAC)
end
return 1`

// tokenQuotaDeltaScript applies a committed spend to the unsigned counter. As
// on the user side it does not bound the spend by the signed total: the delta
// is already committed, and a debit remainder is representable.
const tokenQuotaDeltaScript = tokenQuotaLedgerScriptHeader + `
redis.call('HINCRBY', KEYS[1], 'UsedQuota', amountDigits)
rebalance()
redis.call('HSET', KEYS[1], 'AccessedTime', args.accessedTime)
return 1`

// tokenQuotaReleaseScript returns quota the commit recorded as unspent. The
// token hash has no credit path: IncreaseTokenQuota moves quota from the used
// counter back to the remainder, so the granted total never changes.
const tokenQuotaReleaseScript = tokenQuotaLedgerScriptHeader + `
redis.call('HINCRBY', KEYS[1], 'UsedQuota', '-' .. amountDigits)
local after = redis.call('HGET', KEYS[1], 'UsedQuota')
local afterSign, afterDigits = magnitude(after)
if afterSign == nil or afterSign == '-' or compareMagnitude(afterDigits, baseDigits) < 0 then
  unsubtract(KEYS[1], 'UsedQuota', amountDigits)
  return -3
end
rebalance()
redis.call('HSET', KEYS[1], 'AccessedTime', args.accessedTime)
return 1`

// quotaLedgerRetryAttempts bounds how often a concurrent credit may make a
// verified ledger stale before the attempt is reported as contended.
const quotaLedgerRetryAttempts = 4

// quotaLedgerMinWindowSeconds is the shortest life a signed ledger gets, in
// seconds.
const quotaLedgerMinWindowSeconds = 15

// quotaLedgerWindowMillis is the life a write signs, independent of the hash
// TTL. Every write renews it, so it only has to outlive the gap between the
// read that verified the hash and the script that writes it, plus one hash TTL
// for the read that follows; a window tied to a test-sized TTL would close
// inside that gap.
func quotaLedgerWindowMillis(ttlSeconds int) int64 {
	window := max(ttlSeconds, quotaLedgerMinWindowSeconds)
	return int64(window) * 1000
}

// nextLedgerExpiry returns the expiry a write should sign: a window from now
// that is strictly later than the one the hash already carries. Every write
// renews it, so a copy taken earlier always carries a smaller expiry.
func nextLedgerExpiry(previous int64, ttlSeconds int) int64 {
	expiresAt := time.Now().UnixMilli() + quotaLedgerWindowMillis(ttlSeconds)
	if expiresAt <= previous {
		return previous + 1
	}
	return expiresAt
}

// tokenQuotaLedgerMAC signs the fields a token reservation must trust. The
// token cache key is part of the MAC, so a signature cannot be replayed onto
// another token, and the expiry is signed so a copy taken earlier presents one
// the live hash has already passed.
func tokenQuotaLedgerMAC(key string, id int, granted, usedBase, expiresAt int64) (string, error) {
	return authCacheMAC(getTokenCacheKey(key), struct {
		Id            int
		QuotaGranted  int64
		QuotaUsedBase int64
		ExpiresAt     int64
	}{id, granted, usedBase, expiresAt})
}

// userQuotaLedgerArgs is the state a user ledger write carries into its script.
// The numbers travel as already-formatted decimal strings, so the script can
// compare them exactly and write them back unchanged; Lua would hold them as
// doubles and lose the last digit near the wallet ceiling.
type userQuotaLedgerArgs struct {
	Amount       string `json:"amount"`
	Id           int    `json:"id"`
	Schema       int    `json:"schema"`
	AuthVersion  int64  `json:"authVersion"`
	Credited     string `json:"credited"`
	SpentBase    string `json:"spentBase"`
	NextCredited string `json:"nextCredited"`
	NextExpiry   string `json:"nextExpiry"`
	MAC          string `json:"mac"`
	NextMAC      string `json:"nextMAC"`
	ExhaustedMAC string `json:"exhaustedMAC"`
}

// tokenQuotaLedgerArgs is the state a token ledger write carries into its
// script.
type tokenQuotaLedgerArgs struct {
	Amount       string `json:"amount"`
	Id           int    `json:"id"`
	Granted      string `json:"granted"`
	UsedBase     string `json:"usedBase"`
	NextExpiry   string `json:"nextExpiry"`
	AccessedTime int64  `json:"accessedTime"`
	MAC          string `json:"mac"`
	NextMAC      string `json:"nextMAC"`
	ExhaustedMAC string `json:"exhaustedMAC"`
}

func evalLedgerScript(script string, key string, args any) (int, error) {
	encodedArgs, err := common.Marshal(args)
	if err != nil {
		return 0, err
	}
	return common.RDB.Eval(context.Background(), script, []string{key}, string(encodedArgs)).Int()
}

// ledgerDecimal formats a counter for a script argument. The scripts compare
// these strings character by character instead of converting them to numbers,
// because a Lua number is a double and a wallet counter can sit at 2^53-1,
// where one unit of difference disappears.
func ledgerDecimal(value int64) string {
	return strconv.FormatInt(value, 10)
}

type userQuotaLedger struct {
	id          int
	authVersion int64
	schema      int
	credited    int64
	spent       int64
	spentBase   int64
	// expiresAt and mac are the signed expiry and signature the hash carried as
	// read. A write passes the mac back so the script proves the caller
	// verified this exact ledger, and signs the next expiry strictly later.
	expiresAt int64
	mac       string
}

type tokenQuotaLedger struct {
	id        int
	granted   int64
	used      int64
	usedBase  int64
	expiresAt int64
	mac       string
}

// quotaResultFromLua maps a script result to a cache result. region_pool.go's
// scripts report 1, 0 or -1; the quota ledger scripts report their own codes and
// are decoded at each call site so a retry can be told apart from a refusal.
func quotaResultFromLua(result int, err error) (cacheQuotaResult, error) {
	if err != nil {
		return cacheQuotaMiss, err
	}
	switch result {
	case 1:
		return cacheQuotaOK, nil
	case 0:
		return cacheQuotaInsufficient, nil
	default:
		return cacheQuotaMiss, nil
	}
}

// ledgerRefusalError explains a script result that reported the counters
// contradicting their signature. It is nil for every other result.
func ledgerRefusalError(result int) error {
	if result == -2 {
		return fmt.Errorf("%w: the quota counter contradicts its own signature", ErrQuotaLedgerUnverified)
	}
	return nil
}

// ledgerRefusalReason keeps the discard log informative when a script reported
// the refusal without an error of its own.
func ledgerRefusalReason(err error) string {
	if err == nil {
		return "does not match its quota ledger signature"
	}
	return err.Error()
}

func requireLedgerInt(fields map[string]string, name string) (int64, error) {
	raw, ok := fields[name]
	if !ok || raw == "" {
		return 0, fmt.Errorf("%w: the hash carries no %s", ErrQuotaLedgerUnverified, name)
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s is not an integer", ErrQuotaLedgerUnverified, name)
	}
	return value, nil
}

// verifiedUserQuotaLedger reads the user hash and checks that its authority
// fields are covered by their signature. A user with no cached hash is a miss;
// a hash that cannot be verified is reported as such and never as a balance.
func verifiedUserQuotaLedger(userID int) (*userQuotaLedger, cacheQuotaResult, error) {
	if !common.RedisEnabled {
		return nil, cacheQuotaMiss, nil
	}
	fields, err := common.RDB.HGetAll(context.Background(), getUserCacheKey(userID)).Result()
	if err != nil {
		return nil, cacheQuotaMiss, err
	}
	if len(fields) == 0 {
		return nil, cacheQuotaMiss, nil
	}
	id, err := requireLedgerInt(fields, "Id")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	if int(id) != userID {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the hash belongs to user %d", ErrQuotaLedgerUnverified, id)
	}
	schema, err := requireLedgerInt(fields, "CacheSchema")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	authVersion, err := requireLedgerInt(fields, "AuthVersion")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	if int(schema) != userCacheSchemaVersion || authVersion <= 0 {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the hash predates the quota ledger", ErrQuotaLedgerUnverified)
	}
	credited, err := requireLedgerInt(fields, "QuotaCredited")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	spent, err := requireLedgerInt(fields, "QuotaSpent")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	spentBase, err := requireLedgerInt(fields, "QuotaSpentBase")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	expiresAt, err := requireLedgerInt(fields, "QuotaLedgerExp")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	signed := UserBase{
		Id: userID, AuthVersion: authVersion, CacheSchema: int(schema),
		QuotaCredited: credited, QuotaSpent: spent, QuotaSpentBase: spentBase,
		QuotaLedgerMAC: fields["QuotaLedgerMAC"], QuotaLedgerExp: expiresAt,
	}
	if _, err := signed.verifiedQuotaLedger(); err != nil {
		return nil, cacheQuotaUnverified, err
	}
	return &userQuotaLedger{
		id: userID, authVersion: authVersion, schema: int(schema),
		credited: credited, spent: spent, spentBase: spentBase,
		expiresAt: expiresAt, mac: signed.QuotaLedgerMAC,
	}, cacheQuotaOK, nil
}

func verifiedTokenQuotaLedger(id int, key string) (*tokenQuotaLedger, cacheQuotaResult, error) {
	if !common.RedisEnabled {
		return nil, cacheQuotaMiss, nil
	}
	fields, err := common.RDB.HGetAll(context.Background(), getTokenCacheKey(key)).Result()
	if err != nil {
		return nil, cacheQuotaMiss, err
	}
	if len(fields) == 0 {
		return nil, cacheQuotaMiss, nil
	}
	cachedID, err := requireLedgerInt(fields, "Id")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	if int(cachedID) != id {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the hash belongs to token %d", ErrQuotaLedgerUnverified, cachedID)
	}
	granted, err := requireLedgerInt(fields, "QuotaGranted")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	used, err := requireLedgerInt(fields, "UsedQuota")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	usedBase, err := requireLedgerInt(fields, "QuotaUsedBase")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	expiresAt, err := requireLedgerInt(fields, "QuotaLedgerExp")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	expectedMAC, err := tokenQuotaLedgerMAC(key, id, granted, usedBase, expiresAt)
	if err != nil {
		return nil, cacheQuotaMiss, err
	}
	if !validAuthCacheMAC(expectedMAC, fields["QuotaLedgerMAC"], expiresAt) {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the token ledger signature does not match", ErrQuotaLedgerUnverified)
	}
	if granted < 0 || usedBase < 0 || used < usedBase {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the token ledger counters contradict each other", ErrQuotaLedgerUnverified)
	}
	return &tokenQuotaLedger{
		id: id, granted: granted, used: used, usedBase: usedBase,
		expiresAt: expiresAt, mac: fields["QuotaLedgerMAC"],
	}, cacheQuotaOK, nil
}

// cacheTryReserveUserQuota reserves quota from the signed ledger. A hash whose
// signature does not verify is reported as unverified; a write that lost the
// expiry race is retried against the state it just re-read.
func cacheTryReserveUserQuota(userID int, amount int64) (cacheQuotaResult, error) {
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedUserQuotaLedger(userID)
		if ledger == nil {
			return result, err
		}
		expiresAt := nextLedgerExpiry(ledger.expiresAt, userCacheTTLSeconds())
		nextMAC, exhaustedMAC, err := userLedgerMACs(userID, ledger.authVersion, ledger.schema,
			ledger.credited, ledger.spentBase, ledger.credited, expiresAt)
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(userQuotaReserveScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), SpentBase: ledgerDecimal(ledger.spentBase),
			NextExpiry: ledgerDecimal(expiresAt),
			MAC:        ledger.mac, NextMAC: nextMAC, ExhaustedMAC: exhaustedMAC,
		})
		if evalErr != nil {
			return cacheQuotaMiss, evalErr
		}
		switch raw {
		case 1:
			return cacheQuotaOK, nil
		case 0:
			return cacheQuotaInsufficient, nil
		case -2:
			return cacheQuotaUnverified, ledgerRefusalError(raw)
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

// userLedgerMACs signs the current authority and the exhausted authority a
// reservation may switch to in the same write.
func userLedgerMACs(userID int, authVersion int64, schema int,
	credited, spentBase, exhaustedBase, expiresAt int64) (nextMAC, exhaustedMAC string, err error) {
	signed := UserBase{
		Id: userID, AuthVersion: authVersion, CacheSchema: schema,
		QuotaCredited: credited, QuotaSpentBase: spentBase, QuotaLedgerExp: expiresAt,
	}
	nextMAC, err = signed.quotaLedgerMAC()
	if err != nil {
		return "", "", err
	}
	signed.QuotaSpentBase = exhaustedBase
	exhaustedMAC, err = signed.quotaLedgerMAC()
	if err != nil {
		return "", "", err
	}
	return nextMAC, exhaustedMAC, nil
}

func cacheTryReserveTokenQuota(id int, key string, amount int64) (cacheQuotaResult, error) {
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedTokenQuotaLedger(id, key)
		if ledger == nil {
			return result, err
		}
		expiresAt := nextLedgerExpiry(ledger.expiresAt, tokenCacheTTLSeconds())
		nextMAC, err := tokenQuotaLedgerMAC(key, id, ledger.granted, ledger.usedBase, expiresAt)
		if err != nil {
			return cacheQuotaMiss, err
		}
		exhaustedMAC, err := tokenQuotaLedgerMAC(key, id, ledger.granted, ledger.granted, expiresAt)
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(tokenQuotaReserveScript, getTokenCacheKey(key), tokenQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: id,
			Granted: ledgerDecimal(ledger.granted), UsedBase: ledgerDecimal(ledger.usedBase),
			NextExpiry: ledgerDecimal(expiresAt), AccessedTime: common.GetTimestamp(),
			MAC: ledger.mac, NextMAC: nextMAC, ExhaustedMAC: exhaustedMAC,
		})
		if evalErr != nil {
			return cacheQuotaMiss, evalErr
		}
		switch raw {
		case 1:
			return cacheQuotaOK, nil
		case 0:
			return cacheQuotaInsufficient, nil
		case -2:
			return cacheQuotaUnverified, ledgerRefusalError(raw)
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

// cacheApplyUserQuotaDelta applies a delta the database already committed. A
// positive delta is quota credited and re-signs the ledger; a negative one is a
// committed spend that only moves the unsigned counter.
func cacheApplyUserQuotaDelta(userID int, delta int64) (cacheQuotaResult, error) {
	if delta == 0 {
		return cacheQuotaOK, nil
	}
	if !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	if delta > 0 {
		return cacheCreditUserQuota(userID, delta)
	}
	amount := -delta
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedUserQuotaLedger(userID)
		if ledger == nil {
			return result, err
		}
		raw, evalErr := evalLedgerScript(userQuotaDeltaScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), SpentBase: ledgerDecimal(ledger.spentBase),
			MAC: ledger.mac,
		})
		if evalErr != nil {
			return cacheQuotaMiss, evalErr
		}
		switch raw {
		case 1:
			return cacheQuotaOK, nil
		case -2:
			return cacheQuotaUnverified, ledgerRefusalError(raw)
		case -3:
			return cacheQuotaInsufficient, errQuotaLedgerBoundsRefused
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

// cacheReleaseUserQuota returns quota a reservation took but could not persist.
// It is not a credit: it only restores the unsigned counter it advanced.
func cacheReleaseUserQuota(userID int, amount int64) (cacheQuotaResult, error) {
	if amount <= 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedUserQuotaLedger(userID)
		if ledger == nil {
			return result, err
		}
		raw, evalErr := evalLedgerScript(userQuotaReleaseScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), SpentBase: ledgerDecimal(ledger.spentBase),
			MAC: ledger.mac,
		})
		if evalErr != nil {
			return cacheQuotaMiss, evalErr
		}
		switch raw {
		case 1:
			return cacheQuotaOK, nil
		case -2:
			return cacheQuotaUnverified, ledgerRefusalError(raw)
		case -3:
			return cacheQuotaInsufficient, errQuotaLedgerBoundsRefused
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

func cacheApplyTokenQuotaDelta(id int, key string, delta int64) (cacheQuotaResult, error) {
	if delta == 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	if delta > 0 {
		return cacheReleaseTokenQuota(id, key, delta)
	}
	amount := -delta
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedTokenQuotaLedger(id, key)
		if ledger == nil {
			return result, err
		}
		raw, evalErr := evalLedgerScript(tokenQuotaDeltaScript, getTokenCacheKey(key), tokenQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: id,
			Granted: ledgerDecimal(ledger.granted), UsedBase: ledgerDecimal(ledger.usedBase),
			AccessedTime: common.GetTimestamp(), MAC: ledger.mac,
		})
		if evalErr != nil {
			return cacheQuotaMiss, evalErr
		}
		switch raw {
		case 1:
			return cacheQuotaOK, nil
		case -2:
			return cacheQuotaUnverified, ledgerRefusalError(raw)
		case -3:
			return cacheQuotaInsufficient, errQuotaLedgerBoundsRefused
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

func cacheReleaseTokenQuota(id int, key string, amount int64) (cacheQuotaResult, error) {
	if amount <= 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedTokenQuotaLedger(id, key)
		if ledger == nil {
			return result, err
		}
		raw, evalErr := evalLedgerScript(tokenQuotaReleaseScript, getTokenCacheKey(key), tokenQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: id,
			Granted: ledgerDecimal(ledger.granted), UsedBase: ledgerDecimal(ledger.usedBase),
			AccessedTime: common.GetTimestamp(), MAC: ledger.mac,
		})
		if evalErr != nil {
			return cacheQuotaMiss, evalErr
		}
		switch raw {
		case 1:
			return cacheQuotaOK, nil
		case -2:
			return cacheQuotaUnverified, ledgerRefusalError(raw)
		case -3:
			return cacheQuotaInsufficient, errQuotaLedgerBoundsRefused
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

// cacheCreditUserQuota raises the signed total. It re-signs the ledger, so the
// unsigned counter is re-based at the same moment and a concurrent reservation
// makes the attempt stale instead of losing a spend.
func cacheCreditUserQuota(userID int, delta int64) (cacheQuotaResult, error) {
	if delta <= 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedUserQuotaLedger(userID)
		if ledger == nil {
			return result, err
		}
		expiresAt := nextLedgerExpiry(ledger.expiresAt, userCacheTTLSeconds())
		nextMAC, err := (UserBase{
			Id: userID, AuthVersion: ledger.authVersion, CacheSchema: ledger.schema,
			QuotaCredited: ledger.credited + delta, QuotaSpentBase: ledger.spent,
			QuotaLedgerExp: expiresAt,
		}).quotaLedgerMAC()
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(userQuotaCreditScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(delta), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), SpentBase: ledgerDecimal(ledger.spentBase),
			NextCredited: ledgerDecimal(ledger.credited + delta), NextExpiry: ledgerDecimal(expiresAt),
			MAC: ledger.mac, NextMAC: nextMAC,
		})
		if evalErr != nil {
			return cacheQuotaMiss, evalErr
		}
		switch raw {
		case 1:
			return cacheQuotaOK, nil
		case -2:
			return cacheQuotaUnverified, ledgerRefusalError(raw)
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

// seedUserQuotaLedger publishes a ledger for a user hash that has none, using
// the database balance as the credited total. A hash that already carries a
// signed ledger is left alone, so a cache fill never resets live spend.
//
// A negative balance is a wallet in debt and is carried as a negative credited
// total, so the seed reproduces the snapshot exactly and a later committed
// delta still lands on the right balance.
func seedUserQuotaLedger(userID int, quota int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	credited := int64(quota)
	expiresAt := nextLedgerExpiry(0, userCacheTTLSeconds())
	mac, err := (UserBase{
		Id: userID, AuthVersion: authVersion, CacheSchema: userCacheSchemaVersion,
		QuotaCredited: credited, QuotaLedgerExp: expiresAt,
	}).quotaLedgerMAC()
	if err != nil {
		return err
	}
	_, err = evalLedgerScript(userQuotaLedgerSeedScript, getUserCacheKey(userID), userQuotaLedgerArgs{
		Id: userID, Schema: userCacheSchemaVersion, AuthVersion: authVersion,
		Credited: ledgerDecimal(credited), NextExpiry: ledgerDecimal(expiresAt), NextMAC: mac,
	})
	return err
}

// tokenQuotaLedgerSeedArgs builds the arguments that let cacheInitToken publish
// a token ledger in the same script that writes the snapshot. granted is the
// snapshot total and usedBase the counter the snapshot was taken at; a hash
// still holding a higher counter is signed at that counter, so an unflushed
// batch spend is neither lost nor signed under a baseline it does not match.
func tokenQuotaLedgerSeedArgs(key string, id int, granted, usedBase int64, ttlSeconds int) (string, int64, error) {
	if granted < 0 || usedBase < 0 {
		return "", 0, fmt.Errorf("token %d holds a negative granted quota", id)
	}
	expiresAt := nextLedgerExpiry(0, ttlSeconds)
	mac, err := tokenQuotaLedgerMAC(key, id, granted, usedBase, expiresAt)
	if err != nil {
		return "", 0, err
	}
	return mac, expiresAt, nil
}

// persistUserQuotaDelta 把已在缓存侧预扣成功的增量落库；批量模式下入队，
// 直写模式下要求行存在（用户已删除时报错，交由调用方补偿缓存）。
func persistUserQuotaDelta(id int, delta int) error {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeUserQuota, id, delta)
		return nil
	}
	result := DB.Model(&User{}).Where("id = ?", id).Update("quota", gorm.Expr("quota + ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func persistTokenQuotaDelta(id int, delta int) error {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, delta)
		return nil
	}
	result := DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]any{
			"remain_quota":  gorm.Expr("remain_quota + ?", delta),
			"used_quota":    gorm.Expr("used_quota - ?", delta),
			"accessed_time": common.GetTimestamp(),
		},
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func reserveUserQuotaDB(id int, quota int) (bool, error) {
	result := DB.Model(&User{}).
		Where("id = ? AND quota >= ?", id, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	return result.RowsAffected == 1, result.Error
}

func reserveTokenQuotaDB(id int, quota int) (bool, error) {
	result := DB.Model(&Token{}).
		Where("id = ? AND remain_quota >= ?", id, quota).
		Updates(map[string]any{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		})
	return result.RowsAffected == 1, result.Error
}

// discardUnverifiedUserQuotaCache drops a hash whose counters contradict their
// signature. The next read republishes the balance from the database, the only
// source a forged counter cannot reach, and the reservation below bypasses the
// hash entirely so the same request never trusts it.
func discardUnverifiedUserQuotaCache(id int, reason string) {
	common.SysError(common.LogText("user quota cache for user %d %s; discarding it and reserving from the database", id, reason))
	if err := invalidateUserCache(id); err != nil {
		common.SysError(common.LogText("failed to discard the unverified user quota cache for user %d: %s", id, err.Error()))
	}
}

func discardUnverifiedTokenQuotaCache(id int, key string, reason string) {
	common.SysError(common.LogText("token quota cache for token %d %s; discarding it and reserving from the database", id, reason))
	if err := invalidateTokenCacheForMutation(key); err != nil {
		common.SysError(common.LogText("failed to discard the unverified token quota cache for token %d: %s", id, err.Error()))
	}
}

// reserveUserQuotaCached reports whether the reservation succeeded and whether
// the answer came from the database. A hash that cannot be verified is
// discarded and answered from the database, never from zero: a zero balance
// would refuse a spend the account can pay for.
func reserveUserQuotaCached(id int, quota int) (reserved bool, fromDB bool, err error) {
	hydrated := false
	for range quotaLedgerRetryAttempts {
		result, reserveErr := cacheTryReserveUserQuota(id, int64(quota))
		switch {
		case errors.Is(reserveErr, ErrQuotaLedgerUnverified) || result == cacheQuotaUnverified:
			discardUnverifiedUserQuotaCache(id, ledgerRefusalReason(reserveErr))
			reserved, err = reserveUserQuotaDB(id, quota)
			return reserved, true, err
		case errors.Is(reserveErr, errQuotaLedgerContended):
			return false, false, nil
		case reserveErr != nil:
			return false, false, reserveErr
		case result == cacheQuotaOK:
			return true, false, nil
		case result == cacheQuotaInsufficient:
			return false, false, nil
		}
		if hydrated {
			break
		}
		hydrated = true
		if _, hydrateErr := GetUserCache(id); hydrateErr != nil {
			return false, false, hydrateErr
		}
	}
	// The hash could not be published: a fence blocks it, or the balance never
	// arrived. The database still answers the reservation.
	reserved, err = reserveUserQuotaDB(id, quota)
	return reserved, true, err
}

func reserveTokenQuotaCached(id int, key string, quota int) (reserved bool, fromDB bool, err error) {
	hydrated := false
	for range quotaLedgerRetryAttempts {
		result, reserveErr := cacheTryReserveTokenQuota(id, key, int64(quota))
		switch {
		case errors.Is(reserveErr, ErrQuotaLedgerUnverified) || result == cacheQuotaUnverified:
			discardUnverifiedTokenQuotaCache(id, key, ledgerRefusalReason(reserveErr))
			reserved, err = reserveTokenQuotaDB(id, quota)
			return reserved, true, err
		case errors.Is(reserveErr, errQuotaLedgerContended):
			return false, false, nil
		case reserveErr != nil:
			return false, false, reserveErr
		case result == cacheQuotaOK:
			return true, false, nil
		case result == cacheQuotaInsufficient:
			return false, false, nil
		}
		if hydrated {
			break
		}
		hydrated = true
		if _, hydrateErr := GetTokenByKey(key, true); hydrateErr != nil {
			return false, false, hydrateErr
		}
	}
	reserved, err = reserveTokenQuotaDB(id, quota)
	return reserved, true, err
}

// TryReserveUserQuota atomically checks and deducts a user's wallet quota.
// 缓存命中时以签名账本为准（避免批量模式下过期的数据库余额放大并发超扣）；
// Redis 异常、水合失败或账本与签名不符时降级为数据库条件更新。
func TryReserveUserQuota(id int, quota int) (bool, error) {
	if quota < 0 {
		return false, errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return true, nil
	}
	if RegionEnabled() {
		return reserveRegionQuota(RegionSubjectUser, strconv.Itoa(id), quota)
	}
	if !common.RedisEnabled {
		return reserveUserQuotaDB(id, quota)
	}

	reserved, fromDB, err := reserveUserQuotaCached(id, quota)
	if fromDB {
		return reserved, err
	}
	if err != nil {
		common.SysLog(common.LogText("user quota cache reserve unavailable, falling back to database: %s", err.Error()))
		return reserveUserQuotaDB(id, quota)
	}
	if !reserved {
		return false, nil
	}
	if err = persistUserQuotaDelta(id, -quota); err != nil {
		compensated, compensateErr := cacheReleaseUserQuota(id, int64(quota))
		if compensateErr != nil || compensated != cacheQuotaOK {
			common.SysError(common.LogText("failed to compensate reserved user quota: result=%d error=%v", compensated, compensateErr))
		}
		return false, err
	}
	return true, nil
}

// TryReserveTokenQuota atomically checks and deducts a token quota. Unlimited
// tokens skip the balance check but still update remain/used accounting.
func TryReserveTokenQuota(id int, key string, quota int, unlimited bool) (bool, error) {
	if quota < 0 {
		return false, errors.New("quota cannot be negative")
	}
	if quota == 0 {
		return true, nil
	}
	if unlimited {
		return true, DecreaseTokenQuota(id, key, quota)
	}
	if !common.RedisEnabled {
		return reserveTokenQuotaDB(id, quota)
	}

	reserved, fromDB, err := reserveTokenQuotaCached(id, key, quota)
	if fromDB {
		return reserved, err
	}
	if err != nil {
		common.SysLog(common.LogText("token quota cache reserve unavailable, falling back to database: %s", err.Error()))
		return reserveTokenQuotaDB(id, quota)
	}
	if !reserved {
		return false, nil
	}
	if err = persistTokenQuotaDelta(id, -quota); err != nil {
		compensated, compensateErr := cacheReleaseTokenQuota(id, key, int64(quota))
		if compensateErr != nil || compensated != cacheQuotaOK {
			common.SysError(common.LogText("failed to compensate reserved token quota: result=%d error=%v", compensated, compensateErr))
		}
		return false, err
	}
	return true, nil
}

// reserveRegionQuota spends from the region lease. A region never falls back
// to the database: that would bypass the lease and let every region spend the
// whole central balance.
func reserveRegionQuota(subjectType string, subjectID string, quota int) (bool, error) {
	err := spendRegionLease(subjectType, subjectID, int64(quota))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrRegionLeaseUnavailable) {
		return false, nil
	}
	return false, err
}
