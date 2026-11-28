package model

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const RegionReportProtocolVersion = 2

var (
	ErrInvalidRegionReport  = errors.New("invalid regional report")
	ErrRegionReportConflict = errors.New("regional report payload differs from the stored report")
	ErrRegionReportNotReady = errors.New("regional report or its predecessor is not ready")
	ErrRegionReportBalance  = errors.New("regional report balance update did not match a live wallet within quota bounds")
)

type RegionReportV2 struct {
	Version     int    `json:"version" gorm:"not null;uniqueIndex:idx_region_report_v2_identity,priority:1"`
	Region      string `json:"region" gorm:"size:64;not null;uniqueIndex:idx_region_report_v2_identity,priority:2;index:idx_region_report_v2_delivery,priority:2"`
	SubjectType string `json:"subject_type" gorm:"size:16;not null;uniqueIndex:idx_region_report_v2_identity,priority:3"`
	SubjectID   string `json:"subject_id" gorm:"size:128;not null;uniqueIndex:idx_region_report_v2_identity,priority:4"`
	Generation  int64  `json:"generation" gorm:"not null;uniqueIndex:idx_region_report_v2_identity,priority:5"`
	Seq         int64  `json:"seq" gorm:"not null;uniqueIndex:idx_region_report_v2_identity,priority:6"`
	Delta       int64  `json:"delta" gorm:"not null"`
	PayloadHash string `json:"payload_hash" gorm:"size:64;not null"`
}

type RegionReportReceipt struct {
	ID             int64 `json:"-" gorm:"primaryKey"`
	RegionReportV2 `gorm:"embedded"`
	AppliedAt      int64 `json:"applied_at" gorm:"not null;default:0;index:idx_region_report_v2_pending,priority:1"`
	AcknowledgedAt int64 `json:"-" gorm:"not null;default:0;index:idx_region_report_v2_delivery,priority:1"`
	NextAttemptAt  int64 `json:"-" gorm:"not null;default:0;index:idx_region_report_v2_delivery,priority:3"`
	Attempts       int64 `json:"-" gorm:"not null;default:0"`
}

func (RegionReportReceipt) TableName() string { return "region_quota_report_receipts_v2" }

func (report RegionReportV2) Validate() error {
	if report.Version != RegionReportProtocolVersion || report.Generation <= 0 || report.Seq <= 0 || report.Delta < -common.MaxWalletQuota || report.Delta > common.MaxWalletQuota ||
		report.SubjectType != RegionSubjectUser || !validReportRegion(report.Region) {
		return ErrInvalidRegionReport
	}
	id, err := strconv.Atoi(report.SubjectID)
	if err != nil || id <= 0 || strconv.Itoa(id) != report.SubjectID {
		return ErrInvalidRegionReport
	}
	if len(report.PayloadHash) != 64 || strings.ToLower(report.PayloadHash) != report.PayloadHash {
		return ErrInvalidRegionReport
	}
	if _, err := hex.DecodeString(report.PayloadHash); err != nil {
		return ErrInvalidRegionReport
	}
	return nil
}

func (report RegionReportV2) identity(db *gorm.DB) *gorm.DB {
	return db.Where("version = ? AND region = ? AND subject_type = ? AND subject_id = ? AND generation = ? AND seq = ?",
		report.Version, report.Region, report.SubjectType, report.SubjectID, report.Generation, report.Seq)
}

func readRegionReportReceipt(db *gorm.DB, report RegionReportV2) (RegionReportReceipt, error) {
	var receipt RegionReportReceipt
	err := report.identity(db).Take(&receipt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RegionReportReceipt{}, ErrRegionReportNotReady
	}
	if err != nil {
		return RegionReportReceipt{}, err
	}
	if receipt.RegionReportV2 != report {
		return RegionReportReceipt{}, ErrRegionReportConflict
	}
	return receipt, nil
}

// PendingRegionReportsV2 reads from the same SQL outbox the lease writer uses.
// A successful application removes the row from this view, not from the ledger.
func PendingRegionReportsV2(ctx context.Context, db *gorm.DB, region string, afterID int64, limit int) ([]RegionReportReceipt, error) {
	if !validReportRegion(region) || afterID < 0 || limit < 1 || limit > 500 {
		return nil, ErrInvalidRegionReport
	}
	var pending []RegionReportReceipt
	err := db.WithContext(ctx).Where("version = ? AND region = ? AND applied_at = 0 AND id > ?", RegionReportProtocolVersion, region, afterID).
		Order("id ASC").Limit(limit).Find(&pending).Error
	return pending, err
}

func ApplyRegionReport(ctx context.Context, report RegionReportV2) (RegionReportReceipt, error) {
	return applyStagedRegionReport(DB.WithContext(ctx), report)
}

func applyStagedRegionReport(db *gorm.DB, report RegionReportV2) (RegionReportReceipt, error) {
	if err := report.Validate(); err != nil {
		return RegionReportReceipt{}, err
	}
	receipt, err := readRegionReportReceipt(db, report)
	if err != nil || receipt.AppliedAt > 0 {
		return receipt, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		key := report.leaseKey()
		if _, err := lockRegionWallet(tx, key.UserID); err != nil {
			return err
		}
		receipt, err = readRegionReportReceipt(tx, report)
		if err != nil || receipt.AppliedAt > 0 {
			return err
		}
		lease, err := readRegionLeaseV2(tx, key)
		if err != nil {
			return err
		}
		if lease.AppliedSeq != report.Seq-1 || lease.StagedSeq < report.Seq || lease.PendingDebits < max(report.Delta, 0) {
			return ErrRegionReportNotReady
		}
		return commitRegionReport(tx, &receipt, lease)
	})
	if err != nil {
		return RegionReportReceipt{}, err
	}
	return receipt, nil
}

func commitRegionReport(tx *gorm.DB, receipt *RegionReportReceipt, lease RegionQuotaLeaseV2) error {
	now := common.GetTimestamp()
	if receipt.Delta < 0 && lease.active(now) {
		if -receipt.Delta > lease.Granted-lease.Available {
			return ErrRegionLeaseLedgerConflict
		}
		lease.Available -= receipt.Delta
	}
	if err := applyRegionReportWallet(tx, receipt.RegionReportV2); err != nil {
		return err
	}
	if err := updateRegionLease(tx, lease.ID, map[string]any{"applied_seq": receipt.Seq,
		"pending_debits": lease.PendingDebits - max(receipt.Delta, 0), "available": lease.Available}); err != nil {
		return err
	}
	commit := tx.Model(receipt).Where("applied_at = 0").UpdateColumn("applied_at", now)
	if commit.Error != nil {
		return commit.Error
	}
	if commit.RowsAffected != 1 {
		return ErrRegionReportNotReady
	}
	receipt.AppliedAt = now
	return nil
}

func applyRegionReportWallet(tx *gorm.DB, report RegionReportV2) error {
	if report.Delta == 0 {
		return nil
	}
	wallet := tx.Model(&User{}).Where("id = ?", report.leaseKey().UserID).
		Where("quota BETWEEN ? AND ?", max(report.Delta, 0), common.MaxWalletQuota+min(report.Delta, 0))
	update := wallet.UpdateColumn("quota", gorm.Expr("quota - ?", report.Delta))
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return ErrRegionReportBalance
	}
	return nil
}
