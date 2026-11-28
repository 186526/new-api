package model

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTaskWalletDebitTerminalTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB; _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&Task{}, &User{}, &Token{}, &QuotaRefundReceipt{}))
	prepareQuotaProtocolFixture(t, db)
	require.NoError(t, db.Create(&User{Id: 1, Username: "task-wallet-fixture", Quota: 100}).Error)
	require.NoError(t, db.Create(&Token{Id: 1, UserId: 1, Key: "task-wallet-fixture", RemainQuota: 100}).Error)
	ctx := context.Background()
	op := SQLQuotaDebit{ID: "task-wallet-operation", UserID: 1, TokenID: 1}
	task := Task{TaskID: "task-fixture", UserId: 1, Quota: 60, Status: TaskStatusSubmitted}
	require.Error(t, task.InsertWithWalletDebit(ctx, op))
	require.NoError(t, op.Reserve(60, true))
	wrongAmount := task
	wrongAmount.Quota++
	require.ErrorIs(t, wrongAmount.InsertWithWalletDebit(ctx, op), ErrQuotaOperationDecided)
	require.NoError(t, task.InsertWithWalletDebit(ctx, op))
	duplicate := Task{TaskID: "duplicate", UserId: 1, Quota: 60, Status: TaskStatusSubmitted}
	require.ErrorIs(t, duplicate.InsertWithWalletDebit(ctx, op), ErrQuotaOperationDecided)
	require.ErrorIs(t, op.DecideRefund(), ErrQuotaOperationDecided)

	balance := func(expected int) {
		t.Helper()
		var user User
		var token Token
		require.NoError(t, db.First(&user, 1).Error)
		require.NoError(t, db.First(&token, 1).Error)
		require.Equal(t, expected, user.Quota)
		require.Equal(t, expected, token.RemainQuota)
	}
	candidate := task
	candidate.Status = TaskStatusSuccess
	won, err := candidate.FinishWithWalletDebit(ctx, TaskStatusSubmitted, 101)
	require.Error(t, err)
	require.False(t, won)
	balance(40)
	var stored Task
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, TaskStatus(TaskStatusSubmitted), stored.Status)

	candidate.Status = TaskStatusFailure
	won, err = candidate.FinishWithWalletDebit(ctx, TaskStatusSubmitted, 0)
	require.NoError(t, err)
	require.True(t, won)
	balance(40)
	require.NoError(t, db.First(&stored, task.ID).Error)
	require.Equal(t, TaskStatus(TaskStatusFailure), stored.Status)
	require.Zero(t, stored.Quota)
	for range 2 {
		outcome, err := op.ApplyRefund(ctx)
		require.NoError(t, err)
		require.Equal(t, RefundCompleted, outcome)
		balance(100)
	}
	candidate.Status = TaskStatusSuccess
	won, err = candidate.FinishWithWalletDebit(ctx, TaskStatusSubmitted, 60)
	require.NoError(t, err)
	require.False(t, won)
}
