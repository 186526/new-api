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
	BatchUpdateTypeCount // if you add a new type, you need to add a new map and a new lock
)

var batchUpdateStores []map[int]int
var batchUpdateInFlight []map[int]int
var batchUpdateLocks []sync.Mutex
var batchUpdateFlushLock sync.Mutex

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

func batchUpdate() {
	// One flush at a time, so the in-flight record below describes exactly one
	// swap-and-write and never overlaps another flush's rows.
	batchUpdateFlushLock.Lock()
	defer batchUpdateFlushLock.Unlock()

	stores, ok := takeBatchUpdateStores()
	if !ok {
		return
	}
	common.SysLog(common.LogText("batch update started"))
	writeBatchUpdateStores(stores)
	clearBatchUpdateInFlight()
	common.SysLog(common.LogText("batch update finished"))
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

// writeBatchUpdateStores writes the swapped-out maps to their rows.
func writeBatchUpdateStores(stores []map[int]int) {
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
}

// FlushBatchUpdates writes every accumulated delta to its row now. The periodic
// updater calls this on its own schedule; an explicit call is for a caller that
// needs the rows current before the next tick, and for shutting down without
// leaving a delta unwritten.
func FlushBatchUpdates() {
	batchUpdate()
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
