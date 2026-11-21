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

// BatchUpdateFlushSummary is what one flush drained from one accumulator: how many
// ids it held and the sum of their deltas. Delta is int64 so a store holding the
// extremes of int still sums without wrapping.
type BatchUpdateFlushSummary struct {
	Type    string
	Entries int
	Delta   int64
}

func init() {
	for range BatchUpdateTypeCount {
		batchUpdateStores = append(batchUpdateStores, make(map[int]int))
		batchUpdateInFlight = append(batchUpdateInFlight, make(map[int]int))
		batchUpdateLocks = append(batchUpdateLocks, sync.Mutex{})
	}
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
		common.SysLog(common.LogText("batch update flush wrote %d entries of %s, total delta %d", summary.Entries, summary.Type, summary.Delta))
	}
	return summaries
}

func addNewRecord(type_ int, id int, value int) {
	batchUpdateLocks[type_].Lock()
	defer batchUpdateLocks[type_].Unlock()
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
	summaries := writeBatchUpdateStores(stores)
	clearBatchUpdateInFlight()
	common.SysLog(common.LogText("batch update finished"))
	return summaries
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

// writeBatchUpdateStores writes the swapped-out maps to their rows and reports one
// summary per accumulator that held data.
func writeBatchUpdateStores(stores []map[int]int) []BatchUpdateFlushSummary {
	summaries := make([]BatchUpdateFlushSummary, 0, BatchUpdateTypeCount)
	for i, store := range stores {
		if len(store) == 0 {
			continue
		}
		summary := BatchUpdateFlushSummary{Type: batchUpdateTypeNames[i], Entries: len(store)}
		for _, value := range store {
			summary.Delta += int64(value)
		}
		summaries = append(summaries, summary)
	}

	for i, store := range stores {
		if i == BatchUpdateTypeUserQuota || i == BatchUpdateTypeUsedQuota || i == BatchUpdateTypeRequestCount {
			continue
		}
		for key, value := range store {
			switch i {
			case BatchUpdateTypeTokenQuota:
				err := increaseTokenQuota(key, value)
				if err != nil {
					common.SysLog(common.LogText("failed to batch update token quota: %s", err.Error()))
				}
			case BatchUpdateTypeChannelUsedQuota:
				updateChannelUsedQuota(key, value)
			}
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
	for key := range userIDs {
		updateUserQuotaUsedQuotaAndRequestCount(key, userQuotaStore[key], usedQuotaStore[key], requestCountStore[key])
	}
	return summaries
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
