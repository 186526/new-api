package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
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

// ErrPreLedgerHashCarriesNoSignature reports a user hash that carries no ledger
// signature this build produced. Every deployed image wrote userCacheSchemaVersion
// 2 with no ledger at all, and no metadata write signs the ledger fields, so such
// a hash can appear at any schema number: a settings save stamps the current one,
// and a writer holding only Redis access can raise it. Its balance lives in the
// unsigned Quota mirror alone, and neither the mirror nor the database row is
// safe to spend from — under batch accumulation the row predates the spend the
// accumulator still owes it. A reservation therefore refuses it outright instead
// of discarding it and answering from the row; only the read path rebuilds it,
// and the rebuild signs the balance the accumulator holds.
var ErrPreLedgerHashCarriesNoSignature = errors.New("cached user hash carries no signed quota ledger")

// errQuotaLedgerBoundsRefused reports a committed delta the signed ledger cannot
// hold. The counter is left alone so the cached balance stays below the
// committed one until the hash is reloaded from the database.
var errQuotaLedgerBoundsRefused = errors.New("cached quota ledger cannot hold the committed delta")

// errQuotaLedgerContended reports that a reservation lost the signature race to
// a concurrent writer often enough to give up. The wallet was never consulted,
// so answering it as an empty wallet is wrong, and answering it from the
// database is wrong too: under batch accumulation that row predates the debit.
// No caller retries this, so the read-CAS-write loop is serialized per subject
// (quotaLedgerStripe) and a write that is going to land does so on the first
// attempt; this error is the backstop for a subject two replicas are writing at
// the same instant, not the normal path.
var errQuotaLedgerContended = errors.New("cached quota ledger contention")

// errQuotaLedgerHashGone reports a committed delta with no signed hash to apply
// it to. The delta cannot be written anywhere, so it is reported rather than
// answered as success: a delta the caller is told succeeded while nothing moved
// leaves the cache authorizing quota the commit already spent.
var errQuotaLedgerHashGone = errors.New("cached quota ledger is not present")

// quotaLedgerStripe serializes the read-CAS-write loops that touch one subject's
// ledger. Every one of those loops reads a signed state, computes the next MAC
// and writes it only if the hash still carries the state it read; two of them
// interleaving in one process is what turns ordinary concurrent traffic into
// contention, because the loser has to re-read and try again. Serializing per
// subject means a write issued in this process never loses to another write in
// this process, so the retry budget only has to absorb a peer replica. Two
// subjects that share a stripe only serialize against each other, never against
// themselves, and a stripe is held only across one loop, never across a call
// that takes another stripe.
var quotaLedgerStripes [256]sync.Mutex

func quotaLedgerStripe(subject int) *sync.Mutex {
	return &quotaLedgerStripes[uint(subject)%uint(len(quotaLedgerStripes))]
}

// The cached quota counters sit in a hash whose metadata MAC covers neither of
// them: they move on every spend, and signing them there would invalidate the
// whole hash on the first reservation. The spend authority is therefore a
// separate ledger with its own signature:
//
//	QuotaCredited  every quota credited into the hash since it was published
//	QuotaSpent     every quota reserved out of it
//
// The spendable balance is QuotaCredited - QuotaSpent, and Quota only mirrors
// it. The ledger MAC binds the user, both counters and the expiry. The
// authentication version and the schema number sit outside it: both move on
// writes that never touch the ledger (a password, 2FA or passkey change raises
// the version, and every metadata write stamps its own schema), so signing them
// would break a live ledger on the first such write, and the reservation would
// then have no signed ledger to spend from. The metadata MAC still covers the
// version, and the scripts below still compare both fields against the state the
// caller read.
//
// Signing the live counter is what makes a rollback impossible: every write
// signs the exact value it leaves behind, so no earlier value of QuotaSpent
// carries a signature that still matches. A writer holding only Redis access
// may move the counter to any number, and every number but the current one
// fails the MAC. There is no baseline field to sit above: a baseline that only
// advanced when the balance was exhausted let a partial spend be rolled back to
// it, and it also refused the refund of an exhausting reservation that failed
// to persist, because the reservation itself had just moved the baseline past
// the refund.
//
// Each write also proves the state it signs: it passes both the signature it
// verified and the counter it is about to write, and the script refuses any
// state that no longer matches either. A committed delta that lands between the
// read and the write therefore makes the script refuse and the caller retry,
// instead of publishing a signature that describes another hash.
//
// The counters are decimal strings and the scripts never do arithmetic on
// them: Lua numbers are doubles, and a sum near the wallet ceiling (2^53-1)
// silently loses its last digit. Go passes every limit already formatted, the
// scripts compare magnitudes digit by digit, and every increment runs through
// HINCRBY, which adds inside Redis's own int64 domain. A cap is enforced by
// applying the increment, comparing the exact counter Redis reported, and
// rolling it back when it does not fit, so the comparison and the write still
// happen in one script. A write also refuses to sign a counter other than the
// one Go signed, which catches an int64 overflow on the Go side as well. The
// mirror is then rewritten from the exact counters instead of incremented, so
// it can never keep an offset a forger gave it.
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
local expiry = redis.call('HGET', KEYS[1], 'QuotaLedgerExp')
local creditedSign, creditedDigits = magnitude(credited)
local spentSign, spentDigits = magnitude(spent)
local expirySign, expiryDigits = magnitude(expiry)
-- credited may be negative and spent may exceed it: a wallet holds a debt, and
-- the difference is the balance either way. Neither counter may be negative, so
-- no write can hand out quota by un-spending below zero.
if creditedSign == nil or spentSign == nil or expirySign == nil
  or spentSign == '-' or expirySign == '-' then
  return -2
end
-- The caller verified this exact state and signed the counter it is about to
-- write. Both halves are compared, so a spend that landed since the read is a
-- refusal rather than a write that signs a state nobody checked.
if (redis.call('HGET', KEYS[1], 'QuotaLedgerMAC') or '') ~= args.mac
  or credited ~= args.credited or spent ~= args.spent then
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
-- sign publishes the state the caller signed: the counter has to be exactly the
-- value the caller's next MAC covers, or the write is undone and reported
-- stale. An int64 overflow on the Go side shows up here as a mismatch. undo is
-- the HINCRBY amount that reverses the write just made.
local function sign(field, undo)
  if redis.call('HGET', KEYS[1], field) ~= args.nextSpent then
    redis.call('HINCRBY', KEYS[1], field, undo)
    return false
  end
  rebalance()
  redis.call('HSET', KEYS[1], 'QuotaLedgerMAC', args.nextMAC, 'QuotaLedgerExp', args.nextExpiry)
  -- The hash must not outlive the signature that makes its counters spendable.
  -- Every write renews both, so a live hash always carries a live ledger; a
  -- key whose signature has expired is gone, and the next read rebuilds it from
  -- a row the accumulator has already flushed (the window is a multiple of the
  -- batch interval).
  redis.call('PEXPIREAT', KEYS[1], args.nextExpiry)
  return true
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
if not sign('QuotaSpent', '-' .. amountDigits) then
  return -1
end
return 1`

// userQuotaDeltaScript applies a committed spend to the counter. It does not
// bound the spend by the signed total: the database already committed this
// delta, and refusing it would leave the cached balance above the committed
// one. A debit balance is representable, so spent may pass credited.
const userQuotaDeltaScript = userQuotaLedgerScriptHeader + `
if not advancesExpiry() then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'QuotaSpent', amountDigits)
if not sign('QuotaSpent', '-' .. amountDigits) then
  return -1
end
return 1`

// userQuotaReleaseScript returns a reservation this process took but could not
// persist. It moves the counter back and re-signs; it is not a credit, so the
// signed total is untouched and a release can never widen what the wallet may
// spend.
const userQuotaReleaseScript = userQuotaLedgerScriptHeader + `
if not advancesExpiry() then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'QuotaSpent', '-' .. amountDigits)
local after = redis.call('HGET', KEYS[1], 'QuotaSpent')
local afterSign, _ = magnitude(after)
if afterSign == nil or afterSign == '-' then
  unsubtract(KEYS[1], 'QuotaSpent', amountDigits)
  return -3
end
if not sign('QuotaSpent', amountDigits) then
  return -1
end
return 1`

// userQuotaCreditScript credits quota and re-signs the ledger. It applies only
// while the hash still carries the counter the caller verified, so a concurrent
// spend makes the attempt stale and the caller retries instead of publishing a
// signature that describes another ledger.
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
redis.call('HSET', KEYS[1], 'QuotaLedgerMAC', args.nextMAC, 'QuotaLedgerExp', args.nextExpiry)
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
if magnitude(args.credited) == nil or magnitude(args.spent) == nil
  or magnitude(args.nextExpiry) == nil then
  return -1
end
-- credited and spent reproduce the balance the wallet holds, which is the row
-- once the debit the accumulator still owes it is taken into account. The
-- mirror Quota is derived from them rather than written from the row, because
-- the row lags under batch accumulation.
redis.call('HSET', KEYS[1], 'QuotaCredited', args.credited, 'QuotaSpent', args.spent,
  'QuotaLedgerExp', args.nextExpiry, 'QuotaLedgerMAC', args.nextMAC, 'Quota', 0)
redis.call('HINCRBY', KEYS[1], 'Quota', args.credited)
redis.call('HINCRBY', KEYS[1], 'Quota', '-' .. args.spent)
return 1`

// The token ledger mirrors the user one on the token hash: QuotaGranted and the
// live UsedQuota are signed, and RemainQuota only mirrors
// QuotaGranted - UsedQuota. The token key is part of the MAC, so a signature
// cannot be replayed onto another token.
const tokenQuotaLedgerScriptHeader = quotaLedgerLuaHelpers + `
local args = cjson.decode(ARGV[1])
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= args.id then
  return -1
end
local granted = redis.call('HGET', KEYS[1], 'QuotaGranted')
local used = redis.call('HGET', KEYS[1], 'UsedQuota')
local expiry = redis.call('HGET', KEYS[1], 'QuotaLedgerExp')
local grantedSign, grantedDigits = magnitude(granted)
local usedSign, usedDigits = magnitude(used)
local expirySign, expiryDigits = magnitude(expiry)
-- used may exceed granted: the remainder is then a debt, as on the user side,
-- so only the counters themselves have to stay non-negative.
if grantedSign == nil or usedSign == nil or expirySign == nil
  or grantedSign == '-' or usedSign == '-' or expirySign == '-' then
  return -2
end
-- As on the user side, the caller signed the counter it is about to write, and
-- a spend that landed since the read is a refusal.
if (redis.call('HGET', KEYS[1], 'QuotaLedgerMAC') or '') ~= args.mac
  or granted ~= args.granted or used ~= args.used then
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
-- sign publishes the counter the caller signed; a mismatch is undone and
-- reported stale, as on the user side.
local function sign(field, amount)
  if redis.call('HGET', KEYS[1], field) ~= args.nextUsed then
    unsubtract(KEYS[1], field, amount)
    return false
  end
  rebalance()
  redis.call('HSET', KEYS[1], 'AccessedTime', args.accessedTime,
    'QuotaLedgerMAC', args.nextMAC, 'QuotaLedgerExp', args.nextExpiry)
  -- As on the user side, the hash lives exactly as long as the signature that
  -- makes its counters spendable.
  redis.call('PEXPIREAT', KEYS[1], args.nextExpiry)
  return true
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
if not sign('UsedQuota', '-' .. amountDigits) then
  return -1
end
return 1`

// tokenQuotaDeltaScript applies a committed spend to the counter. As on the
// user side it does not bound the spend by the signed total: the delta is
// already committed, and a debit remainder is representable.
const tokenQuotaDeltaScript = tokenQuotaLedgerScriptHeader + `
if not advancesExpiry() then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'UsedQuota', amountDigits)
if not sign('UsedQuota', '-' .. amountDigits) then
  return -1
end
return 1`

// tokenQuotaReleaseScript returns quota the commit recorded as unspent. The
// token hash has no credit path: IncreaseTokenQuota moves quota from the used
// counter back to the remainder, so the granted total never changes.
const tokenQuotaReleaseScript = tokenQuotaLedgerScriptHeader + `
if not advancesExpiry() then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'UsedQuota', '-' .. amountDigits)
local after = redis.call('HGET', KEYS[1], 'UsedQuota')
local afterSign, _ = magnitude(after)
if afterSign == nil or afterSign == '-' then
  unsubtract(KEYS[1], 'UsedQuota', amountDigits)
  return -3
end
if not sign('UsedQuota', amountDigits) then
  return -1
end
return 1`

// quotaLedgerRetryAttempts bounds how often a peer replica may move the ledger
// between a read and its write before a reservation reports itself contended.
// Writes issued in this process are serialized by quotaLedgerStripe, so the
// only writer that can still steal the CAS is another process; the budget is
// the number of such collisions a burst may absorb, not the concurrency the
// wallet sees. Measured with the stripe held: 512-way unit reserves against a
// 1,000,000 wallet are accepted 512 of 512, the same as master.
const quotaLedgerRetryAttempts = 64

// quotaLedgerDeltaAttempts bounds how many times a committed delta re-reads a
// ledger a peer replica moved between its read and its write. Writes issued in
// this process are serialized by quotaLedgerStripe, so this cap only absorbs a
// collision with another process. The delta is never dropped at the end of it:
// an unapplied spend leaves the signed counter below the spend the commit
// recorded, which authorizes quota already spent, so the caller is told the
// delta did not land (errQuotaLedgerContended) instead of being told it did.
const quotaLedgerDeltaAttempts = 64

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
// the live hash has already passed. The live used counter is signed, not a
// baseline, so no earlier value of it verifies.
func tokenQuotaLedgerMAC(key string, id int, granted, used, expiresAt int64) (string, error) {
	return authCacheMAC(getTokenCacheKey(key), struct {
		Id           int
		QuotaGranted int64
		UsedQuota    int64
		ExpiresAt    int64
	}{id, granted, used, expiresAt})
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
	Spent        string `json:"spent"`
	NextCredited string `json:"nextCredited"`
	NextSpent    string `json:"nextSpent"`
	NextExpiry   string `json:"nextExpiry"`
	MAC          string `json:"mac"`
	NextMAC      string `json:"nextMAC"`
}

// tokenQuotaLedgerArgs is the state a token ledger write carries into its
// script.
type tokenQuotaLedgerArgs struct {
	Amount       string `json:"amount"`
	Id           int    `json:"id"`
	Granted      string `json:"granted"`
	Used         string `json:"used"`
	NextUsed     string `json:"nextUsed"`
	NextExpiry   string `json:"nextExpiry"`
	AccessedTime int64  `json:"accessedTime"`
	MAC          string `json:"mac"`
	NextMAC      string `json:"nextMAC"`
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
	// expiresAt and mac are the signed expiry and signature the hash carried as
	// read. A write passes the mac and both counters back so the script proves
	// the caller verified this exact ledger, and signs the next expiry strictly
	// later.
	expiresAt int64
	mac       string
}

type tokenQuotaLedger struct {
	id        int
	granted   int64
	used      int64
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
	value, err := parseLedgerInt(fields, name)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrQuotaLedgerUnverified, err)
	}
	return value, nil
}

// parseLedgerInt reads one integer field of a hash. The error it returns says
// what is wrong with the field and carries no sentinel, so each caller decides
// which refusal it answers with: an unverifiable ledger is discarded and
// answered from the row, while a hash whose layout is already known to be
// unspendable is refused outright (see requireSignedLayoutInt).
func parseLedgerInt(fields map[string]string, name string) (int64, error) {
	raw, ok := fields[name]
	if !ok || raw == "" {
		return 0, fmt.Errorf("the hash carries no %s", name)
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is not an integer", name)
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
	// The layout is decided by the ledger signature, not by the schema number and
	// not by the presence of the ledger fields. Every metadata write stamps
	// writeUserCache's schema on the hash while adding no ledger, so a live
	// pre-ledger hash reaches the current number the first time the user saves
	// anything; and cacheMAC zeroes the ledger fields before signing while the
	// decoder fills every field by name, so a writer holding only Redis access can
	// add a marker that no signature covers. Either test would let one unsigned
	// write spend from a hash whose balance was never signed.
	//
	// It is asked before any field is parsed, because every other answer has to
	// stay away from the database row: the reservation discards an *unverified
	// ledger* and answers it from the row, and under batch accumulation that row
	// predates the debit the accumulator still owes it. A hash whose own parse
	// failed used to take that route, so deleting one field handed the spend back.
	if !userLedgerFieldsCarryASignature(fields, userID) {
		// Typed apart from ErrQuotaLedgerUnverified on purpose: the reservation
		// below answers an unverified *ledger* from the database row, and that
		// answer is the pre-debit balance this check exists to keep unspendable.
		// A hash with no signed ledger is refused instead, and left in place so its
		// own TTL rebuilds it from the database.
		return nil, cacheQuotaUnverified, ErrPreLedgerHashCarriesNoSignature
	}
	// The signature covers the counters and the expiry only, so the fields it
	// leaves out can be deleted by a writer holding Redis access without breaking
	// it. They are read through requireSignedLayoutInt, which keeps that damage on
	// the refusal above instead of on the row.
	id, err := requireSignedLayoutInt(fields, "Id")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	if int(id) != userID {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the hash belongs to user %d", ErrPreLedgerHashCarriesNoSignature, id)
	}
	schema, err := requireSignedLayoutInt(fields, "CacheSchema")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	authVersion, err := requireSignedLayoutInt(fields, "AuthVersion")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	if authVersion <= 0 {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the hash carries no authentication version", ErrPreLedgerHashCarriesNoSignature)
	}
	credited, err := requireSignedLayoutInt(fields, "QuotaCredited")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	spent, err := requireSignedLayoutInt(fields, "QuotaSpent")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	expiresAt, err := requireSignedLayoutInt(fields, "QuotaLedgerExp")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	signed := UserBase{
		Id: userID, QuotaCredited: credited, QuotaSpent: spent,
		QuotaLedgerMAC: fields["QuotaLedgerMAC"], QuotaLedgerExp: expiresAt,
	}
	if _, err := signed.verifiedQuotaLedger(); err != nil {
		return nil, cacheQuotaUnverified, err
	}
	return &userQuotaLedger{
		id: userID, authVersion: authVersion, schema: int(schema),
		credited: credited, spent: spent,
		expiresAt: expiresAt, mac: signed.QuotaLedgerMAC,
	}, cacheQuotaOK, nil
}

// userLedgerFieldsCarryASignature reports whether the raw hash fields hold a
// ledger this build signed, without regarding the window. It answers the one
// question the reservation guard asks — is this the signed layout — so a field
// that does not parse counts as no signature rather than as an unverified
// ledger. The read path makes the same decision through carriesSignedLedger,
// and the window is left to verifiedQuotaLedger, which answers with the balance
// when it holds and reports the expiry when it does not.
func userLedgerFieldsCarryASignature(fields map[string]string, userID int) bool {
	credited, creditedErr := parseLedgerInt(fields, "QuotaCredited")
	spent, spentErr := parseLedgerInt(fields, "QuotaSpent")
	expiresAt, expiresErr := parseLedgerInt(fields, "QuotaLedgerExp")
	if creditedErr != nil || spentErr != nil || expiresErr != nil {
		return false
	}
	return UserBase{
		Id: userID, QuotaCredited: credited, QuotaSpent: spent,
		QuotaLedgerMAC: fields["QuotaLedgerMAC"], QuotaLedgerExp: expiresAt,
	}.carriesSignedLedger()
}

// requireSignedLayoutInt reads a field of a hash whose ledger signature already
// held. The signature covers the counters and the expiry, not Id, CacheSchema or
// AuthVersion, so a writer holding Redis access can delete or zero one of those
// and leave a verifiable ledger behind. Such a hash is not this build's layout
// any more, and the answer has to stay away from the database row for the same
// reason the pre-ledger refusal exists: under batch accumulation that row
// predates the debit the accumulator still owes it. The error is therefore
// ErrPreLedgerHashCarriesNoSignature, not ErrQuotaLedgerUnverified, whose
// callers discard the hash and reserve from the row.
func requireSignedLayoutInt(fields map[string]string, name string) (int64, error) {
	value, err := parseLedgerInt(fields, name)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrPreLedgerHashCarriesNoSignature, err)
	}
	return value, nil
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
	expiresAt, err := requireLedgerInt(fields, "QuotaLedgerExp")
	if err != nil {
		return nil, cacheQuotaUnverified, err
	}
	expectedMAC, err := tokenQuotaLedgerMAC(key, id, granted, used, expiresAt)
	if err != nil {
		return nil, cacheQuotaMiss, err
	}
	if !validAuthCacheMAC(expectedMAC, fields["QuotaLedgerMAC"], expiresAt) {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the token ledger signature does not match", ErrQuotaLedgerUnverified)
	}
	if granted < 0 || used < 0 {
		return nil, cacheQuotaUnverified, fmt.Errorf("%w: the token ledger counters contradict each other", ErrQuotaLedgerUnverified)
	}
	return &tokenQuotaLedger{
		id: id, granted: granted, used: used,
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
		nextSpent := ledger.spent + amount
		nextMAC, err := (UserBase{
			Id: userID, QuotaCredited: ledger.credited, QuotaSpent: nextSpent,
			QuotaLedgerExp: expiresAt,
		}).quotaLedgerMAC()
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(userQuotaReserveScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), Spent: ledgerDecimal(ledger.spent),
			NextSpent: ledgerDecimal(nextSpent), NextExpiry: ledgerDecimal(expiresAt),
			MAC: ledger.mac, NextMAC: nextMAC,
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

func cacheTryReserveTokenQuota(id int, key string, amount int64) (cacheQuotaResult, error) {
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedTokenQuotaLedger(id, key)
		if ledger == nil {
			return result, err
		}
		expiresAt := nextLedgerExpiry(ledger.expiresAt, tokenCacheTTLSeconds())
		nextUsed := ledger.used + amount
		nextMAC, err := tokenQuotaLedgerMAC(key, id, ledger.granted, nextUsed, expiresAt)
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(tokenQuotaReserveScript, getTokenCacheKey(key), tokenQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: id,
			Granted: ledgerDecimal(ledger.granted), Used: ledgerDecimal(ledger.used),
			NextUsed: ledgerDecimal(nextUsed), NextExpiry: ledgerDecimal(expiresAt),
			AccessedTime: common.GetTimestamp(), MAC: ledger.mac, NextMAC: nextMAC,
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
// committed spend that moves the signed counter.
//
// A committed spend that does not land leaves the signed counter *below* the
// spend the commit recorded, which is the direction that authorizes quota
// already spent, so this path never drops it: it retries under the subject's
// stripe until it lands, republishes a missing hash from the database first,
// and reports an error rather than success if neither is possible.
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
	stripe := quotaLedgerStripe(userID)
	stripe.Lock()
	defer stripe.Unlock()
	return applyUserQuotaDebit(userID, -delta, true)
}

// syncRowCommittedUserQuota hands a delta the caller already wrote to the row to
// the signed ledger. It never hydrates from the database: the row already holds
// this delta, so rebuilding the ledger from it and applying the delta again
// would count the same amount twice — measured at wallet 100, row debited 40,
// hash absent, this left cache=60 against row=100 when it hydrated under batch
// accumulation.
//
// An absent ledger is a no-op: the row is the authority for this delta and the
// next read republishes the ledger from it. An error is not a no-op — a Redis
// that answers LOADING or refuses the script reports `cacheQuotaMiss` with an
// error attached, and swallowing that would tell the caller a delta landed when
// nothing moved. The caller only ever sees success when the delta was applied
// or there was genuinely nothing to apply.
func syncRowCommittedUserQuota(userID int, delta int64) error {
	if delta == 0 || !common.RedisEnabled {
		return nil
	}
	stripe := quotaLedgerStripe(userID)
	stripe.Lock()
	defer stripe.Unlock()
	if delta > 0 {
		_, err := creditUserQuotaLocked(userID, delta)
		return err
	}
	_, err := applyUserQuotaDebit(userID, -delta, false)
	return err
}

// applyUserQuotaDebit moves the signed spend counter by amount. The caller holds
// the subject's stripe, so the only writer that can still steal the CAS is
// another process.
//
// Whether an absent hash should be republished depends on whether the row
// already holds this delta. The caller states that, because the batch flag does
// not decide it: the relay's reserve path queues its debit (row predates it),
// while the row-first callers below write the row directly in both modes.
//
//   - Row-first: the caller wrote the row before handing the delta to the cache,
//     so the row already holds it and is authoritative. There is nothing to
//     apply: the next read seeds the ledger from the row and the debit shows up
//     once. Republishing here would seed QuotaCredited from a row that already
//     includes the debit and then subtract it again, which leaves the cache
//     below the row and refuses a funded wallet. Measured under batch
//     accumulation, wallet 100 with the row debited 40: hydrate-and-apply left
//     cache=20 against row=60; skipping the rebuild leaves 60/60.
//   - Queued: the caller queued the debit in the accumulator and the row predates
//     it, so the cache is the only place that can carry it yet. A missing hash is
//     republished and the delta applied on top.
//
// The row-first callers that write the row directly in a transaction
// (PurchaseSubscriptionWithBalance, AdjustUserQuota) hold the debit in the row in
// both modes, so they pass hydrateOnMiss=false through
// syncRowCommittedUserQuota and never reach the rebuild.
//
// The batch case has one window it cannot close: the accumulator may flush
// between addNewRecord and this call, so the row already holds the debit when
// the hash is reconstructed, and the re-apply then leaves the cache one debit
// below the row. The accumulator cannot tell the two apart at this point,
// because the cache write is scheduled before addNewRecord. The resulting drift
// refuses spends rather than authorizing them, and it heals when the hash
// expires and is reseeded from the row.
func applyUserQuotaDebit(userID int, amount int64, hydrateOnMiss bool) (cacheQuotaResult, error) {
	hydrated := false
	for attempt := 0; attempt < quotaLedgerDeltaAttempts; attempt++ {
		ledger, result, err := verifiedUserQuotaLedger(userID)
		if ledger == nil {
			if err != nil || result == cacheQuotaUnverified {
				return result, err
			}
			if !common.BatchUpdateEnabled || !hydrateOnMiss {
				// The row holds the debit; the cache carries nothing until the
				// next read republishes it. Applying it here would count it twice.
				return cacheQuotaOK, nil
			}
			if hydrated {
				return cacheQuotaMiss, errQuotaLedgerHashGone
			}
			hydrated = true
			if _, hydrateErr := GetUserCache(userID); hydrateErr != nil {
				return cacheQuotaMiss, fmt.Errorf("%w: %w", errQuotaLedgerHashGone, hydrateErr)
			}
			continue
		}
		nextSpent := ledger.spent + amount
		expiresAt := nextLedgerExpiry(ledger.expiresAt, userCacheTTLSeconds())
		nextMAC, err := (UserBase{
			Id: userID, QuotaCredited: ledger.credited, QuotaSpent: nextSpent,
			QuotaLedgerExp: expiresAt,
		}).quotaLedgerMAC()
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(userQuotaDeltaScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), Spent: ledgerDecimal(ledger.spent),
			NextSpent: ledgerDecimal(nextSpent), NextExpiry: ledgerDecimal(expiresAt),
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

// cacheReleaseUserQuota returns quota a reservation took but could not persist.
// It is not a credit: it only moves the signed counter back and re-signs, so the
// signed total stays where it was and a release can never widen a wallet.
func cacheReleaseUserQuota(userID int, amount int64) (cacheQuotaResult, error) {
	if amount <= 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	stripe := quotaLedgerStripe(userID)
	stripe.Lock()
	defer stripe.Unlock()
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedUserQuotaLedger(userID)
		if ledger == nil {
			return result, err
		}
		nextSpent := ledger.spent - amount
		expiresAt := nextLedgerExpiry(ledger.expiresAt, userCacheTTLSeconds())
		nextMAC, err := (UserBase{
			Id: userID, QuotaCredited: ledger.credited, QuotaSpent: nextSpent,
			QuotaLedgerExp: expiresAt,
		}).quotaLedgerMAC()
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(userQuotaReleaseScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), Spent: ledgerDecimal(ledger.spent),
			NextSpent: ledgerDecimal(nextSpent), NextExpiry: ledgerDecimal(expiresAt),
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
		case -3:
			return cacheQuotaInsufficient, errQuotaLedgerBoundsRefused
		}
	}
	return cacheQuotaMiss, errQuotaLedgerContended
}

// cacheApplyTokenQuotaDelta mirrors cacheApplyUserQuotaDelta on the token
// ledger: the delta is already committed, so a spend that does not land is
// retried under the token's stripe and never dropped.
func cacheApplyTokenQuotaDelta(id int, key string, delta int64) (cacheQuotaResult, error) {
	if delta == 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	if delta > 0 {
		return cacheReleaseTokenQuota(id, key, delta)
	}
	stripe := quotaLedgerStripe(id)
	stripe.Lock()
	defer stripe.Unlock()
	return applyTokenQuotaDebit(id, key, -delta, true)
}

// applyTokenQuotaDebit mirrors applyUserQuotaDebit on the token hash: an absent
// hash is only republished when the caller queued the debit in the accumulator
// instead of writing the row. A row-first caller passes hydrateOnMiss=false,
// because the row already holds the debit and rebuilding the ledger from it
// would subtract the same amount again.
func applyTokenQuotaDebit(id int, key string, amount int64, hydrateOnMiss bool) (cacheQuotaResult, error) {
	hydrated := false
	for attempt := 0; attempt < quotaLedgerDeltaAttempts; attempt++ {
		ledger, result, err := verifiedTokenQuotaLedger(id, key)
		if ledger == nil {
			if err != nil || result == cacheQuotaUnverified {
				return result, err
			}
			if !common.BatchUpdateEnabled || !hydrateOnMiss {
				return cacheQuotaOK, nil
			}
			if hydrated {
				return cacheQuotaMiss, errQuotaLedgerHashGone
			}
			hydrated = true
			if _, hydrateErr := GetTokenByKey(key, true); hydrateErr != nil {
				return cacheQuotaMiss, fmt.Errorf("%w: %w", errQuotaLedgerHashGone, hydrateErr)
			}
			continue
		}
		nextUsed := ledger.used + amount
		expiresAt := nextLedgerExpiry(ledger.expiresAt, tokenCacheTTLSeconds())
		nextMAC, err := tokenQuotaLedgerMAC(key, id, ledger.granted, nextUsed, expiresAt)
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(tokenQuotaDeltaScript, getTokenCacheKey(key), tokenQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: id,
			Granted: ledgerDecimal(ledger.granted), Used: ledgerDecimal(ledger.used),
			NextUsed: ledgerDecimal(nextUsed), NextExpiry: ledgerDecimal(expiresAt),
			AccessedTime: common.GetTimestamp(), MAC: ledger.mac, NextMAC: nextMAC,
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

// cacheReleaseTokenQuota returns quota a commit recorded as unspent. It moves
// the used counter back and re-signs; the granted total never changes.
func cacheReleaseTokenQuota(id int, key string, amount int64) (cacheQuotaResult, error) {
	if amount <= 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	stripe := quotaLedgerStripe(id)
	stripe.Lock()
	defer stripe.Unlock()
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedTokenQuotaLedger(id, key)
		if ledger == nil {
			return result, err
		}
		nextUsed := ledger.used - amount
		expiresAt := nextLedgerExpiry(ledger.expiresAt, tokenCacheTTLSeconds())
		nextMAC, err := tokenQuotaLedgerMAC(key, id, ledger.granted, nextUsed, expiresAt)
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(tokenQuotaReleaseScript, getTokenCacheKey(key), tokenQuotaLedgerArgs{
			Amount: ledgerDecimal(amount), Id: id,
			Granted: ledgerDecimal(ledger.granted), Used: ledgerDecimal(ledger.used),
			NextUsed: ledgerDecimal(nextUsed), NextExpiry: ledgerDecimal(expiresAt),
			AccessedTime: common.GetTimestamp(), MAC: ledger.mac, NextMAC: nextMAC,
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

// cacheCreditUserQuota raises the signed total and re-signs the ledger. It
// passes the counter it signs, so a concurrent spend makes the attempt stale
// and the caller retries instead of publishing a signature that describes
// another hash.
func cacheCreditUserQuota(userID int, delta int64) (cacheQuotaResult, error) {
	if delta <= 0 || !common.RedisEnabled {
		return cacheQuotaMiss, nil
	}
	stripe := quotaLedgerStripe(userID)
	stripe.Lock()
	defer stripe.Unlock()
	return creditUserQuotaLocked(userID, delta)
}

// creditUserQuotaLocked is cacheCreditUserQuota with the stripe already held, so
// the row-first sync path can move the ledger without taking it twice.
func creditUserQuotaLocked(userID int, delta int64) (cacheQuotaResult, error) {
	for range quotaLedgerRetryAttempts {
		ledger, result, err := verifiedUserQuotaLedger(userID)
		if ledger == nil {
			return result, err
		}
		expiresAt := nextLedgerExpiry(ledger.expiresAt, userCacheTTLSeconds())
		nextMAC, err := (UserBase{
			Id: userID, QuotaCredited: ledger.credited + delta, QuotaSpent: ledger.spent,
			QuotaLedgerExp: expiresAt,
		}).quotaLedgerMAC()
		if err != nil {
			return cacheQuotaMiss, err
		}
		raw, evalErr := evalLedgerScript(userQuotaCreditScript, getUserCacheKey(userID), userQuotaLedgerArgs{
			Amount: ledgerDecimal(delta), Id: userID, Schema: ledger.schema, AuthVersion: ledger.authVersion,
			Credited: ledgerDecimal(ledger.credited), Spent: ledgerDecimal(ledger.spent),
			NextCredited: ledgerDecimal(ledger.credited + delta), NextSpent: ledgerDecimal(ledger.spent),
			NextExpiry: ledgerDecimal(expiresAt), MAC: ledger.mac, NextMAC: nextMAC,
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
// the balance the wallet holds as the credited total. A hash that already
// carries a signed ledger is left alone, so a cache fill never resets live
// spend.
//
// The balance it signs is not the raw row: under batch accumulation the row lags
// every spend the accumulator still owes it, and signing that row would publish
// a ledger authorizing quota already spent. The balance is therefore read as one
// instant — the row with the flush held off, plus the delta the accumulator
// still owes that row — and signed as the credited total with nothing spent.
//
// A negative balance is a wallet in debt and is carried as a negative credited
// total, so the seed reproduces the snapshot exactly and a later committed
// delta still lands on the right balance.
func seedUserQuotaLedger(userID int, quota int, authVersion int64) error {
	if !common.RedisEnabled {
		return nil
	}
	live, err := quotaBalanceHeldByAccumulator(userID, BatchUpdateTypeUserQuota, func() (int, error) {
		if !common.BatchUpdateEnabled {
			return quota, nil
		}
		return readUserQuotaRow(userID)
	})
	if err != nil {
		return err
	}
	expiresAt := nextLedgerExpiry(0, userCacheTTLSeconds())
	mac, err := (UserBase{
		Id: userID, QuotaCredited: int64(live), QuotaLedgerExp: expiresAt,
	}).quotaLedgerMAC()
	if err != nil {
		return err
	}
	_, err = evalLedgerScript(userQuotaLedgerSeedScript, getUserCacheKey(userID), userQuotaLedgerArgs{
		Id: userID, Schema: userCacheSchemaVersion, AuthVersion: authVersion,
		Credited: ledgerDecimal(int64(live)), Spent: ledgerDecimal(0),
		NextExpiry: ledgerDecimal(expiresAt), NextMAC: mac,
	})
	return err
}

// readUserQuotaRow reads the wallet balance the database holds.
func readUserQuotaRow(userID int) (int, error) {
	var balance int
	err := DB.Model(&User{}).Where("id = ?", userID).Select("quota").Find(&balance).Error
	return balance, err
}

// tokenQuotaLedgerSeedArgs builds the arguments that let cacheInitToken publish
// a token ledger in the same script that writes the snapshot. granted is the
// snapshot total and used the counter the seed signs; the script only runs while
// the hash still holds that counter, so an unflushed batch spend is neither lost
// nor signed under a value nobody verified.
func tokenQuotaLedgerSeedArgs(key string, id int, granted, used int64, ttlSeconds int) (string, int64, error) {
	if granted < 0 || used < 0 {
		return "", 0, fmt.Errorf("token %d holds a negative granted quota", id)
	}
	expiresAt := nextLedgerExpiry(0, ttlSeconds)
	mac, err := tokenQuotaLedgerMAC(key, id, granted, used, expiresAt)
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

// reserveUserQuotaDB writes a wallet reservation against the row, treating the
// delta the accumulator still owes that row as part of the balance the WHERE
// clause sees. A queued debit lowers what the row may fund and a queued credit
// raises it, so the comparison answers the balance the wallet holds rather than
// the balance the row happens to hold, and a credit is never a reason to refuse.
func reserveUserQuotaDB(id int, quota int, owed int) (bool, error) {
	result := DB.Model(&User{}).
		Where("id = ? AND quota + ? >= ?", id, owed, quota).
		Update("quota", gorm.Expr("quota - ?", quota))
	return result.RowsAffected == 1, result.Error
}

func reserveTokenQuotaDB(id int, quota int, owed int) (bool, error) {
	result := DB.Model(&Token{}).
		Where("id = ? AND remain_quota + ? >= ?", id, owed, quota).
		Updates(map[string]any{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		})
	return result.RowsAffected == 1, result.Error
}

// reserveQuotaHoldingAccumulator runs a row reservation with the accumulator for
// this kind held still, so a spend queued between the balance the WHERE clause
// sees and the write cannot slip through: the read of the owed delta and the
// update it authorizes are one critical section. The reserve callback is handed
// that owed delta, which it folds into the comparison.
//
// Both locks are part of that section, and they cover two different windows.
//
// The kind lock covers the check-then-act window. Queuing a delta takes only the
// kind lock, so if this released it before the write, a committed spend queued in
// between would be counted by the accumulator and again by the update, which is
// how a wallet of 100 came to pay 30+100.
//
// The flush lock covers the window after the rows are written and before the
// in-flight record is cleared. In that window batchUpdateInFlight still holds the
// delta the rows now carry, so the owed delta counts it twice: a wallet whose row
// already reached 150 while in-flight still holds its +50 reads as 200, and a
// reserve of 200 against a drained 150-row is accepted. The swap window needs no
// lock of its own — takeBatchUpdateStores records the in-flight map in the same
// critical section that empties the stores, so a mid-swap read of stores plus
// in-flight is already correct.
//
// The cost of holding both is one database round trip while the flush lock and
// this kind's lock are held, and the kind lock is process-wide for the kind
// rather than per subject. On the fallback path that stalls addNewRecord for
// every subject of this kind, and it stalls the flush ticker too, because
// batchUpdate takes the same flush lock. It is a latency coupling, not a
// correctness one: the reserve's own debit is queued after the update returns,
// so nothing is lost. Reading the owed delta and then issuing the conditional
// update without the kind lock is not an alternative — that reopens exactly the
// window above, only now with the spend landing mid-round-trip. Narrowing it
// would take a per-subject lock, which the accumulator does not have; the
// read path's seed takes the flush lock across a row read and pays the same
// coupling. The lock order is flush then kind, matching takeBatchUpdateStores,
// so nothing here can close a cycle with a flush.
func reserveQuotaHoldingAccumulator(kind int, subject int, reserve func(owed int) (bool, error)) (bool, error) {
	if !common.BatchUpdateEnabled {
		return reserve(0)
	}
	batchUpdateFlushLock.Lock()
	defer batchUpdateFlushLock.Unlock()
	batchUpdateLocks[kind].Lock()
	defer batchUpdateLocks[kind].Unlock()
	owed := batchUpdateStores[kind][subject] + batchUpdateInFlight[kind][subject]
	return reserve(owed)
}

// reserveUserQuotaFromRow and reserveTokenQuotaFromRow are the shorthand the
// reserve paths use: the row answers, with the accumulator held still.
func reserveUserQuotaFromRow(id int, quota int) (bool, error) {
	return reserveQuotaHoldingAccumulator(BatchUpdateTypeUserQuota, id,
		func(owed int) (bool, error) { return reserveUserQuotaDB(id, quota, owed) })
}

func reserveTokenQuotaFromRow(id int, quota int) (bool, error) {
	return reserveQuotaHoldingAccumulator(BatchUpdateTypeTokenQuota, id,
		func(owed int) (bool, error) { return reserveTokenQuotaDB(id, quota, owed) })
}

// quotaBalanceHeldByAccumulator answers the balance a subject holds right now,
// given a row read the caller supplies. It holds the flush lock across that
// read, so the row and the deltas the accumulator still owes it describe the
// same instant: a flush either has not swapped its deltas out yet — they are in
// the accumulator and not in the row — or it has written the rows and cleared
// them, so they are in the row and not in the accumulator. Reading the two
// without the lock is what lets a stale row authorize a spend.
//
// The wait this imposes on a flush is the length of one row read, and it is
// taken only when a balance outside the signed ledger is needed.
func quotaBalanceHeldByAccumulator(subject int, kind int, readRow func() (int, error)) (int, error) {
	if !common.BatchUpdateEnabled {
		return readRow()
	}
	batchUpdateFlushLock.Lock()
	defer batchUpdateFlushLock.Unlock()
	balance, err := readRow()
	if err != nil {
		return 0, err
	}
	return balance + unflushedQuotaDelta(subject, kind), nil
}

// unflushedQuotaDelta reports how much the batch accumulator still owes this
// subject's database row: everything queued since the last flush plus everything
// the flush swapped out and has not written yet.
func unflushedQuotaDelta(subject int, kind int) int {
	if !common.BatchUpdateEnabled {
		return 0
	}
	batchUpdateLocks[kind].Lock()
	defer batchUpdateLocks[kind].Unlock()
	return batchUpdateStores[kind][subject] + batchUpdateInFlight[kind][subject]
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
// the answer came from the database. A hash whose ledger a writer contradicted is
// discarded and answered from the database, never from zero: a zero balance would
// refuse a spend the account can pay for. A hash that carries no signed ledger at
// all is refused instead, because the row is not an answer for it.
func reserveUserQuotaCached(id int, quota int) (reserved bool, fromDB bool, err error) {
	// One writer per subject in this process. Without it, two goroutines holding
	// the same verified state race for the same CAS and the loser is answered as
	// contended, which the caller cannot retry away; with it, a write that is
	// going to land gets the first attempt, and the budget only absorbs a peer
	// replica (quotaLedgerRetryAttempts).
	stripe := quotaLedgerStripe(id)
	stripe.Lock()
	defer stripe.Unlock()
	hydrated := false
	contended := 0
	for range quotaLedgerRetryAttempts {
		result, reserveErr := cacheTryReserveUserQuota(id, int64(quota))
		switch {
		case errors.Is(reserveErr, ErrPreLedgerHashCarriesNoSignature):
			// A hash with no signed ledger cannot be spent from and must not be
			// answered from the row: under batch accumulation that row predates the
			// debit the accumulator still owes it. The hash is left in place for its
			// own TTL to rebuild, and the reservation is refused.
			return false, false, reserveErr
		case errors.Is(reserveErr, ErrQuotaLedgerUnverified) || result == cacheQuotaUnverified:
			discardUnverifiedUserQuotaCache(id, ledgerRefusalReason(reserveErr))
			reserved, err = reserveUserQuotaFromRow(id, quota)
			return reserved, true, err
		case errors.Is(reserveErr, errQuotaLedgerContended):
			// A transient: the wallet was never consulted, so answering it as
			// insufficient would report an empty balance that is not empty.
			contended++
			continue
		case reserveErr != nil:
			return false, false, reserveErr
		case result == cacheQuotaOK:
			return true, false, nil
		case result == cacheQuotaInsufficient:
			return false, false, nil
		}
		// The hash is absent or unusable, so the row has to answer. Republishing
		// from a row the accumulator has not written would sign a balance the wallet
		// does not hold, so try one hydrate while the hash can still be rebuilt; a
		// reservation that falls through to the row has the owed delta folded in.
		if hydrated {
			break
		}
		hydrated = true
		if _, hydrateErr := GetUserCache(id); hydrateErr != nil {
			return false, false, hydrateErr
		}
	}
	if contended > 0 {
		return false, false, errQuotaLedgerContended
	}
	// The hash could not be published: a fence blocks it, or the balance never
	// arrived. The row answers, with the accumulator's owed delta folded in, so a
	// spend the flush has not written yet still counts against this reservation.
	reserved, err = reserveUserQuotaFromRow(id, quota)
	return reserved, true, err
}

func reserveTokenQuotaCached(id int, key string, quota int) (reserved bool, fromDB bool, err error) {
	stripe := quotaLedgerStripe(id)
	stripe.Lock()
	defer stripe.Unlock()
	hydrated := false
	contended := 0
	for range quotaLedgerRetryAttempts {
		result, reserveErr := cacheTryReserveTokenQuota(id, key, int64(quota))
		switch {
		case errors.Is(reserveErr, ErrQuotaLedgerUnverified) || result == cacheQuotaUnverified:
			discardUnverifiedTokenQuotaCache(id, key, ledgerRefusalReason(reserveErr))
			reserved, err = reserveTokenQuotaFromRow(id, quota)
			return reserved, true, err
		case errors.Is(reserveErr, errQuotaLedgerContended):
			contended++
			continue
		case reserveErr != nil:
			return false, false, reserveErr
		case result == cacheQuotaOK:
			return true, false, nil
		case result == cacheQuotaInsufficient:
			return false, false, nil
		}
		// See the user twin: the ledger could not be published, so the row
		// answers, with the accumulator's owed delta folded in.
		if hydrated {
			break
		}
		hydrated = true
		if _, hydrateErr := GetTokenByKey(key, true); hydrateErr != nil {
			return false, false, hydrateErr
		}
	}
	if contended > 0 {
		return false, false, errQuotaLedgerContended
	}
	reserved, err = reserveTokenQuotaFromRow(id, quota)
	return reserved, true, err
}

// TryReserveUserQuota atomically checks and deducts a user's wallet quota.
// 缓存命中时以签名账本为准（避免批量模式下过期的数据库余额放大并发超扣）；
// Redis 异常或水合失败时降级为数据库条件更新。哈希里根本没有本版本签出的账本时
// 直接拒绝并返回 ErrPreLedgerHashCarriesNoSignature：它的余额只在未签名的 Quota
// 镜像里，批量模式下数据库行还是扣款前的值，回退到行会把已花的额度再花一次。
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
		return reserveUserQuotaFromRow(id, quota)
	}

	reserved, fromDB, err := reserveUserQuotaCached(id, quota)
	if fromDB {
		return reserved, err
	}
	if errors.Is(err, errQuotaLedgerContended) {
		// Falling back here is the over-spend the ledger exists to stop: the
		// wallet was never consulted, so the caller is told to try again.
		return false, err
	}
	if errors.Is(err, ErrPreLedgerHashCarriesNoSignature) {
		// The fallback would answer from a row the accumulator has not reached yet.
		// The hash recorded the spend and its signature is gone, so the row is a
		// pre-spend balance; refusing is the only answer that cannot hand quota back.
		return false, err
	}
	if err != nil {
		// A Redis that answers LOADING or times out is the one moment the
		// accumulator is the only record of a committed spend, so this exit needs
		// the accumulator held still more than the others, not less.
		common.SysLog(common.LogText("user quota cache reserve unavailable, answering from the database: %s", err.Error()))
		return reserveUserQuotaFromRow(id, quota)
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
		return reserveTokenQuotaFromRow(id, quota)
	}

	reserved, fromDB, err := reserveTokenQuotaCached(id, key, quota)
	if fromDB {
		return reserved, err
	}
	if errors.Is(err, errQuotaLedgerContended) {
		return false, err
	}
	if err != nil {
		// See the user twin: a Redis that is down is the moment the accumulator is
		// the only record of a committed spend.
		common.SysLog(common.LogText("token quota cache reserve unavailable, answering from the database: %s", err.Error()))
		return reserveTokenQuotaFromRow(id, quota)
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
