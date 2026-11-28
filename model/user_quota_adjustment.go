package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var (
	ErrInvalidUserQuotaAdjustment = errors.New("invalid user quota adjustment")
	ErrUserQuotaPermission        = errors.New("cannot adjust quota for this user role")
)

// UserQuotaAdjustment is the immutable database snapshot of a committed manual
// adjustment. Pending relay deductions in the quota cache are not part of it.
type UserQuotaAdjustment struct {
	UserID   int
	Username string
	Before   int
	After    int
}

type UserQuotaAdjustmentActor struct {
	ID        int
	Role      int
	RequestID string
}

func AdjustUserQuota(userID, operatorRole int, mode string, value int) (*UserQuotaAdjustment, error) {
	return AdjustUserQuotaWithActor(userID, mode, value, UserQuotaAdjustmentActor{Role: operatorRole})
}

func AdjustUserQuotaWithActor(userID int, mode string, value int, actor UserQuotaAdjustmentActor) (*UserQuotaAdjustment, error) {
	if userID <= 0 || (mode != "add" && mode != "subtract" && mode != "override") {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	if mode != "override" && value <= 0 {
		return nil, ErrInvalidUserQuotaAdjustment
	}
	if value > common.MaxWalletQuota || value < -common.MaxWalletQuota {
		return nil, ErrWalletQuotaLimitExceeded
	}

	var adjustment UserQuotaAdjustment
	err := nonRequestQuotaTransaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if actor.Role != common.RoleRootUser && actor.Role <= user.Role {
			return ErrUserQuotaPermission
		}
		if SQLQuotaLedgerWriterEnabled() {
			if actor.ID <= 0 || actor.RequestID == "" {
				return ErrInvalidUserQuotaAdjustment
			}
			receipt := NonRequestQuotaReceipt{
				Kind: "admin_adjustment", SourceID: actor.RequestID, RequestID: actor.RequestID,
				UserID: userID, ActorID: actor.ID, ActorRole: actor.Role, Mode: mode, Amount: value,
			}
			if err := receipt.applyOnce(tx, user, nil, ErrWalletQuotaLimitExceeded); err != nil {
				return err
			}
			adjustment = UserQuotaAdjustment{UserID: user.Id, Username: user.Username, Before: receipt.Before, After: receipt.After}
			return nil
		}
		if user.Quota > common.MaxWalletQuota || user.Quota < -common.MaxWalletQuota {
			return ErrWalletQuotaLimitExceeded
		}
		quota := decimal.NewFromInt(int64(value))
		switch mode {
		case "add":
			quota = decimal.NewFromInt(int64(user.Quota)).Add(quota)
		case "subtract":
			quota = decimal.NewFromInt(int64(user.Quota)).Sub(quota)
		}
		after, err := common.WalletQuotaFromDecimalStrict(quota)
		if err != nil {
			return ErrWalletQuotaLimitExceeded
		}
		// An unchanged override is a successful operation, including on MySQL
		// configurations that count only changed rows in RowsAffected.
		if after != user.Quota {
			result := tx.Model(&User{}).Where("id = ?", userID).Update("quota", after)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		adjustment = UserQuotaAdjustment{UserID: user.Id, Username: user.Username, Before: user.Quota, After: after}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Apply only the committed difference, preserving outstanding reservations.
	// Both balances are bounded above, so their difference fits in int64.
	delta := int64(adjustment.After) - int64(adjustment.Before)
	if delta != 0 && !SQLQuotaLedgerWriterEnabled() {
		// The transaction already wrote the row, so the ledger moves by the
		// committed difference without ever being rebuilt from that row.
		if err := syncRowCommittedUserQuota(userID, delta); err != nil {
			common.SysError(common.LogText("failed to sync manual quota adjustment for user %d: %s", userID, err))
		}
	}
	return &adjustment, nil
}
