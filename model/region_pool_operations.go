package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type RegionLeaseDebit struct {
	RegionLeaseKey
	OperationID string
	Amount      int64
}

type RegionQuotaDebitV2 struct {
	ID            int64  `gorm:"primaryKey"`
	UserID        int    `gorm:"not null;uniqueIndex:idx_region_debit_v2_identity,priority:1"`
	OperationID   string `gorm:"size:128;not null;uniqueIndex:idx_region_debit_v2_identity,priority:2"`
	LeaseID       int64  `gorm:"not null;index"`
	Amount        int64  `gorm:"not null"`
	DebitSeq      int64  `gorm:"not null"`
	RefundSeq     int64  `gorm:"not null"`
	SettledSeq    int64  `gorm:"not null;default:0"`
	SettledAmount int64  `gorm:"not null;default:0"`
}

func (RegionQuotaDebitV2) TableName() string { return "region_quota_debits_v2" }

func (debit RegionLeaseDebit) valid() bool {
	return debit.RegionLeaseKey.valid() && debit.Amount > 0 && debit.Amount <= common.MaxWalletQuota &&
		debit.OperationID != "" && len(debit.OperationID) <= 128 && strings.TrimSpace(debit.OperationID) == debit.OperationID &&
		!strings.ContainsRune(debit.OperationID, '\x00')
}

func ReserveRegionLeaseV2(ctx context.Context, db *gorm.DB, debit RegionLeaseDebit) (RegionReportV2, error) {
	return mutateRegionLeaseDebit(ctx, db, debit, false)
}

func RefundRegionLeaseV2(ctx context.Context, db *gorm.DB, debit RegionLeaseDebit) (RegionReportV2, error) {
	return mutateRegionLeaseDebit(ctx, db, debit, true)
}

func mutateRegionLeaseDebit(ctx context.Context, db *gorm.DB, debit RegionLeaseDebit, refund bool) (RegionReportV2, error) {
	if !debit.valid() {
		return RegionReportV2{}, ErrInvalidRegionReport
	}
	var report RegionReportV2
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRegionWallet(tx, debit.UserID); err != nil {
			return err
		}
		lease, err := readRegionLeaseV2(tx, debit.RegionLeaseKey)
		if err != nil {
			return err
		}
		report, err = recordRegionLeaseDebit(tx, lease, debit, refund)
		return err
	})
	if err != nil {
		return RegionReportV2{}, err
	}
	return report, nil
}

func recordRegionLeaseDebit(tx *gorm.DB, lease RegionQuotaLeaseV2, debit RegionLeaseDebit, refund bool) (RegionReportV2, error) {
	var entry RegionQuotaDebitV2
	err := tx.Where("user_id = ? AND operation_id = ?", debit.UserID, debit.OperationID).Take(&entry).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if refund {
			return RegionReportV2{}, ErrRegionReportNotReady
		}
		return reserveRegionLeaseDebit(tx, lease, debit)
	case err != nil:
		return RegionReportV2{}, err
	case entry.LeaseID != lease.ID || entry.Amount != debit.Amount:
		return RegionReportV2{}, ErrRegionLeaseLedgerConflict
	case !refund:
		if entry.RefundSeq != 0 {
			return RegionReportV2{}, ErrRegionLeaseLedgerConflict
		}
		return makeRegionDebitReport(debit, entry.DebitSeq, debit.Amount)
	case entry.SettledSeq != 0:
		return RegionReportV2{}, ErrRegionLeaseLedgerConflict
	case entry.RefundSeq != 0:
		return makeRegionDebitReport(debit, entry.RefundSeq, -debit.Amount)
	default:
		return refundRegionLeaseDebit(tx, lease, debit, entry)
	}
}

func reserveRegionLeaseDebit(tx *gorm.DB, lease RegionQuotaLeaseV2, debit RegionLeaseDebit) (RegionReportV2, error) {
	if !lease.active(common.GetTimestamp()) || lease.Available < debit.Amount {
		return RegionReportV2{}, ErrRegionLeaseUnavailable
	}
	lease.Available -= debit.Amount
	report, err := stageRegionLeaseMutation(tx, lease, debit, debit.Amount)
	if err != nil {
		return RegionReportV2{}, err
	}
	entry := RegionQuotaDebitV2{UserID: debit.UserID, OperationID: debit.OperationID, LeaseID: lease.ID, Amount: debit.Amount, DebitSeq: report.Seq}
	return report, tx.Create(&entry).Error
}

func refundRegionLeaseDebit(tx *gorm.DB, lease RegionQuotaLeaseV2, debit RegionLeaseDebit, entry RegionQuotaDebitV2) (RegionReportV2, error) {
	now := common.GetTimestamp()
	if !lease.active(now) && lease.ClosedAt == 0 {
		lease.Available, lease.ClosedAt = 0, now
	}
	report, err := stageRegionLeaseMutation(tx, lease, debit, -debit.Amount)
	if err != nil {
		return RegionReportV2{}, err
	}
	update := tx.Model(&entry).Where("refund_seq = 0").UpdateColumn("refund_seq", report.Seq)
	if update.Error != nil {
		return RegionReportV2{}, update.Error
	}
	if update.RowsAffected != 1 {
		return RegionReportV2{}, ErrRegionLeaseLedgerConflict
	}
	return report, nil
}

func stageRegionLeaseMutation(tx *gorm.DB, lease RegionQuotaLeaseV2, debit RegionLeaseDebit, delta int64) (RegionReportV2, error) {
	pendingDebits := lease.PendingDebits + max(delta, 0)
	if lease.StagedSeq == math.MaxInt64 || pendingDebits > common.MaxWalletQuota {
		return RegionReportV2{}, ErrRegionLeaseLedgerConflict
	}
	report, err := makeRegionDebitReport(debit, lease.StagedSeq+1, delta)
	if err != nil {
		return RegionReportV2{}, err
	}
	if err := tx.Create(&RegionReportReceipt{RegionReportV2: report}).Error; err != nil {
		return RegionReportV2{}, err
	}
	return report, updateRegionLease(tx, lease.ID, map[string]any{
		"staged_seq": report.Seq, "pending_debits": pendingDebits, "available": lease.Available, "closed_at": lease.ClosedAt,
	})
}

func makeRegionDebitReport(debit RegionLeaseDebit, seq, delta int64) (RegionReportV2, error) {
	report := RegionReportV2{Version: RegionReportProtocolVersion, Region: debit.Region, SubjectType: RegionSubjectUser,
		SubjectID: strconv.Itoa(debit.UserID), Generation: debit.Generation, Seq: seq, Delta: delta}
	payload, err := json.Marshal(struct {
		RegionReportV2
		OperationID string `json:"operation_id"`
	}{report, debit.OperationID})
	if err != nil {
		return RegionReportV2{}, err
	}
	digest := sha256.Sum256(payload)
	report.PayloadHash = hex.EncodeToString(digest[:])
	return report, nil
}
