package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RefundPartWallet            = "wallet"
	RefundPartToken             = "token"
	RefundPartSubscription      = "subscription"
	RefundPartSubscriptionExtra = "subscription_extra"
	RefundPartPreconsumeToken   = "preconsume_token"
)

type QuotaRefundReceipt struct {
	RequestID              string `gorm:"primaryKey;size:64"`
	Part                   string `gorm:"primaryKey;size:32"`
	SubjectID              int
	Quota                  int
	Group                  string `gorm:"size:64"`
	RequestedAt            int64  `gorm:"index"`
	Status                 string `gorm:"size:16;not null;index:idx_quota_refund_recover,priority:2"`
	Version                int    `gorm:"not null;default:0;index:idx_quota_refund_recover,priority:1"`
	LogRequestID           string `gorm:"size:64;index"`
	Restored               int    `gorm:"not null;default:0"`
	SubscriptionGeneration int64  `gorm:"type:bigint;not null;default:0"`
	SubscriptionUserID     int    `gorm:"not null;default:0"`
	RestoreReason          string `gorm:"size:32;not null;default:''"`
	SubscriptionTokenID    int    `gorm:"not null;default:0"`
	SettlementQuota        int    `gorm:"not null;default:0"`
	SettlementDelta        int    `gorm:"not null;default:0"`
	UncollectedQuota       int    `gorm:"not null;default:0"`
	Attempts               int    `gorm:"not null;default:0"`
	RetryAt                int64  `gorm:"not null;default:0;index:idx_quota_refund_recover,priority:3"`
}

func RegisterQuotaRefunds(receipts []QuotaRefundReceipt) error {
	if len(receipts) == 0 || DB == nil {
		return fmt.Errorf("quota refund receipts require a request ID and a database")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, receipt := range receipts {
			if receipt.RequestID == "" || receipt.SubjectID <= 0 || receipt.Quota <= 0 || receipt.Version != 0 || receipt.Status != RefundRequested {
				return fmt.Errorf("invalid legacy quota refund receipt")
			}
			if err := tx.Select("RequestID", "Part", "SubjectID", "Quota", "Group", "RequestedAt", "Status").Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error; err != nil {
				return err
			}
			var stored QuotaRefundReceipt
			if err := legacyQuotaRefundSelection(tx, receipt).Take(&stored).Error; err != nil {
				return fmt.Errorf("quota refund receipt conflicts with stored subject or amount: %w", err)
			}
			if stored.Group != receipt.Group {
				return fmt.Errorf("quota refund receipt conflicts with stored group")
			}
		}
		return nil
	})
}

func quotaRefundSelection(db *gorm.DB, receipt QuotaRefundReceipt) *gorm.DB {
	return db.Model(&QuotaRefundReceipt{}).Where("request_id = ? AND part = ? AND subject_id = ? AND quota = ?", receipt.RequestID, receipt.Part, receipt.SubjectID, receipt.Quota)
}

func legacyQuotaRefundSelection(db *gorm.DB, receipt QuotaRefundReceipt) *gorm.DB {
	query := quotaRefundSelection(db, receipt)
	if quotaRefundSchemaReady.Load() {
		return query.Where("version = 0")
	}
	return query
}

func ApplySQLQuotaRefund(receipt QuotaRefundReceipt) (string, error) {
	if receipt.Quota <= 0 || receipt.RequestID == "" || receipt.Version != 0 {
		return RefundRequested, fmt.Errorf("invalid legacy quota refund receipt")
	}
	if receipt.Part == RefundPartWallet {
		if err := common.ValidateWalletQuota(receipt.Quota); err != nil {
			return RefundRequested, err
		}
	}
	status := RefundRequested
	err := DB.Transaction(func(tx *gorm.DB) error {
		claimed := legacyQuotaRefundSelection(tx, receipt).Where("status = ?", RefundRequested).Update("status", RefundCompleted)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected == 0 {
			var existing QuotaRefundReceipt
			if err := legacyQuotaRefundSelection(tx, receipt).Take(&existing).Error; err != nil {
				return fmt.Errorf("refund receipt cannot be verified: %v", err)
			}
			status = existing.Status
			return nil
		}
		result, err := creditSQLRefund(tx, receipt)
		if err != nil {
			return err
		}
		status = RefundCompleted
		if result == 0 {
			status = RefundFailed
			return legacyQuotaRefundSelection(tx, receipt).Update("status", status).Error
		}
		return nil
	})
	if err != nil {
		return RefundRequested, err
	}
	return status, nil
}

func creditSQLRefund(tx *gorm.DB, receipt QuotaRefundReceipt) (int64, error) {
	var result *gorm.DB
	switch receipt.Part {
	case RefundPartWallet:
		result = tx.Model(&User{}).Where("id = ? AND quota <= ?", receipt.SubjectID, common.MaxWalletQuota-receipt.Quota).
			Update("quota", gorm.Expr("quota + ?", receipt.Quota))
	case RefundPartToken, RefundPartPreconsumeToken:
		result = tx.Model(&Token{}).Where("id = ?", receipt.SubjectID).Updates(map[string]any{
			"remain_quota":  gorm.Expr("remain_quota + ?", receipt.Quota),
			"used_quota":    gorm.Expr("used_quota - ?", receipt.Quota),
			"accessed_time": common.GetTimestamp(),
		})
	default:
		return 0, fmt.Errorf("refund part %q does not support direct SQL credit", receipt.Part)
	}
	return result.RowsAffected, result.Error
}

func RecordQuotaRefundFailure(receipt QuotaRefundReceipt) error {
	return legacyQuotaRefundSelection(DB, receipt).Where("status = ?", RefundRequested).Update("status", RefundFailed).Error
}

func QuotaRefundOutcomes(requestIDs []string) (map[string]string, error) {
	var receipts []QuotaRefundReceipt
	if len(requestIDs) == 0 {
		return nil, nil
	}
	query := DB.Where("request_id IN ?", requestIDs)
	if quotaRefundSchemaReady.Load() {
		query = DB.Where("(version = 0 AND request_id IN ?) OR (version IN ? AND log_request_id IN ?)", requestIDs, []int{quotaRefundVersion, subscriptionRefundVersion, subscriptionTokenRefundVersion}, requestIDs)
	}
	if err := query.Where("status IN ?", []string{RefundRequested, RefundCompleted, RefundFailed}).Find(&receipts).Error; err != nil {
		return nil, err
	}
	outcomes := make(map[string]string, len(requestIDs))
	for _, receipt := range receipts {
		requestID := receipt.RequestID
		if receipt.Version == quotaRefundVersion || receipt.Version == subscriptionRefundVersion || receipt.Version == subscriptionTokenRefundVersion {
			requestID = receipt.LogRequestID
		}
		status := outcomes[requestID]
		if status == "" || status == RefundCompleted || receipt.Status == RefundFailed {
			outcomes[requestID] = receipt.Status
		}
	}
	return outcomes, nil
}
