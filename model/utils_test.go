package model

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findFlushSummary(summaries []BatchUpdateFlushSummary, typeName string) (BatchUpdateFlushSummary, bool) {
	for _, summary := range summaries {
		if summary.Type == typeName {
			return summary, true
		}
	}
	return BatchUpdateFlushSummary{}, false
}

func getUserUsageFromDB(t *testing.T, id int) (usedQuota int, requestCount int) {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("used_quota", "request_count").First(&user, id).Error)
	return user.UsedQuota, user.RequestCount
}

func TestFlushBatchUpdatesWritesTheAccumulatorOnce(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 100)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -60)
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "the debit must still be in the accumulator before the flush")

	summaries := FlushBatchUpdates()
	require.Len(t, summaries, 1)
	summary, ok := findFlushSummary(summaries, batchUpdateTypeNames[BatchUpdateTypeUserQuota])
	require.True(t, ok, "the flush must report the accumulator it drained")
	assert.Equal(t, 1, summary.Entries)
	assert.Equal(t, int64(-60), summary.Delta)
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id), "the shutdown flush must write the debit it drained")

	assert.Empty(t, FlushBatchUpdates(), "a second flush has nothing left to write")
	assert.Equal(t, 40, getUserQuotaFromDB(t, user.Id), "a second flush must not write the debit again")
}

func TestFlushBatchUpdatesReportsEveryDrainedAccumulator(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 1000)
	token := createReserveTestToken(t, 100)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -30)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 30)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -30)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, 77, 30)

	summaries := FlushBatchUpdates()
	require.Len(t, summaries, BatchUpdateTypeCount)
	for i := range BatchUpdateTypeCount {
		summary, ok := findFlushSummary(summaries, batchUpdateTypeNames[i])
		require.True(t, ok, "accumulator %d must be reported", i)
		assert.Equal(t, 1, summary.Entries)
	}

	assert.Equal(t, 970, getUserQuotaFromDB(t, user.Id))
	usedQuota, requestCount := getUserUsageFromDB(t, user.Id)
	assert.Equal(t, 30, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestFlushBatchUpdatesIsEmptyWithoutAccumulatedDeltas(t *testing.T) {
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	assert.Empty(t, FlushBatchUpdates(), "a process with nothing accumulated writes nothing")
}

// A value queued while the periodic ticker is flushing must reach the database
// exactly once, whichever flush drains it.
func TestConcurrentTickerFlushAndShutdownFlushWriteEachDeltaOnce(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	user := createReserveTestUser(t, 1000)
	const debit = 70
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -debit)

	var wg sync.WaitGroup
	fromTicker := make(chan []BatchUpdateFlushSummary, 1)
	fromShutdown := make(chan []BatchUpdateFlushSummary, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		fromTicker <- batchUpdate()
	}()
	go func() {
		defer wg.Done()
		fromShutdown <- FlushBatchUpdates()
	}()
	wg.Wait()
	close(fromTicker)
	close(fromShutdown)

	assert.Equal(t, 1000-debit, getUserQuotaFromDB(t, user.Id), "the concurrent flush must write the debit once")

	var entries int
	var delta int64
	for _, summaries := range [][]BatchUpdateFlushSummary{<-fromTicker, <-fromShutdown} {
		if summary, ok := findFlushSummary(summaries, batchUpdateTypeNames[BatchUpdateTypeUserQuota]); ok {
			entries += summary.Entries
			delta += summary.Delta
		}
	}
	assert.Equal(t, 1, entries, "the debit must be drained by one flush only")
	assert.Equal(t, int64(-debit), delta, "the debit must be written once")

	assert.Empty(t, FlushBatchUpdates(), "nothing is left after both flushes")
	assert.Equal(t, 1000-debit, getUserQuotaFromDB(t, user.Id))
}

// The swap already keeps two flushes from writing one delta twice, so the lock has
// one job beyond that: a shutdown flush may not return while another flush is still
// writing, or the process exits with a drained batch only half written. Holding the
// lock here makes that assertion mean something — without it the flush would return
// immediately and the test would pass against the unlocked code as well.
func TestFlushBatchUpdatesWaitsForAnotherFlushToFinish(t *testing.T) {
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	batchUpdateFlushLock.Lock()
	var wg sync.WaitGroup
	flushed := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		FlushBatchUpdates()
		close(flushed)
	}()

	select {
	case <-flushed:
		batchUpdateFlushLock.Unlock()
		wg.Wait()
		t.Fatal("the shutdown flush returned while another flush held the lock")
	case <-time.After(100 * time.Millisecond):
	}

	batchUpdateFlushLock.Unlock()
	wg.Wait()
	select {
	case <-flushed:
	default:
		t.Fatal("the shutdown flush did not run once the other flush released the lock")
	}
}
