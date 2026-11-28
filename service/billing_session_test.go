package service

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedFunding is a FundingSource whose Refund blocks until the test releases it,
// so the ordering a shutdown has to handle becomes deterministic instead of a
// scheduling race. The wallet credit is queued exactly as WalletFunding queues it,
// so what the flush sees is the real accumulator state.
type gatedFunding struct {
	entered  chan struct{}
	release  chan struct{}
	userId   int
	consumed int
	once     sync.Once
}

func (g *gatedFunding) Source() string       { return BillingSourceWallet }
func (g *gatedFunding) PreConsume(int) error { return nil }
func (g *gatedFunding) Settle(int) error     { return nil }

func (g *gatedFunding) Refund() error {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return model.IncreaseUserQuota(g.userId, g.consumed, false)
}

func seedShutdownOrderingUser(t *testing.T, id int, quota int) {
	t.Helper()
	user := &model.User{
		Id:       id,
		Username: "shutdown-ordering-user",
		Password: "unused-password-hash",
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    quota,
		AffCode:  "shutdown-ordering-aff",
	}
	require.NoError(t, model.DB.Create(user).Error)
}

// The reviewer's ordering: the handler queues a debit and fails, the shutdown flush
// writes that debit, and the detached refund goroutine queues the credit only
// afterwards. A shutdown that does not wait for the refund therefore writes the
// debit and drops the credit, charging the user for a request that failed.
//
// The test forces that order instead of racing it: it holds the refund goroutine
// until the shutdown wait is provably still blocked on it. Against a Refund that
// spawns an untracked goroutine the wait returns at once, the "still blocked"
// assertion fails, and the wallet is left debited.
func TestShutdownWaitsForTheRefundBeforeFlushingTheAccumulator(t *testing.T) {
	truncate(t)
	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = false })

	const (
		userID   = 4242
		start    = 1000
		consumed = 30
	)
	seedShutdownOrderingUser(t, userID, start)

	funding := &gatedFunding{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		userId:   userID,
		consumed: consumed,
	}
	relayInfo := &relaycommon.RelayInfo{
		UserId:        userID,
		IsPlayground:  true,
		ToolExecution: &relaycommon.ToolExecutionMeta{Contract: "tools/search"},
	}
	session := &BillingSession{
		relayInfo:        relayInfo,
		funding:          funding,
		preConsumedQuota: consumed,
		tokenConsumed:    consumed,
	}
	relayInfo.Billing = session

	// 1) The handler's pre-consume debit is queued and not yet written.
	require.NoError(t, model.DecreaseUserQuota(userID, consumed, false))
	assert.Equal(t, start, getUserQuota(t, userID), "the debit is still queued before the flush")

	// 2) The handler fails and its deferred refund returns immediately.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	session.Refund(c)
	select {
	case <-funding.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the refund goroutine never started")
	}

	assert.Equal(t, "requested", relayInfo.ToolExecution.Refund)

	// 3) A periodic flush writes the debit while the refund is still pending.
	model.FlushBatchUpdates()
	assert.Equal(t, start-consumed, getUserQuota(t, userID), "the debit was written by the flush")

	// 4) Shutdown waits for the detached work. The refund is still gated, so a wait
	// that actually tracks it cannot return yet; one that tracks nothing returns
	// immediately and the following flush runs before the credit is queued.
	waited := make(chan bool, 1)
	go func() {
		_, finished := model.WaitDetachedQuotaWork(5 * time.Second)
		waited <- finished
	}()
	select {
	case finished := <-waited:
		close(funding.release)
		t.Fatalf("the shutdown wait returned (finished=%t) while the refund was still pending", finished)
	case <-time.After(200 * time.Millisecond):
	}

	// 5) Let the refund queue its credit, finish the wait, then drain again.
	close(funding.release)
	select {
	case finished := <-waited:
		assert.True(t, finished, "the shutdown wait must observe the refund finishing")
	case <-time.After(5 * time.Second):
		t.Fatal("the shutdown wait did not return after the refund finished")
	}
	model.FlushBatchUpdates()

	assert.Equal(t, start, getUserQuota(t, userID), "the failed request must cost nothing")
	assert.Equal(t, "requested", relayInfo.ToolExecution.Refund)
}

func TestBillingSessionRefundDoesNotMarkAnUnreservedToolRequest(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ToolExecution: &relaycommon.ToolExecutionMeta{Contract: "tools/search"}}
	session := &BillingSession{relayInfo: info, funding: &WalletFunding{}}
	session.Refund(ctx)
	assert.Empty(t, info.ToolExecution.Refund)
}
