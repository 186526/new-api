package model

import (
	"math"
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
	channel := Channel{
		Id:        77,
		Name:      "flush-test-channel",
		Key:       "sk-flush-test",
		Status:    common.ChannelStatusEnabled,
		UsedQuota: 0,
	}
	require.NoError(t, DB.Create(&channel).Error)
	addNewRecord(BatchUpdateTypeUserQuota, user.Id, -30)
	addNewRecord(BatchUpdateTypeUsedQuota, user.Id, 30)
	addNewRecord(BatchUpdateTypeRequestCount, user.Id, 1)
	addNewRecord(BatchUpdateTypeTokenQuota, token.Id, -30)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, channel.Id, 30)

	summaries := FlushBatchUpdates()
	require.Len(t, summaries, BatchUpdateTypeCount)
	for i := range BatchUpdateTypeCount {
		summary, ok := findFlushSummary(summaries, batchUpdateTypeNames[i])
		require.True(t, ok, "accumulator %d must be reported", i)
		assert.Equal(t, 1, summary.Entries)
		assert.Equal(t, 1, summary.Accepted, "accumulator %d wrote a row that exists, so the database accepted it", i)
	}

	assert.Equal(t, 970, getUserQuotaFromDB(t, user.Id))
	usedQuota, requestCount := getUserUsageFromDB(t, user.Id)
	assert.Equal(t, 30, usedQuota)
	assert.Equal(t, 1, requestCount)
	assert.Equal(t, 70, getTokenFromDB(t, token.Id).RemainQuota)
}

// A delta queued for a row that no longer exists is drained but not written, so the
// flush must report fewer accepted entries than it drained instead of claiming the
// write happened. The token path already returned ErrRecordNotFound; the user and
// channel paths now check RowsAffected for the same reason.
func TestFlushBatchUpdatesCountsOnlyAcceptedRows(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	const missingID = 987654
	addNewRecord(BatchUpdateTypeTokenQuota, missingID, -50)
	addNewRecord(BatchUpdateTypeUserQuota, missingID, -50)
	addNewRecord(BatchUpdateTypeChannelUsedQuota, missingID, 50)

	summaries := FlushBatchUpdates()
	for _, i := range []int{BatchUpdateTypeTokenQuota, BatchUpdateTypeUserQuota, BatchUpdateTypeChannelUsedQuota} {
		summary, ok := findFlushSummary(summaries, batchUpdateTypeNames[i])
		require.True(t, ok, "accumulator %d must be reported even when its row is gone", i)
		assert.Equal(t, 1, summary.Entries, "the delta was drained")
		assert.Equal(t, 0, summary.Accepted, "no row existed, so the database accepted nothing")
	}
}

func TestFlushBatchUpdatesIsEmptyWithoutAccumulatedDeltas(t *testing.T) {
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	assert.Empty(t, FlushBatchUpdates(), "a process with nothing accumulated writes nothing")
}

// addNewRecord saturates one id at the int extremes, so two such ids added together
// must saturate the reported total too: int64 arithmetic would otherwise report -2
// for two rows that each received MaxInt.
func TestFlushBatchUpdatesSaturatesTheReportedDelta(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	common.BatchUpdateEnabled = true

	first := createReserveTestUser(t, 0)
	second := createReserveTestUser(t, 0)
	addNewRecord(BatchUpdateTypeUserQuota, first.Id, math.MaxInt)
	addNewRecord(BatchUpdateTypeUserQuota, second.Id, math.MaxInt)

	summaries := FlushBatchUpdates()
	summary, ok := findFlushSummary(summaries, batchUpdateTypeNames[BatchUpdateTypeUserQuota])
	require.True(t, ok)
	assert.Equal(t, 2, summary.Entries)
	assert.Equal(t, int64(math.MaxInt64), summary.Delta, "the reported total saturates instead of wrapping negative")
	assert.Equal(t, math.MaxInt, getUserQuotaFromDB(t, first.Id))
	assert.Equal(t, math.MaxInt, getUserQuotaFromDB(t, second.Id))
}

func TestAccumulateFlushDeltaSaturatesBothDirections(t *testing.T) {
	assert.Equal(t, int64(math.MaxInt64), accumulateFlushDelta(math.MaxInt64, 1))
	assert.Equal(t, int64(math.MinInt64), accumulateFlushDelta(math.MinInt64, -1))
	assert.Equal(t, int64(math.MaxInt64), accumulateFlushDelta(int64(math.MaxInt)-1, math.MaxInt))
	assert.Equal(t, int64(-60), accumulateFlushDelta(0, -60), "an ordinary sum is unchanged")
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

func TestDetachedQuotaWorkIsIdleWithNothingRunning(t *testing.T) {
	pending, finished := WaitDetachedQuotaWork(time.Second)
	assert.Equal(t, 0, pending)
	assert.True(t, finished, "an idle tracker must not make shutdown wait")
}

func TestWaitDetachedQuotaWorkCoversAlreadyRunningGoroutines(t *testing.T) {
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(1)
	GoDetachedQuotaWork(func() {
		started.Done()
		<-release
	})
	started.Wait()

	waited := make(chan struct{})
	var pending int
	var finished bool
	go func() {
		defer close(waited)
		pending, finished = WaitDetachedQuotaWork(5 * time.Second)
	}()

	select {
	case <-waited:
		close(release)
		t.Fatal("the wait returned while the tracked goroutine was still running")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not return after the tracked goroutine finished")
	}
	assert.Equal(t, 1, pending)
	assert.True(t, finished)
}

// main.go logs the pending count and flushes anyway when the wait expires, so the
// timeout branch must report what it found rather than block.
func TestWaitDetachedQuotaWorkReportsTheTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var started sync.WaitGroup
	started.Add(1)
	GoDetachedQuotaWork(func() {
		started.Done()
		<-release
	})
	started.Wait()

	pending, finished := WaitDetachedQuotaWork(50 * time.Millisecond)
	assert.Equal(t, 1, pending, "the count of goroutines still running must reach the caller")
	assert.False(t, finished, "an expired wait reports that it did not finish")
}
