package model

import (
	"errors"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

var ErrNonRequestQuotaConflict = errors.New("non-request quota operation conflicts with stored evidence")

type NonRequestQuotaReceipt struct {
	Kind      string `gorm:"primaryKey;size:32"`
	SourceID  string `gorm:"primaryKey;size:64"`
	UserID    int    `gorm:"not null;index"`
	ActorID   int    `gorm:"not null"`
	ActorRole int    `gorm:"not null"`
	Mode      string `gorm:"size:16;not null"`
	Amount    int    `gorm:"type:bigint;not null"`
	Before    int    `gorm:"type:bigint;not null"`
	After     int    `gorm:"type:bigint;not null"`
	RequestID string `gorm:"size:64;index"`
	CreatedAt int64  `gorm:"not null"`
}

func (receipt *NonRequestQuotaReceipt) matches(stored NonRequestQuotaReceipt) bool {
	return receipt.UserID == stored.UserID && receipt.ActorID == stored.ActorID &&
		receipt.ActorRole == stored.ActorRole && receipt.Mode == stored.Mode && receipt.Amount == stored.Amount
}

func (receipt *NonRequestQuotaReceipt) apply(tx *gorm.DB, user User, fields map[string]any, limitError error) error {
	if user.Quota < -common.MaxWalletQuota || user.Quota > common.MaxWalletQuota ||
		receipt.Amount < -common.MaxWalletQuota || receipt.Amount > common.MaxWalletQuota {
		return limitError
	}
	receipt.Before = user.Quota
	switch receipt.Mode {
	case "add":
		receipt.After = receipt.Before + receipt.Amount
	case "subtract":
		receipt.After = receipt.Before - receipt.Amount
	case "override":
		receipt.After = receipt.Amount
	default:
		return ErrNonRequestQuotaConflict
	}
	if receipt.After < -common.MaxWalletQuota || receipt.After > common.MaxWalletQuota {
		return limitError
	}
	if receipt.Before == receipt.After && len(fields) == 0 {
		return nil
	}
	updates := make(map[string]any, len(fields)+1)
	for field, value := range fields {
		updates[field] = value
	}
	updates["quota"] = receipt.After
	result := tx.Model(&User{}).Where("id = ? AND quota = ?", user.Id, receipt.Before).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNonRequestQuotaConflict
	}
	return nil
}

func (receipt *NonRequestQuotaReceipt) applyOnce(tx *gorm.DB, user User, fields map[string]any, limitError error) error {
	var stored NonRequestQuotaReceipt
	err := tx.Where("kind = ? AND source_id = ?", receipt.Kind, receipt.SourceID).Take(&stored).Error
	if err == nil {
		if !receipt.matches(stored) {
			return ErrNonRequestQuotaConflict
		}
		*receipt = stored
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err := receipt.apply(tx, user, fields, limitError); err != nil {
		return err
	}
	return receipt.record(tx)
}

func (receipt *NonRequestQuotaReceipt) record(tx *gorm.DB) error {
	if receipt.Kind == "" || receipt.SourceID == "" || len(receipt.SourceID) > 64 || len(receipt.RequestID) > 64 {
		return ErrNonRequestQuotaConflict
	}
	receipt.CreatedAt = common.GetTimestamp()
	if err := tx.Create(receipt).Error; err != nil {
		return err
	}
	return recordQuotaBalance(tx, QuotaBalancePublication{Kind: RefundPartWallet, SubjectID: receipt.UserID, Balance: int64(receipt.After)})
}

func nonRequestQuotaTransaction(write func(*gorm.DB) error) error {
	if SQLQuotaLedgerWriterEnabled() {
		return quotaSQLTransaction(DB, true, write)
	}
	return DB.Transaction(write)
}

func applyNonRequestCredit(tx *gorm.DB, receipt NonRequestQuotaReceipt, fields map[string]any) error {
	var user User
	if err := lockForUpdate(tx).Select("id", "quota").First(&user, receipt.UserID).Error; err != nil {
		return err
	}
	if err := receipt.apply(tx, user, fields, ErrTopUpQuotaLimitExceeded); err != nil {
		return err
	}
	return receipt.record(tx)
}

func quotaCreditReceipt(kind string, sourceID, userID, amount int) NonRequestQuotaReceipt {
	return NonRequestQuotaReceipt{
		Kind: kind, SourceID: strconv.Itoa(sourceID), UserID: userID, Mode: "add", Amount: amount,
	}
}

func recordRegistrationQuotaWithTx(tx *gorm.DB, user *User, inviterID int) error {
	if user.Quota < 0 || user.Quota > common.MaxWalletQuota {
		return ErrWalletQuotaLimitExceeded
	}
	if user.Quota > 0 {
		receipt := quotaCreditReceipt("registration", user.Id, user.Id, user.Quota)
		receipt.After = user.Quota
		if err := receipt.record(tx); err != nil {
			return err
		}
	}
	if inviterID == 0 || !operation_setting.IsPaymentComplianceConfirmed() {
		return nil
	}
	if common.QuotaForInvitee > 0 {
		receipt := quotaCreditReceipt("invitee", user.Id, user.Id, common.QuotaForInvitee)
		receipt.ActorID = inviterID
		if err := receipt.applyOnce(tx, *user, nil, ErrWalletQuotaLimitExceeded); err != nil {
			return err
		}
		user.Quota = receipt.After
	}
	if common.QuotaForInviter <= 0 {
		return nil
	}
	return recordInviterAwardWithTx(tx, user.Id, inviterID)
}

func recordInviterAwardWithTx(tx *gorm.DB, inviteeID, inviterID int) error {
	var inviter User
	if err := lockForUpdate(tx).Select("id", "aff_quota", "aff_history").First(&inviter, inviterID).Error; err != nil {
		return err
	}
	amount := common.QuotaForInviter
	if amount > common.MaxWalletQuota || inviter.AffQuota < 0 || inviter.AffHistoryQuota < 0 ||
		inviter.AffQuota > common.MaxWalletQuota-amount || inviter.AffHistoryQuota > common.MaxWalletQuota-amount {
		return ErrWalletQuotaLimitExceeded
	}
	result := tx.Model(&User{}).Where("id = ?", inviterID).Updates(map[string]any{
		"aff_quota": gorm.Expr("aff_quota + ?", amount), "aff_history": gorm.Expr("aff_history + ?", amount),
		"aff_count": gorm.Expr("aff_count + 1"),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	receipt := quotaCreditReceipt("inviter", inviteeID, inviterID, amount)
	receipt.ActorID = inviteeID
	receipt.Before, receipt.After = inviter.AffQuota, inviter.AffQuota+amount
	receipt.CreatedAt = common.GetTimestamp()
	return tx.Create(&receipt).Error
}

func TransferInvitationQuotaSQL(userID, quota int, requestID string) error {
	if userID <= 0 || quota <= 0 || quota > common.MaxWalletQuota || requestID == "" {
		return ErrInvalidUserQuotaAdjustment
	}
	if float64(quota) < common.QuotaPerUnit {
		return common.NewMessage("Minimum transfer quota is {{min}}!", map[string]any{"min": logger.FormatQuota(common.QuotaFromFloat(common.QuotaPerUnit))})
	}
	return quotaSQLTransaction(DB, true, func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "quota", "aff_quota").First(&user, userID).Error; err != nil {
			return err
		}
		receipt := NonRequestQuotaReceipt{
			Kind: "invitation_transfer", SourceID: requestID, RequestID: requestID,
			UserID: userID, ActorID: userID, Mode: "add", Amount: quota,
		}
		var stored NonRequestQuotaReceipt
		err := tx.Where("kind = ? AND source_id = ?", receipt.Kind, receipt.SourceID).Take(&stored).Error
		if err == nil {
			if !receipt.matches(stored) {
				return ErrNonRequestQuotaConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if user.AffQuota < quota {
			return common.NewMessage("Invitation quota is insufficient!")
		}
		if err := receipt.apply(tx, user, map[string]any{"aff_quota": user.AffQuota - quota}, ErrWalletQuotaLimitExceeded); err != nil {
			return err
		}
		return receipt.record(tx)
	})
}

func publishNonRequestCredit(userID, quota int, operation string) {
	if !SQLQuotaLedgerWriterEnabled() {
		syncCreditUserQuotaCache(userID, quota, operation)
	}
}
