# Process Lifecycle Rules

Role split and graceful shutdown for the Go binary. Touched by `common/constants.go`, `common/init.go`, `router/api-router.go`, `router/main.go`, `router/relay-router.go`, `main.go`, `middleware/draining.go`, `model/utils.go`, and `service/billing_session.go`.

## Role split

- `SERVER_ROLE` is `all` (default), `admin`, or `proxy`. `REGION` names the region a proxy serves.
- The admin registers every route except the relay routes. A proxy registers the public `/api` routes and the relay routes, and answers everything else with the relay 404.
- Relay-only background work (quota data, perf metrics) runs on a proxy. Management loops run on the admin: channel refresh, Command Code, Antigravity, Mistral, subscription reset, credential refresh, auth cleanup.
- A proxy without `REGION`, or an unknown role, stops at startup.

## Shutdown sequence

On SIGTERM:

- The process marks itself draining and `/healthz` answers 503 so the pod leaves the Service endpoints.
- New relay requests are refused with 503 while requests already in flight keep running.
- The listener closes after `SHUTDOWN_DRAIN_DELAY_SECONDS` (default 10), with `SHUTDOWN_TIMEOUT_SECONDS` (default 120) to finish them.
- The deployment probes `/healthz` for readiness and sets a 300-second grace period.

After `Shutdown` returns, `main.go` first calls `model.WaitDetachedQuotaWork` and only then `model.FlushBatchUpdates` when `BATCH_UPDATE_ENABLED` is on. The order MUST NOT be reversed. The instance reporter then cancels and joins its loop before removing its own row, with one 5-second budget for the join and unregister.

System Info uses `POD_UID` as the instance key and displays `POD_NAME` and `POD_HOST_NODE_NAME` from the Downward API. This does not change the global `NodeName`. Unregister matches the process ID as well as the instance key. Admin/all deletes expired temporary rows every 30 seconds; their default lifetime is 180 seconds (`SYSTEM_INSTANCE_AUTO_EXPIRE_SECONDS`). Manual bulk cleanup calls `DELETE /api/system-info/expired-instances` and only deletes rows whose positive expiry has elapsed. The dashboard does not fall back when an older backend lacks this route. The current server keeps `DELETE /api/system-info/stale-instances` as a compatibility alias with the same restricted behavior, but older backends used the last heartbeat for that route. Rows without an expiry can still be reviewed and deleted individually once stale. `SYSTEM_INSTANCE_REPORTER_ENABLED=false` stops this server's heartbeat but does not stop cleanup or scheduled tasks. Upgrade the migration-running admin before proxies use the new instance columns.

## Why the refund wait comes first

- The wait covers goroutines that queue a delta after the request that started them has returned. This is how `BillingSession.Refund` credits a failed request: it marks the session refunded and returns, and its goroutine calls `IncreaseUserQuota`/`IncreaseTokenQuota` a scheduling delay later.
- `Refund` registers that goroutine through `model.GoDetachedQuotaWork`, so a shutdown that flushed first would drop the queued token-quota refund and the matching usage counters — a failed request would still spend the user's token budget.
- On the deployed proxy the wallet is not on this path: it runs `REGION_LEASE_ENABLED=true` (`deploys/31-new-api-proxy.yaml:91`), and while `RegionEnabled()` is true `IncreaseUserQuota`, `DecreaseUserQuota`, and `TryReserveUserQuota` enter the region lease before they can reach `addNewRecord`. The wallet exposure at shutdown is therefore the region-lease refund race, which this wait covers (the whole `Refund` closure is tracked) and the flush does not.
- The wait is bounded by `SHUTDOWN_REFUND_WAIT_SECONDS` (default 10, an inner bound inside the deployment's 240 s drain budget). On expiry it logs the pending count and the flush still runs.

## Flush contents and logging

- The flush writes the quota deltas queued before it on the routes the server waits for.
- It logs one line per accumulator that held data: `batch update flush wrote <accepted> of <drained> entries of <name>, total delta <delta>`, and one line when there was nothing to write.
- The accepted count comes from the write itself: `updateChannelUsedQuota` and `updateUserQuotaUsedQuotaAndRequestCount` return `gorm.ErrRecordNotFound` when `RowsAffected != 1` (a deleted channel or user), and `increaseTokenQuota` already did. A drained delta no row accepted therefore shows as accepted fewer than drained rather than as written.
- The reported total saturates at the int64 extremes instead of wrapping, because `addNewRecord` saturates one id at the int extremes and two such ids would otherwise sum to a small negative number.
- It drains this process's accumulator only. Two replicas keep separate maps, so flushing one replica writes its own deltas and leaves the peer's to that peer's ticker. Because all three write paths are relative SQL increments, a flush and a peer's ticker never overwrite each other.
- The periodic ticker and the shutdown flush share `batchUpdateFlushLock`, held across both the drain and the write, so the flush cannot return while the ticker is still writing a batch it already drained.

## Outside the guarantee

Two cases sit outside the flush guarantee, and in both a delta queued after the flush is lost:

- A `Shutdown` timeout leaves a handler free to queue one.
- A hijacked WebSocket relay (`RelayFormatOpenAIRealtime` and the responses WebSocket route) is neither closed nor awaited by `net/http` and settles its quota at session end from that hijacked goroutine. Such a session can also call `Refund` after the wait returned, which the wait does not cover because it only awaits what was already running.

An ordinary failed request, the case that used to leave a debit written and its credit dropped, is no longer in that set.

## Mistral quota loop

`main` also waits for the Mistral quota loop before it returns: the deferred stop cancels that loop and waits for it to exit, so a tick still inside the pause between two accounts is joined rather than left running past the process.

- The pause is a `time.NewTimer(common.RequestInterval)` selected against `ctx.Done()`, so a cancellation ends it where it stands instead of after the interval.
- That wait is one `RequestInterval` at most, not one per account, because the loop checks `ctx.Err()` at the top of every account and `Fetch` takes the context.
- The deferred stop runs after the flush, which drops no delta: the loop writes a channel balance directly instead of queueing into the batch accumulator.
