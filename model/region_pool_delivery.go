package model

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
)

func ClaimRegionReportsV2(ctx context.Context, db *gorm.DB, region string, limit int) ([]RegionReportReceipt, error) {
	if !validReportRegion(region) || limit < 1 || limit > 500 {
		return nil, ErrInvalidRegionReport
	}
	var claimed []RegionReportReceipt
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now, err := regionDeliveryTime(tx)
		if err != nil {
			return err
		}
		var ready []RegionReportReceipt
		err = tx.Where("version = ? AND region = ? AND acknowledged_at = 0 AND next_attempt_at <= ?", RegionReportProtocolVersion, region, now.Unix()).
			Where(`NOT EXISTS (SELECT 1 FROM region_quota_report_receipts_v2 AS prior
    WHERE prior.version = region_quota_report_receipts_v2.version
    AND prior.region = region_quota_report_receipts_v2.region
    AND prior.subject_type = region_quota_report_receipts_v2.subject_type
    AND prior.subject_id = region_quota_report_receipts_v2.subject_id
    AND prior.generation = region_quota_report_receipts_v2.generation
    AND prior.seq < region_quota_report_receipts_v2.seq AND prior.acknowledged_at = 0)`).
			Order("next_attempt_at ASC, id ASC").Limit(limit).Find(&ready).Error
		if err != nil {
			return err
		}
		for _, entry := range ready {
			attempt := min(entry.Attempts+1, 7)
			delay := (90 * time.Second) << (attempt - 1)
			result := tx.Model(&entry).Where("acknowledged_at = 0 AND next_attempt_at <= ?", now.Unix()).
				UpdateColumns(map[string]any{"next_attempt_at": now.Add(delay).Unix(), "attempts": attempt})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 1 {
				claimed = append(claimed, entry)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func regionDeliveryTime(tx *gorm.DB) (time.Time, error) {
	var now sql.NullTime
	if tx.Dialector.Name() == "sqlite" {
		var unix int64
		err := tx.Raw("SELECT CAST(strftime('%s','now') AS INTEGER)").Row().Scan(&unix)
		return time.Unix(unix, 0), err
	}
	err := tx.Raw("SELECT CURRENT_TIMESTAMP").Row().Scan(&now)
	return now.Time, err
}

func AcknowledgeRegionReportV2(ctx context.Context, db *gorm.DB, receipt RegionReportReceipt) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if receipt.AppliedAt <= 0 {
		return ErrRegionReportNotReady
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, err := readRegionReportReceipt(tx, receipt.RegionReportV2)
		if err != nil {
			return err
		}
		if stored.AppliedAt != receipt.AppliedAt {
			return ErrRegionReportNotReady
		}
		if stored.AcknowledgedAt != 0 {
			return nil
		}
		return tx.Model(&stored).Where("acknowledged_at = 0").UpdateColumn("acknowledged_at", stored.AppliedAt).Error
	})
}
