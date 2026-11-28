package model

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupQuotaReviewSQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&QuotaRefundReceipt{}, &QuotaReconciliation{}))
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	return db
}

func TestQuotaReviewRejectsMissingEvidence(t *testing.T) {
	setupQuotaReviewSQLite(t)
	ctx := context.Background()
	identity := QuotaReviewIdentity{RequestID: "missing-receipt"}
	review, err := GetQuotaReview(ctx, identity)
	require.NoError(t, err)
	require.Empty(t, review.Receipts)
	require.False(t, review.Confirmable)
	require.Empty(t, review.EvidenceHash)
	require.ErrorIs(t, ConfirmQuotaReview(ctx, QuotaReviewSubmission{
		QuotaReviewIdentity: identity,
		EvidenceHash:        strings.Repeat("a", 64),
		Reason:              "Reviewed refund",
	}, 42), ErrQuotaReviewPending)
}

func TestQuotaReviewRequiresCurrentEvidence(t *testing.T) {
	db := setupQuotaReviewSQLite(t)
	ctx := context.Background()
	receipt := QuotaRefundReceipt{RequestID: "refund-operation", LogRequestID: "refund-log", Part: RefundPartWallet, SubjectID: 1, Status: RefundCompleted, Quota: 10, Restored: 10}
	require.NoError(t, db.Create(&receipt).Error)
	identity := QuotaReviewIdentity{RequestID: "refund-log"}
	original, err := GetQuotaReview(ctx, identity)
	require.NoError(t, err)
	require.True(t, original.Confirmable)
	require.Len(t, original.EvidenceHash, 64)

	require.NoError(t, db.Model(&QuotaRefundReceipt{}).Where("request_id = ? AND part = ?", receipt.RequestID, receipt.Part).Update("restored", 7).Error)
	submission := QuotaReviewSubmission{QuotaReviewIdentity: identity, EvidenceHash: original.EvidenceHash, Reason: "Reviewed refund"}
	require.ErrorIs(t, ConfirmQuotaReview(ctx, submission, 42), ErrQuotaReviewPending)
	updated, err := GetQuotaReview(ctx, identity)
	require.NoError(t, err)
	require.Nil(t, updated.Confirmation)
	require.True(t, updated.Confirmable)
	require.Len(t, updated.Receipts, 1)
	require.Equal(t, 7, updated.Receipts[0].Restored)
	require.NotEqual(t, original.EvidenceHash, updated.EvidenceHash)
	submission.EvidenceHash = updated.EvidenceHash
	require.NoError(t, ConfirmQuotaReview(ctx, submission, 42))
}

func TestQuotaReviewPreservesFirstConfirmationAcrossRequestIDs(t *testing.T) {
	db := setupQuotaReviewSQLite(t)
	ctx := context.Background()
	receipt := QuotaRefundReceipt{RequestID: "refund-operation", LogRequestID: "refund-log", Part: RefundPartWallet, SubjectID: 1, Status: RefundCompleted, Quota: 10, Restored: 10}
	require.NoError(t, db.Create(&receipt).Error)
	identity := QuotaReviewIdentity{RequestID: "refund-log"}
	review, err := GetQuotaReview(ctx, identity)
	require.NoError(t, err)
	require.True(t, review.Confirmable)
	submission := QuotaReviewSubmission{QuotaReviewIdentity: identity, EvidenceHash: review.EvidenceHash, Reason: "  First review  "}
	require.NoError(t, ConfirmQuotaReview(ctx, submission, 42))

	submission.RequestID = "refund-operation"
	submission.Reason = "Replacement review"
	require.ErrorIs(t, ConfirmQuotaReview(ctx, submission, 43), ErrQuotaReviewRecorded)
	confirmed, err := GetQuotaReview(ctx, identity)
	require.NoError(t, err)
	require.False(t, confirmed.Confirmable)
	require.Empty(t, confirmed.EvidenceHash)
	require.NotNil(t, confirmed.Confirmation)
	require.Equal(t, "First review", confirmed.Confirmation.Reason)
	require.Equal(t, 42, confirmed.Confirmation.OperatorID)
}
