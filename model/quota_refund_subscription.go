package model

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const subscriptionRefundVersion = 2

var errSubscriptionQuotaPeriodChanged = errors.New("subscription quota period changed")

func (sub *UserSubscription) resetQuotaUsage() error {
	if sub.QuotaGeneration == math.MaxInt64 {
		return errors.New("subscription quota generation exhausted")
	}
	sub.AmountUsed = 0
	sub.QuotaGeneration++
	return nil
}

// sqlSubscriptionDebit covers subscription funding only. Callers must not
// combine this with an untracked token debit or enable it with old reset writers.
type sqlSubscriptionDebit struct {
	ID, LogRequestID, Group string
	UserID, SubscriptionID  int
	version                 int
}

func (op sqlSubscriptionDebit) rows(tx *gorm.DB) *gorm.DB {
	version := op.version
	if version == 0 {
		version = subscriptionRefundVersion
	}
	return tx.Model(&QuotaRefundReceipt{}).Where("request_id = ? AND version = ?", op.ID, version)
}

func (op sqlSubscriptionDebit) lockBase(tx *gorm.DB) (QuotaRefundReceipt, error) {
	var base QuotaRefundReceipt
	err := op.rows(lockForUpdate(tx)).Where("part = ?", RefundPartSubscription).Take(&base).Error
	if err == nil && op.SubscriptionID != 0 && (base.SubjectID != op.SubscriptionID || base.Group != op.Group || base.LogRequestID != op.LogRequestID || (base.Quota != 0 && base.SubscriptionUserID != op.UserID)) {
		return base, errors.New("subscription operation conflicts with stored identity")
	}
	return base, err
}

func lockSubscriptionQuotaPeriod(tx *gorm.DB, subscriptionID int, now int64) (UserSubscription, error) {
	var sub UserSubscription
	err := lockForUpdate(tx).Where("id = ?", subscriptionID).Take(&sub).Error
	if err != nil || sub.Status != "active" || sub.EndTime <= now || (sub.NextResetTime > now && sub.NextResetTime > 0) {
		return sub, err
	}
	var plan SubscriptionPlan
	if err := tx.Where("id = ?", sub.PlanId).Take(&plan).Error; err != nil {
		return sub, err
	}
	plan.NormalizeDefaults()
	err = maybeResetUserSubscriptionWithPlanTx(tx, &sub, &plan, now)
	return sub, err
}

func (op sqlSubscriptionDebit) Reserve(baseAmount, extraTarget int) error {
	if strings.TrimSpace(op.ID) == "" || len(op.ID) > 64 || op.UserID <= 0 || op.SubscriptionID <= 0 || baseAmount <= 0 || baseAmount > common.MaxQuota || extraTarget < 0 || extraTarget > common.MaxQuota-baseAmount {
		return errors.New("invalid subscription quota reservation")
	}
	now := GetDBTimestamp()
	return quotaSQLTransaction(DB, true, func(tx *gorm.DB) error {
		anchor := QuotaRefundReceipt{RequestID: op.ID, Part: RefundPartSubscription, SubjectID: op.SubscriptionID,
			Group: op.Group, LogRequestID: op.LogRequestID, Version: subscriptionRefundVersion, Status: quotaDebitOpen}
		if err := insertQuotaDebitAnchor(tx, anchor); err != nil {
			return err
		}
		base, err := op.lockBase(tx)
		if err != nil {
			return err
		}
		if base.Status != quotaDebitOpen {
			return ErrQuotaOperationDecided
		}
		sub, err := lockSubscriptionQuotaPeriod(tx, op.SubscriptionID, now)
		if err != nil {
			return err
		}
		if sub.UserId != op.UserID || sub.Status != "active" || sub.EndTime <= now || (base.Quota != 0 && sub.QuotaGeneration != base.SubscriptionGeneration) {
			return errSubscriptionQuotaPeriodChanged
		}
		return op.reserveParts(tx, &sub, base, baseAmount, extraTarget)
	})
}

func (op sqlSubscriptionDebit) extra(tx *gorm.DB, base QuotaRefundReceipt) (QuotaRefundReceipt, error) {
	var extra QuotaRefundReceipt
	err := op.rows(tx).Where("part = ?", RefundPartSubscriptionExtra).Take(&extra).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return QuotaRefundReceipt{}, nil
	}
	if err == nil && (extra.SubjectID != base.SubjectID || extra.SubscriptionUserID != base.SubscriptionUserID || extra.SubscriptionGeneration != base.SubscriptionGeneration || extra.Status != base.Status || extra.Quota <= 0 || extra.Quota > common.MaxQuota-base.Quota) {
		return extra, errors.New("invalid subscription extra quota evidence")
	}
	return extra, err
}

func (op sqlSubscriptionDebit) reserveParts(tx *gorm.DB, sub *UserSubscription, base QuotaRefundReceipt, baseAmount, extraTarget int) error {
	if base.Quota != 0 && base.Quota != baseAmount {
		return errors.New("subscription base reservation amount changed")
	}
	extra, err := op.extra(tx, base)
	if err != nil {
		return err
	}
	extraTarget = max(extraTarget, extra.Quota)
	delta := baseAmount - base.Quota + extraTarget - extra.Quota
	if delta == 0 {
		return nil
	}
	if err := changeCommittedSubscriptionUsage(tx, sub, int64(delta)); err != nil {
		return err
	}
	if base.Quota == 0 {
		if err := op.rows(tx).Where("part = ?", RefundPartSubscription).Updates(map[string]any{
			"quota": baseAmount, "subscription_generation": sub.QuotaGeneration, "subscription_user_id": sub.UserId,
		}).Error; err != nil {
			return err
		}
	}
	if extraTarget == extra.Quota {
		return nil
	}
	if extra.Quota != 0 {
		return op.rows(tx).Where("part = ?", RefundPartSubscriptionExtra).Update("quota", extraTarget).Error
	}
	base.Part, base.Quota, base.SubscriptionGeneration = RefundPartSubscriptionExtra, extraTarget, sub.QuotaGeneration
	base.SubscriptionUserID = sub.UserId
	return tx.Create(&base).Error
}

func changeCommittedSubscriptionUsage(tx *gorm.DB, sub *UserSubscription, delta int64) error {
	if sub.AmountUsed < 0 || sub.AmountUsed > math.MaxInt64-max(delta, 0) || sub.AmountUsed+delta < 0 || (delta > 0 && sub.AmountTotal > 0 && sub.AmountUsed+delta > sub.AmountTotal) {
		return errors.New("subscription quota insufficient or inconsistent")
	}
	if delta == 0 {
		return nil
	}
	result := tx.Model(&UserSubscription{}).Where("id = ? AND user_id = ? AND quota_generation = ?", sub.Id, sub.UserId, sub.QuotaGeneration).
		Update("amount_used", sub.AmountUsed+delta)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("subscription quota update did not match its owner")
	}
	sub.AmountUsed += delta
	return nil
}

func (op sqlSubscriptionDebit) Settle(actual int) error {
	if actual < 0 || actual > common.MaxQuota || op.UserID <= 0 || op.SubscriptionID <= 0 {
		return errors.New("invalid subscription settlement")
	}
	now := GetDBTimestamp()
	return quotaSQLTransaction(DB, false, func(tx *gorm.DB) error {
		base, err := op.lockBase(tx)
		if err != nil || base.Status == quotaDebitSettled {
			return err
		}
		if base.Status != quotaDebitOpen {
			return ErrQuotaOperationDecided
		}
		extra, err := op.extra(tx, base)
		if err != nil {
			return err
		}
		delta := actual - base.Quota - extra.Quota
		if delta != 0 {
			sub, err := lockSubscriptionQuotaPeriod(tx, base.SubjectID, now)
			if err != nil {
				return err
			}
			if sub.UserId != op.UserID || sub.Status != "active" || sub.EndTime <= now || sub.QuotaGeneration != base.SubscriptionGeneration {
				return errSubscriptionQuotaPeriodChanged
			}
			if err := changeCommittedSubscriptionUsage(tx, &sub, int64(delta)); err != nil {
				return err
			}
		}
		return op.rows(tx).Where("status = ?", quotaDebitOpen).Update("status", quotaDebitSettled).Error
	})
}

func (op sqlSubscriptionDebit) DecideRefund() error {
	if op.UserID <= 0 || op.SubscriptionID <= 0 {
		return errors.New("invalid subscription refund decision")
	}
	return quotaSQLTransaction(DB, false, func(tx *gorm.DB) error {
		base, err := op.lockBase(tx)
		if err != nil {
			return err
		}
		if base.Status == quotaDebitSettled {
			return ErrQuotaOperationDecided
		}
		return op.rows(tx).Where("status = ?", quotaDebitOpen).Updates(map[string]any{
			"status": RefundRequested, "requested_at": common.GetTimestamp(),
		}).Error
	})
}

func applyCommittedSubscriptionRefund(ctx context.Context, operationID string) error {
	op := sqlSubscriptionDebit{ID: operationID}
	now := GetDBTimestamp()
	return quotaSQLTransaction(DB.WithContext(ctx), false, func(tx *gorm.DB) error {
		base, err := op.lockBase(tx)
		if err != nil || base.Status == RefundCompleted || base.Status == RefundFailed {
			return err
		}
		if base.Status != RefundRequested || base.Quota <= 0 || base.Quota > common.MaxQuota {
			return ErrQuotaOperationDecided
		}
		extra, err := op.extra(tx, base)
		if err != nil {
			return err
		}
		restored, reason, status, err := restoreCommittedSubscription(tx, base, extra, now)
		if err != nil {
			return err
		}
		return finishSubscriptionRefundParts(tx, base, extra, restored, reason, status)
	})
}

func restoreCommittedSubscription(tx *gorm.DB, base, extra QuotaRefundReceipt, now int64) (int, string, string, error) {
	sub, err := lockSubscriptionQuotaPeriod(tx, base.SubjectID, now)
	if errors.Is(err, gorm.ErrRecordNotFound) && sub.Id == 0 {
		return 0, "subscription_missing", RefundFailed, nil
	}
	if err != nil {
		return 0, "", RefundRequested, err
	}
	if base.SubscriptionUserID <= 0 || sub.UserId != base.SubscriptionUserID {
		return 0, "owner_mismatch", RefundFailed, nil
	}
	if !subscriptionPeriodOpen(sub, base, now) {
		return 0, "period_closed", RefundCompleted, nil
	}
	quota := int64(base.Quota + extra.Quota)
	restored := min(quota, max(int64(0), sub.AmountUsed))
	if err := changeCommittedSubscriptionUsage(tx, &sub, -restored); err != nil {
		return 0, "", RefundRequested, err
	}
	if restored != quota {
		return int(restored), "partially_restored", RefundFailed, nil
	}
	return int(restored), "restored", RefundCompleted, nil
}

func finishSubscriptionRefundParts(tx *gorm.DB, base, extra QuotaRefundReceipt, restored int, reason, status string) error {
	for _, part := range []QuotaRefundReceipt{base, extra} {
		if part.Quota == 0 {
			continue
		}
		amount := min(restored, part.Quota)
		partStatus, partReason := status, reason
		if amount == part.Quota {
			partStatus, partReason = RefundCompleted, "restored"
		}
		result := quotaRefundSelection(tx, part).Where("version = ? AND status = ?", part.Version, RefundRequested).
			Updates(map[string]any{"status": partStatus, "restored": amount, "restore_reason": partReason})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("subscription refund does not match committed quota evidence")
		}
		restored -= amount
	}
	return nil
}
