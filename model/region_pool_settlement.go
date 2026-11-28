package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type RegionLeaseSettlement struct {
	RegionLeaseDebit
	Actual int64
}

func SettleRegionLeaseV2(ctx context.Context, db *gorm.DB, settlement RegionLeaseSettlement) (RegionReportV2, error) {
	if !settlement.valid() || settlement.Actual < 0 || settlement.Actual > common.MaxWalletQuota {
		return RegionReportV2{}, ErrInvalidRegionReport
	}
	var report RegionReportV2
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRegionWallet(tx, settlement.UserID); err != nil {
			return err
		}
		lease, err := readRegionLeaseV2(tx, settlement.RegionLeaseKey)
		if err != nil {
			return err
		}
		report, err = settleRegionLeaseDebit(tx, lease, settlement)
		return err
	})
	if err != nil {
		return RegionReportV2{}, err
	}
	return report, nil
}

func settleRegionLeaseDebit(tx *gorm.DB, lease RegionQuotaLeaseV2, settlement RegionLeaseSettlement) (RegionReportV2, error) {
	var entry RegionQuotaDebitV2
	err := tx.Where("user_id = ? AND operation_id = ?", settlement.UserID, settlement.OperationID).Take(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RegionReportV2{}, ErrRegionReportNotReady
	}
	if err != nil {
		return RegionReportV2{}, err
	}
	if entry.LeaseID != lease.ID || entry.Amount != settlement.Amount || entry.RefundSeq != 0 {
		return RegionReportV2{}, ErrRegionLeaseLedgerConflict
	}
	delta := settlement.Actual - entry.Amount
	if entry.SettledSeq != 0 {
		if entry.SettledAmount != settlement.Actual {
			return RegionReportV2{}, ErrRegionLeaseLedgerConflict
		}
		return makeRegionDebitReport(settlement.RegionLeaseDebit, entry.SettledSeq, delta)
	}
	if delta > 0 {
		if !lease.active(common.GetTimestamp()) || lease.Available < delta {
			return RegionReportV2{}, ErrRegionLeaseUnavailable
		}
		lease.Available -= delta
	}
	report, err := stageRegionLeaseMutation(tx, lease, settlement.RegionLeaseDebit, delta)
	if err != nil {
		return RegionReportV2{}, err
	}
	update := tx.Model(&entry).Where("settled_seq = 0 AND refund_seq = 0").UpdateColumns(map[string]any{
		"settled_seq": report.Seq, "settled_amount": settlement.Actual,
	})
	if update.Error != nil {
		return RegionReportV2{}, update.Error
	}
	if update.RowsAffected != 1 {
		return RegionReportV2{}, ErrRegionLeaseLedgerConflict
	}
	return report, nil
}
