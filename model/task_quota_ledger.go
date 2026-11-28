package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const quotaDebitTaskOwned = "task_debited"

// InsertWithWalletDebit transfers an open request debit to exactly one task.
func (t *Task) InsertWithWalletDebit(ctx context.Context, op SQLQuotaDebit, omits ...string) error {
	if t.ID != 0 || t.UserId != op.UserID || t.Group != op.Group || t.TaskID == "" {
		return ErrQuotaOperationDecided
	}
	candidate := *t
	candidate.PrivateData.WalletDebit = &op
	if err := quotaSQLTransaction(DB.WithContext(ctx), false, func(tx *gorm.DB) error {
		var wallet QuotaRefundReceipt
		if err := op.lockWallet(tx, &wallet); err != nil {
			return err
		}
		if wallet.Status != quotaDebitOpen || wallet.Quota <= 0 || wallet.Quota > common.MaxQuota {
			return ErrQuotaOperationDecided
		}
		if err := op.updateOpenParts(tx, map[string]any{"status": quotaDebitTaskOwned}); err != nil {
			return err
		}
		if candidate.Status == TaskStatusSuccess || candidate.Status == TaskStatusFailure {
			if err := finishTaskWalletDebit(tx, op, wallet, candidate.Status, candidate.Quota); err != nil {
				return err
			}
		} else if candidate.Quota != wallet.Quota {
			return ErrQuotaOperationDecided
		}
		return tx.Omit(omits...).Create(&candidate).Error
	}); err != nil {
		return err
	}
	*t = candidate
	return nil
}

// FinishWithWalletDebit writes the terminal state and its accounting decision together.
func (t *Task) FinishWithWalletDebit(ctx context.Context, fromStatus TaskStatus, actual int) (bool, error) {
	if t.ID == 0 || t.Status == fromStatus || fromStatus == TaskStatusSuccess || fromStatus == TaskStatusFailure {
		return false, ErrQuotaOperationDecided
	}
	candidate := *t
	candidate.Quota = actual
	won := false
	err := quotaSQLTransaction(DB.WithContext(ctx), false, func(tx *gorm.DB) error {
		var stored Task
		if err := lockForUpdate(tx).Where("id = ?", t.ID).Take(&stored).Error; err != nil {
			return err
		}
		if stored.Status != fromStatus {
			return nil
		}
		op := stored.PrivateData.WalletDebit
		if op == nil || t.PrivateData.WalletDebit == nil || *op != *t.PrivateData.WalletDebit ||
			stored.TaskID != t.TaskID || stored.UserId != t.UserId || stored.Group != t.Group ||
			op.UserID != stored.UserId || op.Group != stored.Group {
			return ErrQuotaOperationDecided
		}
		var wallet QuotaRefundReceipt
		if err := op.lockWallet(tx, &wallet); err != nil {
			return err
		}
		if wallet.Status != quotaDebitTaskOwned || wallet.Quota != stored.Quota {
			return ErrQuotaOperationDecided
		}
		if err := finishTaskWalletDebit(tx, *op, wallet, candidate.Status, actual); err != nil {
			return err
		}
		result := tx.Model(&candidate).Where("status = ?", fromStatus).Select("*").Updates(&candidate)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrQuotaOperationDecided
		}
		won = true
		return nil
	})
	if err == nil && won {
		*t = candidate
	}
	return won && err == nil, err
}

func finishTaskWalletDebit(tx *gorm.DB, op SQLQuotaDebit, wallet QuotaRefundReceipt, status TaskStatus, actual int) error {
	if wallet.Quota <= 0 || wallet.Quota > common.MaxQuota || actual < 0 || actual > common.MaxQuota {
		return ErrQuotaOperationDecided
	}
	var fields map[string]any
	switch status {
	case TaskStatusFailure:
		if actual != 0 {
			return ErrQuotaOperationDecided
		}
		fields = map[string]any{"status": RefundRequested, "requested_at": common.GetTimestamp()}
	case TaskStatusSuccess:
		delta := actual - wallet.Quota
		if op.TokenID != 0 {
			if err := updateSQLDebitToken(tx, op.UserID, op.TokenID, delta, delta > 0); err != nil {
				return err
			}
		}
		if err := updateSQLDebitWallet(tx, op.UserID, delta, delta > 0); err != nil {
			return err
		}
		fields = settlementFields(actual, delta, delta, "settled")
	default:
		return errors.New("quota decision requires a terminal task status")
	}
	result := op.debits(tx).Where("status = ? AND part IN ?", quotaDebitTaskOwned, []string{RefundPartWallet, RefundPartToken}).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	expected := int64(1)
	if op.TokenID != 0 {
		expected++
	}
	if result.RowsAffected != expected {
		return ErrQuotaOperationDecided
	}
	return nil
}
