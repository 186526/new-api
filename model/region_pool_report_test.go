package model

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openRegionLedgerFixture(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, quota BIGINT NOT NULL, deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO users (id, quota) VALUES (1, 100)`).Error)
	require.NoError(t, db.AutoMigrate(&RegionQuotaLeaseV2{}, &RegionQuotaDebitV2{}, &RegionReportReceipt{}, &RegionQuotaEvent{}))
	return db
}

func TestRegionReportReceiptRetries(t *testing.T) {
	db, ctx := openRegionLedgerFixture(t), context.Background()
	key := RegionLeaseKey{UserID: 1, Region: "test", Generation: 1}
	unknown := RegionReportV2{Version: RegionReportProtocolVersion, Region: key.Region, SubjectType: RegionSubjectUser,
		SubjectID: "1", Generation: 1, Seq: 1, Delta: 30, PayloadHash: strings.Repeat("a", 64)}
	_, err := applyStagedRegionReport(db, unknown)
	require.ErrorIs(t, err, ErrRegionReportNotReady)
	_, err = GrantRegionLeaseV2(ctx, db, RegionLeaseGrant{RegionLeaseKey: key, Granted: 80, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	debit := RegionLeaseDebit{RegionLeaseKey: key, OperationID: "fixture", Amount: 30}
	first, err := ReserveRegionLeaseV2(ctx, db, debit)
	require.NoError(t, err)
	retry, err := ReserveRegionLeaseV2(ctx, db, debit)
	require.NoError(t, err)
	require.Equal(t, first, retry)
	refund, err := RefundRegionLeaseV2(ctx, db, debit)
	require.NoError(t, err)
	retry, err = RefundRegionLeaseV2(ctx, db, debit)
	require.NoError(t, err)
	require.Equal(t, refund, retry)
	_, err = applyStagedRegionReport(db, refund)
	require.ErrorIs(t, err, ErrRegionReportNotReady)
	_, err = applyStagedRegionReport(db, unknown)
	require.ErrorIs(t, err, ErrRegionReportConflict)

	snapshot, err := RebuildRegionLeaseSnapshot(ctx, db, key)
	require.NoError(t, err)
	require.EqualValues(t, 50, snapshot.Available)
	require.EqualValues(t, 30, snapshot.PendingDebits)
	pending, err := PendingRegionReportsV2(ctx, db, key.Region, 0, 5)
	require.NoError(t, err)
	require.Len(t, pending, 2)
	require.Equal(t, first, pending[0].RegionReportV2)
	require.Equal(t, refund, pending[1].RegionReportV2)

	applied, err := applyStagedRegionReport(db, first)
	require.NoError(t, err)
	require.Positive(t, applied.AppliedAt)
	duplicate, err := applyStagedRegionReport(db, first)
	require.NoError(t, err)
	require.Equal(t, applied, duplicate)
	assertRegionWallet(t, db, 70)
	_, err = applyStagedRegionReport(db, refund)
	require.NoError(t, err)
	assertRegionWallet(t, db, 100)
	snapshot, err = RebuildRegionLeaseSnapshot(ctx, db, key)
	require.NoError(t, err)
	require.EqualValues(t, 80, snapshot.Available)
	require.Zero(t, snapshot.PendingDebits)
	pending, err = PendingRegionReportsV2(ctx, db, key.Region, 0, 5)
	require.NoError(t, err)
	require.Empty(t, pending)
	var legacyCount int64
	require.NoError(t, db.Model(&RegionQuotaEvent{}).Count(&legacyCount).Error)
	require.Zero(t, legacyCount)
}

func TestRegionReportClosedLeaseKeepsFundsForPendingDebit(t *testing.T) {
	db, ctx := openRegionLedgerFixture(t), context.Background()
	key := RegionLeaseKey{UserID: 1, Region: "test", Generation: 1}
	grant := RegionLeaseGrant{RegionLeaseKey: key, Granted: 80, ExpiresAt: common.GetTimestamp() + 3600}
	_, err := GrantRegionLeaseV2(ctx, db, grant)
	require.NoError(t, err)
	debit := RegionLeaseDebit{RegionLeaseKey: key, OperationID: "fixture", Amount: 30}
	first, err := ReserveRegionLeaseV2(ctx, db, debit)
	require.NoError(t, err)
	require.NoError(t, CloseRegionLeaseV2(ctx, db, key))
	refund, err := RefundRegionLeaseV2(ctx, db, debit)
	require.NoError(t, err)

	grant.Generation++
	grant.Granted = 71
	_, err = GrantRegionLeaseV2(ctx, db, grant)
	require.ErrorIs(t, err, ErrRegionLeaseUnavailable)
	grant.Granted = 70
	_, err = GrantRegionLeaseV2(ctx, db, grant)
	require.NoError(t, err)
	debit.RegionLeaseKey = grant.RegionLeaseKey
	_, err = ReserveRegionLeaseV2(ctx, db, debit)
	require.ErrorIs(t, err, ErrRegionLeaseLedgerConflict)

	_, err = applyStagedRegionReport(db, first)
	require.NoError(t, err)
	assertRegionWallet(t, db, 70)
	_, err = applyStagedRegionReport(db, refund)
	require.NoError(t, err)
	assertRegionWallet(t, db, 100)
	archived, err := RebuildRegionLeaseSnapshot(ctx, db, key)
	require.NoError(t, err)
	require.Zero(t, archived.Available)
	require.Positive(t, archived.ClosedAt)
	current, err := RebuildRegionLeaseSnapshot(ctx, db, grant.RegionLeaseKey)
	require.NoError(t, err)
	require.EqualValues(t, 70, current.Available)
}

func TestRegionReportGrantsShareOneWalletAcrossRegions(t *testing.T) {
	db, ctx := openRegionLedgerFixture(t), context.Background()
	grant := RegionLeaseGrant{RegionLeaseKey: RegionLeaseKey{UserID: 1, Region: "first", Generation: 1},
		Granted: 80, ExpiresAt: common.GetTimestamp() + 3600}
	lease, err := GrantRegionLeaseV2(ctx, db, grant)
	require.NoError(t, err)
	repeated, err := GrantRegionLeaseV2(ctx, db, grant)
	require.NoError(t, err)
	require.Equal(t, lease, repeated)
	grant.Region, grant.Granted = "second", 21
	_, err = GrantRegionLeaseV2(ctx, db, grant)
	require.ErrorIs(t, err, ErrRegionLeaseUnavailable)
	grant.Granted = 20
	_, err = GrantRegionLeaseV2(ctx, db, grant)
	require.NoError(t, err)
}

func TestRegionReportWalletFailureKeepsPendingReceipt(t *testing.T) {
	db, ctx := openRegionLedgerFixture(t), context.Background()
	key := RegionLeaseKey{UserID: 1, Region: "test", Generation: 1}
	_, err := GrantRegionLeaseV2(ctx, db, RegionLeaseGrant{RegionLeaseKey: key, Granted: 80, ExpiresAt: common.GetTimestamp() + 3600})
	require.NoError(t, err)
	report, err := ReserveRegionLeaseV2(ctx, db, RegionLeaseDebit{RegionLeaseKey: key, OperationID: "fixture", Amount: 30})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`UPDATE users SET quota = 20 WHERE id = 1`).Error)
	_, err = applyStagedRegionReport(db, report)
	require.ErrorIs(t, err, ErrRegionReportBalance)
	pending, err := PendingRegionReportsV2(ctx, db, key.Region, 0, 5)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Zero(t, pending[0].AppliedAt)
	snapshot, err := RebuildRegionLeaseSnapshot(ctx, db, key)
	require.NoError(t, err)
	require.EqualValues(t, 30, snapshot.PendingDebits)
	require.Zero(t, snapshot.AppliedSeq)
}

func assertRegionWallet(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	var balance int64
	require.NoError(t, db.Raw("SELECT quota FROM users WHERE id = 1").Scan(&balance).Error)
	require.Equal(t, want, balance)
}
