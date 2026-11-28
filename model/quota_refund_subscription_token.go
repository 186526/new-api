package model

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const subscriptionTokenRefundVersion = 3

var ErrQuotaSubscriptionInsufficient = errors.New("subscription quota insufficient")

type SQLSubscriptionDebit struct {
	ID, LogRequestID, Group string
	UserID, TokenID         int // TokenID is zero for Playground.
}

type SQLSubscriptionReservation struct {
	SubscriptionPreConsumeResult
	SubscriptionPlanInfo
}

func (op SQLSubscriptionDebit) subscription() sqlSubscriptionDebit {
	return sqlSubscriptionDebit{ID: op.ID, LogRequestID: op.LogRequestID, Group: op.Group, UserID: op.UserID, version: subscriptionTokenRefundVersion}
}

func (op SQLSubscriptionDebit) lockParts(tx *gorm.DB) (base, extra, token QuotaRefundReceipt, err error) {
	ledger := op.subscription()
	base, err = ledger.lockBase(tx)
	if err != nil {
		return
	}
	if base.Group != op.Group || base.LogRequestID != op.LogRequestID || (base.Quota != 0 && base.SubscriptionUserID != op.UserID) {
		err = errors.New("subscription operation conflicts with stored identity")
		return
	}
	extra, err = ledger.extra(tx, base)
	if err == nil && base.Quota != 0 {
		token, err = readSubscriptionTokenPart(tx, ledger, base, extra)
		if err == nil && token.SubjectID != op.TokenID {
			err = errors.New("subscription operation conflicts with stored token")
		}
	}
	return
}

func readSubscriptionTokenPart(tx *gorm.DB, ledger sqlSubscriptionDebit, base, extra QuotaRefundReceipt) (QuotaRefundReceipt, error) {
	var token QuotaRefundReceipt
	err := ledger.rows(tx).Where("part = ?", RefundPartToken).Take(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && base.SubscriptionTokenID == 0 {
		return QuotaRefundReceipt{}, nil
	}
	if err == nil && (token.SubjectID <= 0 || token.SubjectID != base.SubscriptionTokenID || token.Quota != base.Quota+extra.Quota || token.SubscriptionUserID != base.SubscriptionUserID || token.Status != base.Status) {
		return token, errors.New("invalid subscription token debit evidence")
	}
	return token, err
}

func (op SQLSubscriptionDebit) Reserve(baseAmount, target int) (SQLSubscriptionReservation, error) {
	var reserved SQLSubscriptionReservation
	if strings.TrimSpace(op.ID) == "" || len(op.ID) > 64 || op.UserID <= 0 || op.TokenID < 0 || baseAmount <= 0 || target < baseAmount || target > common.MaxQuota {
		return reserved, errors.New("invalid subscription quota reservation")
	}
	now := GetDBTimestamp()
	ledger := op.subscription()
	err := quotaSQLTransaction(DB, true, func(tx *gorm.DB) error {
		anchor := QuotaRefundReceipt{RequestID: op.ID, Part: RefundPartSubscription, Group: op.Group, LogRequestID: op.LogRequestID, Version: subscriptionTokenRefundVersion, Status: quotaDebitOpen}
		if err := insertQuotaDebitAnchor(tx, anchor); err != nil {
			return err
		}
		base, extra, token, err := op.lockParts(tx)
		if err != nil {
			return err
		}
		if base.Status != quotaDebitOpen {
			return ErrQuotaOperationDecided
		}
		if base.Quota != 0 && base.Quota != baseAmount {
			return errors.New("subscription base reservation amount changed")
		}
		if base.Quota != 0 && target < base.Quota+extra.Quota {
			return errors.New("subscription reservation target regressed")
		}
		sub, plan, err := op.reservePeriod(tx, base, target, now)
		if err != nil {
			return err
		}
		if base.Quota == 0 {
			base.SubjectID = sub.Id
			if err := ledger.rows(tx).Where("part = ?", RefundPartSubscription).Updates(map[string]any{"subject_id": sub.Id, "subscription_token_id": op.TokenID}).Error; err != nil {
				return err
			}
		}
		if err := ledger.reserveParts(tx, &sub, base, baseAmount, target-baseAmount); err != nil {
			return err
		}
		if op.TokenID != 0 && target != token.Quota {
			if err := changeSubscriptionTokenQuota(tx, op.UserID, op.TokenID, target-token.Quota, true); err != nil {
				return err
			}
			if token.Quota == 0 {
				newToken := QuotaRefundReceipt{RequestID: op.ID, Part: RefundPartToken, SubjectID: op.TokenID, Quota: target, Group: op.Group, LogRequestID: op.LogRequestID,
					Version: subscriptionTokenRefundVersion, Status: quotaDebitOpen, SubscriptionUserID: op.UserID}
				if err := tx.Create(&newToken).Error; err != nil {
					return err
				}
			} else if err := ledger.rows(tx).Where("part = ?", RefundPartToken).Update("quota", target).Error; err != nil {
				return err
			}
		}
		reserved = SQLSubscriptionReservation{
			SubscriptionPreConsumeResult: SubscriptionPreConsumeResult{UserSubscriptionId: sub.Id, PreConsumed: int64(baseAmount), AmountTotal: sub.AmountTotal, AmountUsedAfter: sub.AmountUsed},
			SubscriptionPlanInfo:         SubscriptionPlanInfo{PlanId: sub.PlanId, PlanTitle: plan.Title},
		}
		return nil
	})
	return reserved, err
}

func (op SQLSubscriptionDebit) reservePeriod(tx *gorm.DB, base QuotaRefundReceipt, target int, now int64) (UserSubscription, *SubscriptionPlan, error) {
	if base.Quota > 0 {
		sub, err := lockSubscriptionQuotaPeriod(tx, base.SubjectID, now)
		if err != nil {
			return sub, nil, err
		}
		if !subscriptionPeriodOpen(sub, base, now) {
			return sub, nil, errSubscriptionQuotaPeriodChanged
		}
		plan, err := readDebitSubscriptionPlan(tx, sub.PlanId)
		return sub, plan, err
	}
	var candidates []UserSubscription
	if err := lockForUpdate(tx).Where("user_id = ? AND status = ? AND end_time > ?", op.UserID, "active", now).Order("end_time asc, id asc").Find(&candidates).Error; err != nil {
		return UserSubscription{}, nil, err
	}
	for _, sub := range candidates {
		plan, err := readDebitSubscriptionPlan(tx, sub.PlanId)
		if err != nil {
			return sub, nil, err
		}
		if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
			return sub, nil, err
		}
		if sub.AmountTotal <= 0 || sub.AmountTotal-sub.AmountUsed >= int64(target) {
			return sub, plan, nil
		}
	}
	return UserSubscription{}, nil, ErrQuotaSubscriptionInsufficient
}

func readDebitSubscriptionPlan(tx *gorm.DB, id int) (*SubscriptionPlan, error) {
	var plan SubscriptionPlan
	if err := tx.Where("id = ?", id).Take(&plan).Error; err != nil {
		return nil, err
	}
	plan.NormalizeDefaults()
	return &plan, nil
}

func subscriptionPeriodOpen(sub UserSubscription, base QuotaRefundReceipt, now int64) bool {
	return sub.UserId == base.SubscriptionUserID && sub.Status == "active" && sub.EndTime > now && sub.QuotaGeneration == base.SubscriptionGeneration
}

func changeSubscriptionTokenQuota(tx *gorm.DB, userID, tokenID, delta int, requireAvailable bool) error {
	if delta == 0 {
		return nil
	}
	query := tx.Model(&Token{}).Where("id = ? AND user_id = ?", tokenID, userID).
		Where("remain_quota BETWEEN ? AND ? AND used_quota BETWEEN ? AND ?", -common.MaxWalletQuota+max(delta, 0), common.MaxWalletQuota+min(delta, 0), max(-delta, 0), common.MaxWalletQuota-max(delta, 0))
	if requireAvailable && delta > 0 {
		query = query.Where("(unlimited_quota = ? OR remain_quota >= ?)", true, delta)
	}
	result := query.Updates(map[string]any{
		"remain_quota": gorm.Expr("remain_quota - ?", delta), "used_quota": gorm.Expr("used_quota + ?", delta), "accessed_time": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQuotaTokenInsufficient
	}
	return recordTokenBalance(tx, tokenID)
}

func (op SQLSubscriptionDebit) Settle(actual int) (int, error) {
	if actual < 0 || actual > common.MaxQuota {
		return 0, errors.New("invalid subscription settlement amount")
	}
	now := GetDBTimestamp()
	var applied int
	err := quotaSQLTransaction(DB, false, func(tx *gorm.DB) error {
		base, extra, token, err := op.lockParts(tx)
		if err != nil {
			return err
		}
		if base.Status == quotaDebitSettled {
			applied = base.SettlementDelta
			if actual != base.SettlementQuota {
				return ErrQuotaOperationDecided
			}
			return nil
		}
		if base.Status != quotaDebitOpen || base.Quota <= 0 {
			return ErrQuotaOperationDecided
		}
		delta := actual - base.Quota - extra.Quota
		applied = delta
		reason := "settled"
		if delta != 0 {
			sub, err := lockSubscriptionQuotaPeriod(tx, base.SubjectID, now)
			if err != nil && !(errors.Is(err, gorm.ErrRecordNotFound) && sub.Id == 0) {
				return err
			}
			if sub.Id != 0 && sub.UserId != base.SubscriptionUserID {
				return errors.New("subscription settlement owner mismatch")
			}
			if !subscriptionPeriodOpen(sub, base, now) {
				applied, reason = 0, "period_closed"
			} else if err := changeCommittedSubscriptionUsage(tx, &sub, int64(delta)); err != nil {
				return err
			}
		}
		if token.SubjectID != 0 {
			tokenDelta := min(delta, applied)
			if err := changeSubscriptionTokenQuota(tx, base.SubscriptionUserID, token.SubjectID, tokenDelta, false); err != nil {
				return err
			}
			if err := updateOpenSubscriptionPart(tx, token, settlementFields(actual, delta, tokenDelta, reason)); err != nil {
				return err
			}
		}
		if extra.Quota != 0 {
			if err := updateOpenSubscriptionPart(tx, extra, map[string]any{"status": quotaDebitSettled, "restore_reason": reason}); err != nil {
				return err
			}
		}
		return updateOpenSubscriptionPart(tx, base, settlementFields(actual, delta, applied, reason))
	})
	return applied, err
}

func updateOpenSubscriptionPart(tx *gorm.DB, part QuotaRefundReceipt, fields map[string]any) error {
	result := quotaRefundSelection(tx, part).Where("version = ? AND status = ?", part.Version, quotaDebitOpen).Updates(fields)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQuotaOperationDecided
	}
	return nil
}

func settlementFields(actual, requestedDelta, appliedDelta int, reason string) map[string]any {
	return map[string]any{"status": quotaDebitSettled, "settlement_quota": actual, "settlement_delta": appliedDelta,
		"uncollected_quota": max(requestedDelta-appliedDelta, 0), "restore_reason": reason}
}

func (op SQLSubscriptionDebit) DecideRefund() error {
	return quotaSQLTransaction(DB, false, func(tx *gorm.DB) error {
		ledger := op.subscription()
		base, err := ledger.lockBase(tx)
		if err != nil {
			return err
		}
		if base.SubscriptionUserID != op.UserID || base.SubscriptionTokenID != op.TokenID || base.Group != op.Group || base.LogRequestID != op.LogRequestID {
			return errors.New("subscription refund conflicts with stored identity")
		}
		switch base.Status {
		case RefundRequested, RefundCompleted, RefundFailed:
			return nil
		case quotaDebitOpen:
			if base.Quota <= 0 {
				return ErrQuotaOperationDecided
			}
		default:
			return ErrQuotaOperationDecided
		}
		extra, err := ledger.extra(tx, base)
		if err != nil {
			return err
		}
		token, err := readSubscriptionTokenPart(tx, ledger, base, extra)
		if err != nil {
			return err
		}
		fields := map[string]any{"status": RefundRequested, "requested_at": common.GetTimestamp()}
		for _, part := range []QuotaRefundReceipt{base, extra, token} {
			if part.Quota > 0 {
				if err := updateOpenSubscriptionPart(tx, part, fields); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (op SQLSubscriptionDebit) ApplyRefund(ctx context.Context) (string, error) {
	if op.UserID <= 0 {
		return RefundRequested, errors.New("invalid subscription refund owner")
	}
	return op.applyRefund(ctx, true)
}

func applySubscriptionTokenRefund(ctx context.Context, id string) (string, error) {
	return (SQLSubscriptionDebit{ID: id}).applyRefund(ctx, false)
}

func (op SQLSubscriptionDebit) applyRefund(ctx context.Context, checkIdentity bool) (string, error) {
	ledger := op.subscription()
	now := GetDBTimestamp()
	outcome := RefundCompleted
	err := quotaSQLTransaction(DB.WithContext(ctx), false, func(tx *gorm.DB) error {
		base, err := ledger.lockBase(tx)
		if err != nil {
			return err
		}
		if checkIdentity && (base.SubscriptionUserID != op.UserID || base.SubscriptionTokenID != op.TokenID || base.Group != op.Group || base.LogRequestID != op.LogRequestID) {
			return errors.New("subscription refund conflicts with stored identity")
		}
		if base.Status == RefundCompleted || base.Status == RefundFailed {
			var failed int64
			err := ledger.rows(tx).Where("status = ?", RefundFailed).Count(&failed).Error
			if failed > 0 {
				outcome = RefundFailed
			}
			return err
		}
		if base.Status != RefundRequested || base.Quota <= 0 || base.Quota > common.MaxQuota {
			return ErrQuotaOperationDecided
		}
		extra, err := ledger.extra(tx, base)
		if err != nil {
			return err
		}
		token, err := readSubscriptionTokenPart(tx, ledger, base, extra)
		if err != nil {
			return err
		}
		restored, reason, status, err := restoreCommittedSubscription(tx, base, extra, now)
		if err != nil {
			return err
		}
		outcome = status
		if err := finishSubscriptionRefundParts(tx, base, extra, restored, reason, status); err != nil {
			return err
		}
		if token.SubjectID == 0 {
			return nil
		}
		err = changeSubscriptionTokenQuota(tx, base.SubscriptionUserID, token.SubjectID, -token.Quota, false)
		if errors.Is(err, ErrQuotaTokenInsufficient) {
			outcome = RefundFailed
			return finishSubscriptionRefundParts(tx, token, QuotaRefundReceipt{}, 0, "token_not_creditable", RefundFailed)
		}
		if err != nil {
			return err
		}
		return finishSubscriptionRefundParts(tx, token, QuotaRefundReceipt{}, token.Quota, "restored", RefundCompleted)
	})
	if err != nil {
		return RefundRequested, err
	}
	return outcome, nil
}
