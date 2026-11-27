# Region Quota Leases

Per-region wallet quota leases and the processes that grant, report and fold spend. Code: `model/region_pool.go`, `model/quota_reserve.go`, `model/user.go`, `model/user_cache.go`, `model/main.go`, `service/region_pool.go`, `controller/region_pool.go`, `pkg/metrics/region_pool.go`.

## Enablement and lease accounting

- `REGION_LEASE_ENABLED` enables leases; `REGION` alone only labels the instance.
- Token quota is a per-token cap, not money, so it stays on shared Redis accounting.
- Region mode turns the trust bypass off; wallet checks never fall back to the database.
- Local lease only: `Available = Granted − Reported − localSpent`, with `Σ outstanding ≤ central balance`. A dry spend asks the admin for another chunk.
- Spend reports `TotalSpent − LastReported`, deduplicated by sequence number in `region_quota_events`.
- `region:<region>:active` marks subjects that spent; `region:<region>:dirty` those needing a top-up.

## Shared-Redis ledger signature

The non-region path signs `Id`, `QuotaCredited`, `QuotaSpent` and the expiry. `AuthVersion` and `CacheSchema` stay outside the MAC: `IncrementUserAuthVersionWithTx` (password, 2FA, passkey) and `writeUserCache` (every metadata write) change them without touching the ledger, so signing either breaks a live ledger and sends the next reservation to the pre-debit row. The metadata MAC still covers the version and scripts compare both with the caller's state, so neither is forgeable.

- Layout comes from the signature alone (`carriesSignedLedger` on read, `userLedgerFieldsCarryASignature` on reservation), never `CacheSchema` or a non-empty `QuotaLedgerMAC`, which a Redis-only writer can set. The reservation checks before parsing; later fields pass `requireSignedLayoutInt`.
- A hash missing `Id`, `CacheSchema`, `AuthVersion`, `QuotaCredited`, `QuotaSpent` or `QuotaLedgerExp`, or with zeroed `Id`/`AuthVersion`, is refused rather than parsed into a miss, since `ErrQuotaLedgerUnverified` would reserve from the pre-debit row (`TestReserveRefusesASignedLedgerWithADeletedField`). A zeroed `CacheSchema` is not refused; that hash still spends from the signed ledger, never the row.
- No signed ledger: `ErrPreLedgerHashCarriesNoSignature`; only the read path rebuilds it, signing the accumulator's balance.

## Token ledger mirror

`tokenQuotaLedgerMAC` signs `Id`, `QuotaGranted`, `UsedQuota` and the expiry — every field a token reservation reads. The stored `Id` is the one unsigned layout field: `tokenQuotaLedgerFieldsCarryASignature` rebuilds the MAC from the caller's `id`, so `HSET Id 0` passes layout and only `cachedId != id` rejects it. The `CacheSchema` caveat is user-side only.

- Same rule before any parse, then `requireSignedTokenLayoutInt`; `reserveTokenQuotaCached` and `TryReserveTokenQuota` refuse damaged or unsigned hashes with `ErrPreLedgerTokenHashCarriesNoSignature` instead of reading the row (`TestTokenReserveRefusesASignedLedgerWithADeletedField`, `TestTokenReserveRefusesAHashWithNoSignedLedger`).
- `UsedQuota` may be negative: `IncreaseTokenQuota` refunds by lowering `used_quota`, so a refund never reaching the row leaves remain 100 with used −5, and the seed signs that counter (`TestTokenWithANegativeUsedCounterIsSpendableAfterARepublish`, `TestTokenReleaseBelowAZeroCounterStillCompensates`).
- Both write scripts accept that sign; header-only acceptance made a token holding 100 refuse a reservation of 1 and accept 5. `tokenQuotaReserveScript` refuses only an unparseable counter or a spend past `QuotaGranted`; `tokenQuotaReleaseScript` only an unparseable counter, so a compensation release returns `cacheQuotaOK`, not `-3`.

## Token read path and repair

- No `QuotaLedgerMAC`: `cacheGetTokenByKey` reports stale, `GetTokenByKey` reloads the row, `cacheInitToken` republishes, and the read answers from the republished ledger, not the pre-debit counters (`TestTokenReadRepairsASignedLedgerWithADeletedSignature`).
- MAC present but a covered field lost: `ErrDamagedTokenQuotaLedger` (wrapping `ErrPreLedgerTokenHashCarriesNoSignature`), not republished; the seed signs only MAC-less hashes, so the mirror answer stands until the TTL ends.
- Bounded by `granted := remain + used` in `cacheInitToken` plus the seed's `HGET UsedQuota == args.seed.used`, so a writer's worst case equals deleting the key: `HDEL QuotaLedgerMAC` signs `granted` 100 and answers 40; with `HSET UsedQuota 0` signs 100 and answers 100; `HSET RemainQuota 1000000` still signs 100; `DEL` matches.
- A landing repair renews the hash to its signature's window; a declining one leaves the TTL (`TestARepairedTokenHashOutlivesTheRowCatchingUp`, `TestDamageTokenHashKeepsItsOwnTTLSoTheRowCanCatchUp`).
- A genuine signature past its window answers from the row plus the accumulator's owed delta; `expireSignedTokenLedger` builds that state.
- `cacheInitToken` fills a fresh `UsedQuota` from `args.seed.used`, the snapshot raised by the delta the accumulator owes, not the snapshot value; the row value made the seed decline and publish an unsigned hash, refused until its TTL, then seeded from the flushed row.

## Region pool processes

- Proxy reports every 15 seconds, persisting the batch first; admin tops up leases every 60 seconds and folds reported spend into the central balance every 30 seconds.
- `/api/internal/region/lease` and `/api/internal/region/report` require `X-Region-Token` (`REGION_INTERNAL_TOKEN`) and register only with `REGION` set.
- `newapi_region_pool_*` exports available, outstanding, unreported, exhausted, grants, reports and report lag.
