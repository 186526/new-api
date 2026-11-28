package model

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrRegionLeaseLedgerConflict = errors.New("regional lease generation or operation conflicts with the ledger")

type RegionLeaseKey struct {
	UserID     int    `gorm:"not null;uniqueIndex:idx_region_lease_v2_identity,priority:1"`
	Region     string `gorm:"size:64;not null;uniqueIndex:idx_region_lease_v2_identity,priority:2"`
	Generation int64  `gorm:"not null;uniqueIndex:idx_region_lease_v2_identity,priority:3"`
}

func (key RegionLeaseKey) valid() bool {
	return key.UserID > 0 && key.Generation > 0 && validReportRegion(key.Region)
}

func (key RegionLeaseKey) find(db *gorm.DB) *gorm.DB {
	return db.Where("user_id = ? AND region = ? AND generation = ?", key.UserID, key.Region, key.Generation)
}

type RegionQuotaLeaseV2 struct {
	ID             int64 `gorm:"primaryKey"`
	RegionLeaseKey `gorm:"embedded"`
	ExpiresAt      int64 `gorm:"not null"`
	ClosedAt       int64 `gorm:"not null"`
	Granted        int64 `gorm:"not null"`
	Available      int64 `gorm:"not null"`
	PendingDebits  int64 `gorm:"not null"`
	StagedSeq      int64 `gorm:"not null"`
	AppliedSeq     int64 `gorm:"not null"`
}

func (RegionQuotaLeaseV2) TableName() string { return "region_quota_leases_v2" }

func (lease RegionQuotaLeaseV2) active(now int64) bool {
	return lease.ClosedAt == 0 && lease.ExpiresAt > now
}

type RegionLeaseGrant struct {
	RegionLeaseKey
	Granted   int64
	ExpiresAt int64
}

// GrantRegionLeaseV2 sets an absolute grant on the nominated generation. Use it
// only once legacy writers are drained; the SQL wallet cannot see legacy leases.
func GrantRegionLeaseV2(ctx context.Context, db *gorm.DB, grant RegionLeaseGrant) (RegionQuotaLeaseV2, error) {
	if !grant.valid() || grant.Granted <= 0 || grant.Granted > common.MaxWalletQuota {
		return RegionQuotaLeaseV2{}, ErrInvalidRegionReport
	}
	var lease RegionQuotaLeaseV2
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		wallet, err := lockRegionWallet(tx, grant.UserID)
		if err != nil {
			return err
		}
		lease, err = saveRegionLeaseGrant(tx, wallet, grant)
		return err
	})
	if err != nil {
		return RegionQuotaLeaseV2{}, err
	}
	return lease, nil
}

func saveRegionLeaseGrant(tx *gorm.DB, wallet int64, grant RegionLeaseGrant) (RegionQuotaLeaseV2, error) {
	var last RegionQuotaLeaseV2
	err := tx.Where("user_id = ? AND region = ?", grant.UserID, grant.Region).Order("generation DESC").Take(&last).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return RegionQuotaLeaseV2{}, err
	}
	now := common.GetTimestamp()
	if grant.ExpiresAt <= now {
		return RegionQuotaLeaseV2{}, ErrRegionLeaseUnavailable
	}
	if last.Generation == grant.Generation {
		return renewRegionLease(tx, last, grant, wallet, now)
	}
	if last.Generation == math.MaxInt64 || last.Generation+1 != grant.Generation || last.active(now) {
		return RegionQuotaLeaseV2{}, ErrRegionLeaseLedgerConflict
	}
	if last.ID != 0 {
		if err := closeRegionLease(tx, last, now); err != nil {
			return RegionQuotaLeaseV2{}, err
		}
	}
	if err := checkRegionGrantHeadroom(tx, grant.UserID, wallet, grant.Granted); err != nil {
		return RegionQuotaLeaseV2{}, err
	}
	lease := RegionQuotaLeaseV2{RegionLeaseKey: grant.RegionLeaseKey, ExpiresAt: grant.ExpiresAt, Granted: grant.Granted, Available: grant.Granted}
	if err := tx.Create(&lease).Error; err != nil {
		return RegionQuotaLeaseV2{}, err
	}
	return lease, nil
}

func renewRegionLease(tx *gorm.DB, lease RegionQuotaLeaseV2, grant RegionLeaseGrant, wallet, now int64) (RegionQuotaLeaseV2, error) {
	if !lease.active(now) || grant.Granted < lease.Granted || grant.ExpiresAt < lease.ExpiresAt {
		return RegionQuotaLeaseV2{}, ErrRegionLeaseLedgerConflict
	}
	if grant.Granted == lease.Granted && grant.ExpiresAt == lease.ExpiresAt {
		return lease, nil
	}
	added := grant.Granted - lease.Granted
	if added > 0 {
		if err := checkRegionGrantHeadroom(tx, grant.UserID, wallet, added); err != nil {
			return RegionQuotaLeaseV2{}, err
		}
	}
	lease.Available += added
	lease.Granted, lease.ExpiresAt = grant.Granted, grant.ExpiresAt
	err := updateRegionLease(tx, lease.ID, map[string]any{"granted": lease.Granted, "available": lease.Available, "expires_at": lease.ExpiresAt})
	return lease, err
}

func checkRegionGrantHeadroom(tx *gorm.DB, userID int, wallet, added int64) error {
	var held int64
	// Keep each pending debit reserved until its own report applies. Netting a
	// pending refund first could leave insufficient funds when reports are
	// applied in debit-before-refund order.
	err := tx.Model(&RegionQuotaLeaseV2{}).Where("user_id = ?", userID).
		Select("COALESCE(SUM(available + pending_debits), 0)").Scan(&held).Error
	if err != nil {
		return err
	}
	if held < 0 || wallet < held || added > wallet-held {
		return ErrRegionLeaseUnavailable
	}
	return nil
}

func CloseRegionLeaseV2(ctx context.Context, db *gorm.DB, key RegionLeaseKey) error {
	if !key.valid() {
		return ErrInvalidRegionReport
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockRegionWallet(tx, key.UserID); err != nil {
			return err
		}
		lease, err := readRegionLeaseV2(tx, key)
		if err != nil {
			return err
		}
		return closeRegionLease(tx, lease, common.GetTimestamp())
	})
}

func closeRegionLease(tx *gorm.DB, lease RegionQuotaLeaseV2, now int64) error {
	if lease.ClosedAt != 0 {
		return nil
	}
	return updateRegionLease(tx, lease.ID, map[string]any{"closed_at": now, "available": 0})
}

func readRegionLeaseV2(db *gorm.DB, key RegionLeaseKey) (RegionQuotaLeaseV2, error) {
	var lease RegionQuotaLeaseV2
	err := key.find(db).Take(&lease).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrRegionLeaseUnavailable
	}
	return lease, err
}

// RebuildRegionLeaseSnapshot reads the authoritative state after a cache loss.
// A cache may display a snapshot but must not authorize spending from it.
func RebuildRegionLeaseSnapshot(ctx context.Context, db *gorm.DB, key RegionLeaseKey) (RegionQuotaLeaseV2, error) {
	if !key.valid() {
		return RegionQuotaLeaseV2{}, ErrInvalidRegionReport
	}
	lease, err := readRegionLeaseV2(db.WithContext(ctx), key)
	if !lease.active(common.GetTimestamp()) {
		lease.Available = 0
	}
	return lease, err
}

func lockRegionWallet(tx *gorm.DB, userID int) (int64, error) {
	var user User
	err := lockForUpdate(tx).Select("id", "quota").Where("id = ?", userID).Take(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrRegionReportBalance
	}
	return int64(user.Quota), err
}

func updateRegionLease(tx *gorm.DB, id int64, columns map[string]any) error {
	result := tx.Model(&RegionQuotaLeaseV2{}).Where("id = ?", id).UpdateColumns(columns)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrRegionLeaseUnavailable
	}
	return nil
}

func validReportRegion(region string) bool {
	return region != "" && len(region) <= 64 && strings.TrimSpace(region) == region && !strings.ContainsRune(region, '\x00')
}

func (report RegionReportV2) leaseKey() RegionLeaseKey {
	userID, _ := strconv.Atoi(report.SubjectID)
	return RegionLeaseKey{UserID: userID, Region: report.Region, Generation: report.Generation}
}
