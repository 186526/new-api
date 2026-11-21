package model

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const (
	BatchUpdateTypeUserQuota = iota
	BatchUpdateTypeTokenQuota
	BatchUpdateTypeUsedQuota
	BatchUpdateTypeChannelUsedQuota
	BatchUpdateTypeRequestCount
	BatchUpdateTypeCount // if you add a new type, you need to add a new map, a new lock and a name
)

var batchUpdateStores []map[int]int
var batchUpdateInFlight []map[int]int
var batchUpdateLocks []sync.Mutex

// batchUpdateFlushLock serialises whole flushes. Each accumulator already has its
// own lock for the swap, but two concurrent flushes would still split one batch in
// two, so the shutdown flush holds this lock for the drain and the write and cannot
// return while the periodic ticker is still writing.
var batchUpdateFlushLock sync.Mutex

// batchUpdateTypeNames labels each accumulator in the flush log, so a restart can
// be audited without reading this file.
var batchUpdateTypeNames = [BatchUpdateTypeCount]string{
	BatchUpdateTypeUserQuota:        "user quota",
	BatchUpdateTypeTokenQuota:       "token quota",
	BatchUpdateTypeUsedQuota:        "used quota",
	BatchUpdateTypeChannelUsedQuota: "channel used quota",
	BatchUpdateTypeRequestCount:     "request count",
}

// detachedQuotaWork counts goroutines that queue accumulator deltas after the
// request that started them has returned, so shutdown can wait for them before it
// drains. BillingSession.Refund is the one such path: it returns as soon as it has
// marked the session refunded, and its goroutine credits the wallet and token a
// scheduling delay later. A shutdown that flushed without waiting would write the
// debit and drop the matching credit, charging the user for a request that failed.
var detachedQuotaWork = &quotaWorkTracker{}

// quotaWorkTracker counts pending detached quota work without sync.WaitGroup,
// because Add can legitimately race Wait here: a hijacked WebSocket relay can call
// Refund after shutdown has started waiting. The channel is replaced rather than
// recreated in place, so a waiter that took the old channel is never closed twice.
type quotaWorkTracker struct {
	mu      sync.Mutex
	pending int
	idle    chan struct{} // non-nil while pending > 0; closed when it reaches 0
}

func (t *quotaWorkTracker) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == 0 {
		t.idle = make(chan struct{})
	}
	t.pending++
}

func (t *quotaWorkTracker) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending--
	if t.pending == 0 {
		close(t.idle)
		t.idle = nil
	}
}

// waitUntilIdle returns the number of goroutines it found pending and whether they
// all finished before timeout. Work started after this call is not covered: the
// guarantee is "everything already running when shutdown began", which is what the
// caller can honestly claim.
func (t *quotaWorkTracker) waitUntilIdle(timeout time.Duration) (int, bool) {
	t.mu.Lock()
	if t.pending == 0 {
		t.mu.Unlock()
		return 0, true
	}
	pending := t.pending
	idle := t.idle
	t.mu.Unlock()

	select {
	case <-idle:
		return pending, true
	case <-time.After(timeout):
		return pending, false
	}
}

// BatchUpdateFlushSummary is what one flush drained from one accumulator: how many
// ids it held, how many of those the database accepted, and the sum of all their
// deltas. The sum saturates at the int64 extremes instead of wrapping, because
// addNewRecord already saturates a single id at the int extremes and two such ids
// added together would otherwise report a small total for rows that each received a
// huge one.
type BatchUpdateFlushSummary struct {
	Type     string
	Entries  int
	Accepted int
	Delta    int64
}

// accumulateFlushDelta adds value to sum and saturates at the int64 extremes on
// overflow, the way addNewRecord saturates a single id at the int extremes.
func accumulateFlushDelta(sum int64, value int) int64 {
	total := sum + int64(value)
	if (value > 0 && total < sum) || (value < 0 && total > sum) {
		if value > 0 {
			return math.MaxInt64
		}
		return math.MinInt64
	}
	return total
}

func init() {
	for range BatchUpdateTypeCount {
		batchUpdateStores = append(batchUpdateStores, make(map[int]int))
		batchUpdateInFlight = append(batchUpdateInFlight, make(map[int]int))
		batchUpdateLocks = append(batchUpdateLocks, sync.Mutex{})
	}
}

// GoDetachedQuotaWork runs fn in a background goroutine that queues accumulator
// deltas, and registers it so WaitDetachedQuotaWork can wait for it before the
// shutdown flush. Callers are the paths where the request has already returned by
// the time the delta is queued; a delta queued that way is otherwise invisible to
// shutdown and is dropped when the process exits.
func GoDetachedQuotaWork(fn func()) {
	detachedQuotaWork.start()
	gopool.Go(func() {
		defer detachedQuotaWork.finish()
		fn()
	})
}

// WaitDetachedQuotaWork blocks until every detached quota goroutine that was running
// when it was called has finished, or timeout expires, and reports whether it ran
// out. It must be called before FlushBatchUpdates: waiting afterwards would leave
// what those goroutines queued in the accumulator again.
func WaitDetachedQuotaWork(timeout time.Duration) (pending int, finished bool) {
	return detachedQuotaWork.waitUntilIdle(timeout)
}

func InitBatchUpdater() {
	gopool.Go(func() {
		for {
			time.Sleep(time.Duration(common.BatchUpdateInterval) * time.Second)
			batchUpdate()
		}
	})
}

// FlushBatchUpdates drains every accumulator and writes it, then logs one line per
// accumulator that held data so a restart can be audited. The shutdown sequence
// calls it after the listener closes, otherwise the debits accumulated since the
// last tick are dropped with the process and a scale-down silently loses them.
//
// It drains this process's accumulator only. Two replicas each hold their own maps,
// so flushing one replica writes that replica's debits and leaves the other's to its
// own ticker; it does not write a peer's pending deltas.
func FlushBatchUpdates() []BatchUpdateFlushSummary {
	summaries := batchUpdate()
	if len(summaries) == 0 {
		common.SysLog(common.LogText("batch update flush: no accumulated deltas to write"))
		return nil
	}
	for _, summary := range summaries {
		common.SysLog(common.LogText("batch update flush wrote %d of %d entries of %s, total delta %d", summary.Accepted, summary.Entries, summary.Type, summary.Delta))
	}
	return summaries
}

func addNewRecord(type_ int, id int, value int) {
	batchUpdateLocks[type_].Lock()
	defer batchUpdateLocks[type_].Unlock()
	addToStoreLocked(type_, id, value)
}

// addToStoreLocked folds value into the accumulator for this id. The caller holds
// this kind's lock.
func addToStoreLocked(type_ int, id int, value int) {
	old, ok := batchUpdateStores[type_][id]
	if !ok {
		batchUpdateStores[type_][id] = value
		return
	}

	sum := old + value
	if (value > 0 && sum < old) || (value < 0 && sum > old) {
		common.SysError(common.LogText("batch update overflow: type=%d id=%d old=%d value=%d", type_, id, old, value))
		if value > 0 {
			sum = math.MaxInt
		} else {
			sum = math.MinInt
		}
	}
	batchUpdateStores[type_][id] = sum
}

// flushWriteAttempts bounds how many times one flush writes before it gives up and
// leaves the remaining deltas to the next tick. A write that fails for a persistent
// reason would otherwise keep the flush lock held while it retried the same rows.
const flushWriteAttempts = 3

// batchUpdate drains every accumulator and writes what it drained, reporting one
// summary per accumulator that held data. Callers are serialised by
// batchUpdateFlushLock: two concurrent callers would otherwise each drain a
// different half and a shutdown flush could return while the periodic ticker was
// still writing the half it took.
func batchUpdate() []BatchUpdateFlushSummary {
	batchUpdateFlushLock.Lock()
	defer batchUpdateFlushLock.Unlock()

	// The swap is also the only place that records what it took as in flight, in the
	// same critical section that empties the accumulators, so no delta is ever in
	// neither the queue nor the record while its row still lags.
	stores, ok := takeBatchUpdateStores()
	if !ok {
		return nil
	}
	common.SysLog(common.LogText("batch update started"))
	accepted := make([]int, BatchUpdateTypeCount)
	unwritten := stores
	for attempt := 0; attempt < flushWriteAttempts && !emptyDeltaStores(unwritten); attempt++ {
		unwritten = writeBatchUpdateStoresInto(unwritten, accepted)
	}
	// Whatever still failed goes back into the queue, so the next flush and the
	// reservation check both still see it owed to its row. Both this and the clear
	// below happen under the flush lock, so their order is not observable to a
	// reader: reserveQuotaHoldingAccumulator holds the same lock across its read of
	// stores plus in-flight.
	requeueUnwrittenDeltas(unwritten)
	clearBatchUpdateInFlight()
	common.SysLog(common.LogText("batch update finished"))
	return summarizeBatchUpdateStores(stores, accepted)
}

// summarizeBatchUpdateStores builds one summary per accumulator that held entries,
// describing what the flush found and how much of it the database accepted.
func summarizeBatchUpdateStores(stores []map[int]int, accepted []int) []BatchUpdateFlushSummary {
	summaries := make([]BatchUpdateFlushSummary, 0, BatchUpdateTypeCount)
	for i, store := range stores {
		if len(store) == 0 {
			continue
		}
		summary := BatchUpdateFlushSummary{Type: batchUpdateTypeNames[i], Entries: len(store), Accepted: accepted[i]}
		for _, value := range store {
			summary.Delta = accumulateFlushDelta(summary.Delta, value)
		}
		summaries = append(summaries, summary)
	}
	return summaries
}

// emptyDeltaStores reports whether every per-accumulator map has no entries.
func emptyDeltaStores(stores []map[int]int) bool {
	for _, store := range stores {
		if len(store) > 0 {
			return false
		}
	}
	return true
}

// takeBatchUpdateStores swaps the accumulators out and reports whether there was
// anything to flush. The swapped-out maps are recorded as in flight in the same
// critical section that empties them, so no reader observes an empty accumulator
// and an empty in-flight record while the rows are still owed.
func takeBatchUpdateStores() ([]map[int]int, bool) {
	hasData := false
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		if len(batchUpdateStores[i]) > 0 {
			hasData = true
			batchUpdateLocks[i].Unlock()
			break
		}
		batchUpdateLocks[i].Unlock()
	}
	if !hasData {
		return nil, false
	}
	stores := make([]map[int]int, BatchUpdateTypeCount)
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		stores[i] = batchUpdateStores[i]
		batchUpdateInFlight[i] = stores[i]
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	return stores, true
}

// writeBatchUpdateStores writes the swapped-out maps to their rows once and reports
// one summary per accumulator that held data. It is the single-attempt shape the
// tests drive directly; a delta whose write failed is queued again, so a caller
// reading the accumulators afterwards still sees it owed.
func writeBatchUpdateStores(stores []map[int]int) []BatchUpdateFlushSummary {
	accepted := make([]int, BatchUpdateTypeCount)
	requeueUnwrittenDeltas(writeBatchUpdateStoresInto(stores, accepted))
	return summarizeBatchUpdateStores(stores, accepted)
}

// writeBatchUpdateStoresInto writes the swapped-out maps to their rows, adding each
// accepted entry to accepted, and returns the deltas whose write failed for a reason
// that may pass. An entry whose row no longer exists is counted as neither, because
// no retry can apply it.
func writeBatchUpdateStoresInto(stores []map[int]int, accepted []int) []map[int]int {
	failed := make([]map[int]int, BatchUpdateTypeCount)
	for i := range failed {
		failed[i] = make(map[int]int)
	}
	for i, store := range stores {
		if i == BatchUpdateTypeUserQuota || i == BatchUpdateTypeUsedQuota || i == BatchUpdateTypeRequestCount {
			continue
		}
		for key, value := range store {
			if value == 0 {
				// Nothing to add, so no write can fail and nothing can be lost.
				accepted[i]++
				continue
			}
			switch i {
			case BatchUpdateTypeTokenQuota:
				err := increaseTokenQuota(key, value)
				if err != nil {
					common.SysLog(common.LogText("failed to batch update token quota: %s", err.Error()))
					if errors.Is(err, gorm.ErrRecordNotFound) {
						// The token is gone. Requeueing would retry a write no row can accept.
						continue
					}
					recordUnwrittenDelta(failed[i], key, value, err)
					continue
				}
			case BatchUpdateTypeChannelUsedQuota:
				if err := updateChannelUsedQuota(key, value); err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						// The channel is gone. Requeueing would retry a write no row can accept.
						continue
					}
					recordUnwrittenDelta(failed[i], key, value, err)
					continue
				}
			}
			accepted[i]++
		}
	}

	userQuotaStore := stores[BatchUpdateTypeUserQuota]
	usedQuotaStore := stores[BatchUpdateTypeUsedQuota]
	requestCountStore := stores[BatchUpdateTypeRequestCount]

	userIDs := make(map[int]struct{}, len(userQuotaStore)+len(usedQuotaStore)+len(requestCountStore))
	for key := range userQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range usedQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range requestCountStore {
		userIDs[key] = struct{}{}
	}
	userStoreTypes := [...]int{BatchUpdateTypeUserQuota, BatchUpdateTypeUsedQuota, BatchUpdateTypeRequestCount}
	for key := range userIDs {
		err := updateUserQuotaUsedQuotaAndRequestCount(key, userQuotaStore[key], usedQuotaStore[key], requestCountStore[key])
		if err != nil {
			// The one write carries all three deltas for this id, so a failure holds
			// back all three rather than an arbitrary subset of them.
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// The user is gone. Requeueing would retry a write no row can accept.
				continue
			}
			for _, i := range userStoreTypes {
				if delta, ok := stores[i][key]; ok {
					recordUnwrittenDelta(failed[i], key, delta, err)
				}
			}
			continue
		}
		// One write covers the three user accumulators, so it accepts the entry of
		// whichever of them held this id.
		for _, i := range userStoreTypes {
			if _, ok := stores[i][key]; ok {
				accepted[i]++
			}
		}
	}

	return failed
}

// recordUnwrittenDelta remembers a delta whose row write failed for a reason a later
// flush might not hit. It is logged at error level rather than log level: the delta
// stays owed to its row until a flush writes it, so a database that keeps refusing
// the write keeps this loop alive instead of silently discarding the delta.
func recordUnwrittenDelta(unwritten map[int]int, id int, value int, err error) {
	common.SysError(common.LogText("batch update will retry delta for id %d after a failed write: %s", id, err.Error()))
	unwritten[id] = value
}

// requeueUnwrittenDeltas returns the deltas whose write failed to the accumulators
// they came from, leaving the in-flight record as the reader sees it. A delta is
// either in the queue or in the in-flight record until a flush writes it; dropping
// this requeue would erase it from both on a write error, and the reservation check
// would then answer from a row that never received it.
//
// It assumes the accumulator belongs to the database that was installed when the
// delta was queued, and requeues without re-checking that. The accumulators are
// process-global and keyed by (kind, id), so a caller that swaps the database under
// them inherits any delta this leaves behind, and the id may name an unrelated row
// there. Nothing in the process does that - the database is installed once at start
// up - so the ownership stays with whoever swaps it: that caller must flush before
// it swaps and then clear the accumulators unconditionally, including when the flush
// could not write.
func requeueUnwrittenDeltas(unwritten []map[int]int) {
	for i, store := range unwritten {
		if len(store) == 0 {
			continue
		}
		batchUpdateLocks[i].Lock()
		for id, value := range store {
			addToStoreLocked(i, id, value)
		}
		batchUpdateLocks[i].Unlock()
	}
}

// clearBatchUpdateInFlight forgets the rows the flush just wrote. Until this
// runs, the swapped-out maps are the only record that those rows still lag, so
// a reservation in that window is refused rather than answered from a balance
// the flush has not published yet.
func clearBatchUpdateInFlight() {
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		batchUpdateInFlight[i] = nil
		batchUpdateLocks[i].Unlock()
	}
}

// ClearBatchUpdateAccumulators drops every queued delta and the in-flight record.
// It belongs to a caller that replaces the database under the accumulators: their
// entries are keyed by (kind, id) and describe rows in the database that was
// installed when they were queued, so a swap must flush first and then call this,
// even when the flush could not write. Nothing in a running process does that - the
// database is installed once at start up - so this exists for the tests that put a
// fresh database under each case.
func ClearBatchUpdateAccumulators() {
	batchUpdateFlushLock.Lock()
	defer batchUpdateFlushLock.Unlock()
	for i := range BatchUpdateTypeCount {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateInFlight[i] = nil
		batchUpdateLocks[i].Unlock()
	}
}

func RecordExist(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

func shouldUpdateRedis(fromDB bool, err error) bool {
	return common.RedisEnabled && fromDB && err == nil
}
