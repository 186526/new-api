package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSQLQuotaRefundReceiptPreventsDuplicateCredit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	require.NoError(t, db.AutoMigrate(&User{}, &QuotaRefundReceipt{}))
	require.NoError(t, db.Create(&User{Id: 1, Username: "refund-fixture", Quota: 10}).Error)
	receipt := QuotaRefundReceipt{RequestID: "test-refund", Part: RefundPartWallet, SubjectID: 1, Quota: 5, RequestedAt: 100, Status: RefundRequested}
	require.NoError(t, RegisterQuotaRefunds([]QuotaRefundReceipt{receipt}))
	for range 2 {
		status, err := ApplySQLQuotaRefund(receipt)
		require.NoError(t, err)
		require.Equal(t, RefundCompleted, status)
	}
	var user User
	require.NoError(t, db.First(&user, 1).Error)
	require.Equal(t, 15, user.Quota)
	counts, err := GetQuotaRefundCounts(90, 110, nil)
	require.NoError(t, err)
	require.Equal(t, QuotaRefundCounts{Requested: 1, Completed: 1}, counts)
}

func TestCommittedSQLQuotaRefundRecovery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	previousDB := DB
	previousReady := quotaRefundSchemaReady.Load()
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		quotaRefundSchemaReady.Store(previousReady)
	})
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &QuotaRefundReceipt{}))
	prepareQuotaProtocolFixture(t, db)
	require.NoError(t, db.Create(&User{Id: 1, Username: "refund-fixture", Quota: 10}).Error)
	require.NoError(t, db.Create(&Token{Id: 1, UserId: 1, Key: "test-token-refund", RemainQuota: 20}).Error)
	require.True(t, InitializeQuotaRefundRecovery())
	require.False(t, SQLQuotaLedgerWriterEnabled())

	check := func(wallet, token int) {
		t.Helper()
		var user User
		var key Token
		require.NoError(t, db.First(&user, 1).Error)
		require.NoError(t, db.First(&key, 1).Error)
		require.Equal(t, wallet, user.Quota)
		require.Equal(t, token, key.RemainQuota)
		require.Equal(t, 20-token, key.UsedQuota)
	}

	failed := SQLQuotaDebit{ID: "failed", UserID: 1, TokenID: 1}
	require.ErrorIs(t, failed.Reserve(11, true), ErrQuotaWalletInsufficient)
	check(10, 20)
	var count int64
	require.NoError(t, db.Model(&QuotaRefundReceipt{}).Where("request_id = ?", failed.ID).Count(&count).Error)
	require.Zero(t, count)

	op := SQLQuotaDebit{ID: "committed", LogRequestID: "http-log", UserID: 1, TokenID: 1}
	require.NoError(t, op.Reserve(5, true))
	require.NoError(t, op.Reserve(7, true))
	require.NoError(t, op.Reserve(7, true))
	require.ErrorIs(t, op.Reserve(6, true), ErrQuotaOperationDecided)
	check(3, 13)

	legacy := QuotaRefundReceipt{RequestID: "legacy", Part: RefundPartWallet, SubjectID: 1, Quota: 2, Status: RefundRequested}
	require.NoError(t, RegisterQuotaRefunds([]QuotaRefundReceipt{legacy}))
	legacy.Quota++
	require.Error(t, RegisterQuotaRefunds([]QuotaRefundReceipt{legacy}))
	require.NoError(t, RecoverQuotaRefunds(t.Context()))
	check(3, 13)

	protocol := QuotaWriteProtocol{ID: quotaWriteProtocolID}
	require.NoError(t, db.Model(&protocol).Updates(map[string]any{"mode": QuotaWriteDraining, "drain_from": QuotaWriteLegacy}).Error)
	require.ErrorIs(t, op.DecideRefund(), ErrQuotaWriteProtocol)
	require.NoError(t, db.Model(&protocol).UpdateColumn("drain_from", QuotaWriteSQL).Error)
	require.ErrorIs(t, (SQLQuotaDebit{ID: "during-drain", UserID: 1}).Reserve(1, true), ErrQuotaWriteProtocol)
	require.NoError(t, op.DecideRefund())
	require.ErrorIs(t, op.Settle(3), ErrQuotaOperationDecided)
	for range 2 {
		require.NoError(t, RecoverQuotaRefunds(t.Context()))
	}
	check(10, 20)
	for kind, balance := range map[string]int64{RefundPartWallet: 10, RefundPartToken: 20} {
		var publication QuotaBalancePublication
		require.NoError(t, db.Where("kind = ? AND subject_id = ?", kind, 1).Take(&publication).Error)
		require.Equal(t, int64(3), publication.Version)
		require.Equal(t, balance, publication.Balance)
		require.True(t, publication.Pending)
	}
	require.NoError(t, db.Model(&protocol).UpdateColumn("mode", QuotaWriteSQL).Error)
	outcomes, err := QuotaRefundOutcomes([]string{"http-log", "legacy"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"http-log": RefundCompleted, "legacy": RefundRequested}, outcomes)
	var restored []QuotaRefundReceipt
	require.NoError(t, db.Where("request_id = ?", op.ID).Find(&restored).Error)
	require.Len(t, restored, 2)
	for _, part := range restored {
		require.Equal(t, 7, part.Restored)
	}

	settled := SQLQuotaDebit{ID: "settled", UserID: 1, TokenID: 1}
	require.NoError(t, settled.Reserve(4, true))
	require.NoError(t, settled.Settle(3))
	require.NoError(t, settled.Settle(3))
	require.ErrorIs(t, settled.Settle(2), ErrQuotaOperationDecided)
	require.ErrorIs(t, settled.DecideRefund(), ErrQuotaOperationDecided)
	check(7, 17)

	require.NoError(t, db.Create(&Token{Id: 2, UserId: 2, Key: "other-user-refund-fixture", RemainQuota: 20, UnlimitedQuota: true}).Error)
	wrongOwner := SQLQuotaDebit{ID: "wrong-owner", UserID: 1, TokenID: 2}
	require.ErrorIs(t, wrongOwner.Reserve(2, true), ErrQuotaTokenInsufficient)
	check(7, 17)
	require.NoError(t, db.Model(&QuotaRefundReceipt{}).Where("request_id = ?", wrongOwner.ID).Count(&count).Error)
	require.Zero(t, count)
}

func TestCommittedSubscriptionQuotaRefundRecovery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := DB
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		_ = sqlDB.Close()
	})
	require.NoError(t, db.AutoMigrate(&UserSubscription{}, &SubscriptionPlan{}, &QuotaRefundReceipt{}))
	prepareQuotaProtocolFixture(t, db)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{Id: 71, QuotaResetPeriod: SubscriptionResetNever}
	require.NoError(t, db.Create(&plan).Error)
	sub := UserSubscription{UserId: 1, PlanId: plan.Id, Status: "active", StartTime: now - 1, EndTime: now + 3600, NextResetTime: now + 1800, AmountTotal: 20}
	require.NoError(t, db.Create(&sub).Error)

	checkUsed := func(want int64) {
		t.Helper()
		var stored UserSubscription
		require.NoError(t, db.First(&stored, sub.Id).Error)
		require.Equal(t, want, stored.AmountUsed)
	}
	checkReceipt := func(id, part, status, reason string, restored int) {
		t.Helper()
		var receipt QuotaRefundReceipt
		require.NoError(t, db.Where("request_id = ? AND part = ?", id, part).Take(&receipt).Error)
		require.Equal(t, status, receipt.Status)
		require.Equal(t, reason, receipt.RestoreReason)
		require.Equal(t, restored, receipt.Restored)
	}

	op := sqlSubscriptionDebit{ID: "subscription-one", UserID: sub.UserId, SubscriptionID: sub.Id}
	require.NoError(t, op.Reserve(3, 0))
	require.NoError(t, op.Reserve(3, 4))
	require.NoError(t, op.Reserve(3, 4))
	require.Error(t, op.Reserve(3, 18))
	require.NoError(t, RecoverQuotaRefunds(t.Context()))
	checkUsed(7)
	require.NoError(t, op.DecideRefund())
	require.ErrorIs(t, op.Settle(3), ErrQuotaOperationDecided)
	for range 2 {
		require.NoError(t, RecoverQuotaRefunds(t.Context()))
	}
	checkUsed(0)
	checkReceipt(op.ID, RefundPartSubscription, RefundCompleted, "restored", 3)
	checkReceipt(op.ID, RefundPartSubscriptionExtra, RefundCompleted, "restored", 4)

	op.ID = "subscription-before-reset"
	require.NoError(t, op.Reserve(6, 2))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		var stored UserSubscription
		if err := tx.First(&stored, sub.Id).Error; err != nil {
			return err
		}
		return resetUserSubscriptionTx(tx, &stored, &plan, now, false)
	}))
	current := op
	current.ID = "subscription-after-reset"
	require.NoError(t, current.Reserve(5, 0))
	require.NoError(t, current.Settle(4))
	require.ErrorIs(t, current.DecideRefund(), ErrQuotaOperationDecided)
	require.NoError(t, op.DecideRefund())
	require.NoError(t, RecoverQuotaRefunds(t.Context()))
	checkUsed(4)
	checkReceipt(op.ID, RefundPartSubscription, RefundCompleted, "period_closed", 0)
	checkReceipt(op.ID, RefundPartSubscriptionExtra, RefundCompleted, "period_closed", 0)
}

func TestSubscriptionTokenQuotaTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB; _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&UserSubscription{}, &SubscriptionPlan{}, &Token{}, &QuotaRefundReceipt{}))
	prepareQuotaProtocolFixture(t, db)
	now := GetDBTimestamp()
	plan := SubscriptionPlan{QuotaResetPeriod: SubscriptionResetNever}
	require.NoError(t, db.Create(&plan).Error)
	sub := UserSubscription{UserId: 42, PlanId: plan.Id, Status: "active", StartTime: now - 1, EndTime: now + 3600, AmountTotal: 100}
	require.NoError(t, db.Create(&sub).Error)
	token := Token{UserId: sub.UserId, Key: "subscription-token-test", RemainQuota: 10}
	require.NoError(t, db.Create(&token).Error)
	checkBalances := func(used int64, tokenRemain int) {
		t.Helper()
		var storedSub UserSubscription
		var storedToken Token
		require.NoError(t, db.First(&storedSub, sub.Id).Error)
		require.NoError(t, db.First(&storedToken, token.Id).Error)
		require.Equal(t, used, storedSub.AmountUsed)
		require.Equal(t, tokenRemain, storedToken.RemainQuota)
		require.Equal(t, 10-tokenRemain, storedToken.UsedQuota)
	}
	op := SQLSubscriptionDebit{ID: "joint", UserID: sub.UserId, TokenID: token.Id}
	_, err = op.Reserve(11, 11)
	require.ErrorIs(t, err, ErrQuotaTokenInsufficient)
	checkBalances(0, 10)
	var count int64
	require.NoError(t, db.Model(&QuotaRefundReceipt{}).Count(&count).Error)
	require.Zero(t, count)
	for _, target := range []int{3, 7, 7} {
		reservation, err := op.Reserve(3, target)
		require.NoError(t, err)
		require.Equal(t, sub.Id, reservation.UserSubscriptionId)
		require.EqualValues(t, target, reservation.AmountUsedAfter)
	}
	_, err = op.Reserve(3, 11)
	require.ErrorIs(t, err, ErrQuotaTokenInsufficient)
	require.NoError(t, RecoverQuotaRefunds(t.Context()))
	checkBalances(7, 3)
	cross := op
	cross.ID = "cross-period-settlement"
	_, err = cross.Reserve(2, 2)
	require.NoError(t, err)
	require.NoError(t, db.Model(&sub).Updates(map[string]any{"amount_used": 1, "quota_generation": 1}).Error)
	applied, err := cross.Settle(8)
	require.NoError(t, err)
	require.Zero(t, applied)
	_, err = cross.Settle(8)
	require.NoError(t, err)
	_, err = cross.Settle(9)
	require.ErrorIs(t, err, ErrQuotaOperationDecided)
	require.ErrorIs(t, cross.DecideRefund(), ErrQuotaOperationDecided)
	var receipt QuotaRefundReceipt
	require.NoError(t, db.Where("request_id = ? AND part = ?", cross.ID, RefundPartSubscription).Take(&receipt).Error)
	require.Equal(t, 6, receipt.UncollectedQuota)
	checkBalances(1, 1)
	require.NoError(t, op.DecideRefund())
	for range 2 {
		require.NoError(t, RecoverQuotaRefunds(t.Context()))
		status, err := op.ApplyRefund(t.Context())
		require.NoError(t, err)
		require.Equal(t, RefundCompleted, status)
	}
	checkBalances(1, 8)
	playground := SQLSubscriptionDebit{ID: "playground", UserID: sub.UserId}
	_, err = playground.Reserve(1, 1)
	require.NoError(t, err)
	checkBalances(2, 8)
	require.False(t, SQLQuotaLedgerWriterEnabled())
}

func prepareQuotaProtocolFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&QuotaWriteProtocol{}, &QuotaBalancePublication{}))
	require.NoError(t, db.Create(&QuotaWriteProtocol{ID: quotaWriteProtocolID, Version: quotaWriteProtocolVersion, Mode: QuotaWriteSQL}).Error)
}

func TestOverlayToolRefundOutcomeKeepsProviderAdminOnly(t *testing.T) {
	original := `{"tool_execution":{"refund":"requested"},"admin_info":{"tool_execution":{"refund":"requested","provider":"exa"}}}`
	want := `{"tool_execution":{"refund":"completed"},"admin_info":{"tool_execution":{"refund":"completed","provider":"exa"}}}`
	require.JSONEq(t, want, overlayToolRefundOutcome(original, RefundCompleted))
}
