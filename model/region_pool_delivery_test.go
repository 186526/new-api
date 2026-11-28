package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestRegionDeliveryRetriesAppliedReportUntilAcknowledged(t *testing.T) {
	db, ctx := openRegionLedgerFixture(t), context.Background()
	key := RegionLeaseKey{UserID: 1, Region: "test", Generation: 1}
	_, err := GrantRegionLeaseV2(ctx, db, RegionLeaseGrant{RegionLeaseKey: key, Granted: 80, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	debit := RegionLeaseDebit{RegionLeaseKey: key, OperationID: "fixture", Amount: 30}
	first, err := ReserveRegionLeaseV2(ctx, db, debit)
	require.NoError(t, err)
	settlement := RegionLeaseSettlement{RegionLeaseDebit: debit, Actual: 30}
	final, err := SettleRegionLeaseV2(ctx, db, settlement)
	require.NoError(t, err)
	require.Zero(t, final.Delta)
	require.Equal(t, first.Seq+1, final.Seq)
	duplicate, err := SettleRegionLeaseV2(ctx, db, settlement)
	require.NoError(t, err)
	require.Equal(t, final, duplicate)
	settlement.Actual++
	_, err = SettleRegionLeaseV2(ctx, db, settlement)
	require.ErrorIs(t, err, ErrRegionLeaseLedgerConflict)
	_, err = RefundRegionLeaseV2(ctx, db, debit)
	require.ErrorIs(t, err, ErrRegionLeaseLedgerConflict)

	claimed, err := ClaimRegionReportsV2(ctx, db, key.Region, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, first, claimed[0].RegionReportV2)
	receipt, err := applyStagedRegionReport(db, first)
	require.NoError(t, err)
	assertRegionWallet(t, db, 70)
	mismatched := receipt
	mismatched.AppliedAt++
	require.ErrorIs(t, AcknowledgeRegionReportV2(ctx, db, mismatched), ErrRegionReportNotReady)
	blocked, err := ClaimRegionReportsV2(ctx, db, key.Region, 10)
	require.NoError(t, err)
	require.Empty(t, blocked)
	require.NoError(t, db.Model(&claimed[0]).UpdateColumn("next_attempt_at", 0).Error)
	retry, err := ClaimRegionReportsV2(ctx, db, key.Region, 10)
	require.NoError(t, err)
	require.Len(t, retry, 1)
	require.Equal(t, first, retry[0].RegionReportV2)
	applied, err := applyStagedRegionReport(db, first)
	require.NoError(t, err)
	require.Equal(t, receipt.AppliedAt, applied.AppliedAt)
	require.NoError(t, AcknowledgeRegionReportV2(ctx, db, applied))
	require.NoError(t, AcknowledgeRegionReportV2(ctx, db, applied))
	claimed, err = ClaimRegionReportsV2(ctx, db, key.Region, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, final, claimed[0].RegionReportV2)
	applied, err = applyStagedRegionReport(db, final)
	require.NoError(t, err)
	require.NoError(t, AcknowledgeRegionReportV2(ctx, db, applied))
	assertRegionWallet(t, db, 70)
}
