package service

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func (s *BillingSession) reserveSQLWallet(wallet *WalletFunding, target int, requireWalletBalance bool) *types.NewAPIError {
	op := s.sqlDebit
	if op == nil {
		info := s.relayInfo
		op = &model.SQLQuotaDebit{ID: common.GetUUID(), LogRequestID: info.RequestId, Group: info.UsingGroup, UserID: wallet.userId}
		if !info.IsPlayground {
			if info.TokenId <= 0 {
				return types.NewErrorWithStatusCode(model.ErrQuotaTokenInsufficient, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
			op.TokenID = info.TokenId
		}
	}
	if err := op.Reserve(target, requireWalletBalance); err != nil {
		switch {
		case errors.Is(err, model.ErrQuotaTokenInsufficient):
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		case errors.Is(err, model.ErrQuotaWalletInsufficient):
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		default:
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
	}
	s.sqlDebit = op
	wallet.consumed = target
	if op.TokenID != 0 {
		s.tokenConsumed = target
	}
	return nil
}

func (s *BillingSession) reserveSQLSubscription(funding *SubscriptionFunding, target int) *types.NewAPIError {
	op := s.sqlSubscription
	if op == nil {
		info := s.relayInfo
		op = &model.SQLSubscriptionDebit{ID: common.GetUUID(), LogRequestID: info.RequestId, Group: info.UsingGroup, UserID: funding.userId}
		if !info.IsPlayground {
			if info.TokenId <= 0 {
				return types.NewErrorWithStatusCode(model.ErrQuotaTokenInsufficient, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
			op.TokenID = info.TokenId
		}
	}
	reserved, err := op.Reserve(int(funding.amount), target)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrQuotaTokenInsufficient):
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		case errors.Is(err, model.ErrQuotaSubscriptionInsufficient):
			return types.NewErrorWithStatusCode(errors.New(i18n.Translate(s.lang, i18n.MsgQuotaSubscriptionInsufficient, map[string]any{"Error": err.Error()})), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		default:
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
	}
	s.sqlSubscription = op
	funding.subscriptionId = reserved.UserSubscriptionId
	funding.preConsumed = reserved.PreConsumed
	funding.AmountTotal = reserved.AmountTotal
	funding.AmountUsedAfter = reserved.AmountUsedAfter - int64(target) + reserved.PreConsumed
	funding.PlanId, funding.PlanTitle = reserved.PlanId, reserved.PlanTitle
	if op.TokenID != 0 {
		s.tokenConsumed = target
	}
	return nil
}

func applySQLRefund(apply func(context.Context) (string, error), persistOutcome func(string)) {
	model.GoDetachedQuotaWork(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status, err := apply(ctx)
		if err != nil {
			common.SysError("SQL quota refund deferred to recovery: " + err.Error())
			return
		}
		persistOutcome(status)
	})
}

func RunQuotaRefundRecovery(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		cycle, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := model.RecoverQuotaRefunds(cycle)
		cancel()
		if err != nil && ctx.Err() == nil {
			common.SysError("quota refund recovery: " + err.Error())
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (s *BillingSession) registerRefunds(c *gin.Context) (map[string]model.QuotaRefundReceipt, error) {
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = s.relayInfo.RequestId
	}
	var parts []model.QuotaRefundReceipt
	add := func(part string, subject, quota int) {
		if quota > 0 {
			parts = append(parts, model.QuotaRefundReceipt{
				RequestID: requestID, Part: part, SubjectID: subject, Quota: quota,
				Group: s.relayInfo.UsingGroup, RequestedAt: common.GetTimestamp(), Status: model.RefundRequested,
			})
		}
	}
	switch funding := s.funding.(type) {
	case *WalletFunding:
		add(model.RefundPartWallet, funding.userId, funding.consumed)
	case *SubscriptionFunding:
		add(model.RefundPartSubscription, funding.subscriptionId, int(funding.preConsumed))
		add(model.RefundPartSubscriptionExtra, funding.subscriptionId, s.extraReserved)
	}
	if !s.relayInfo.IsPlayground {
		add(model.RefundPartToken, s.relayInfo.TokenId, s.tokenConsumed)
	}
	if len(parts) == 0 {
		return nil, nil
	}
	if requestID == "" {
		requestID = common.GetUUID()
		for i := range parts {
			parts[i].RequestID = requestID
		}
	}
	if err := model.RegisterQuotaRefunds(parts); err != nil {
		return nil, err
	}
	result := make(map[string]model.QuotaRefundReceipt, len(parts))
	for _, part := range parts {
		result[part.Part] = part
	}
	return result, nil
}

func refundToken(receipt model.QuotaRefundReceipt, tokenID int, key string, quota int) (string, error) {
	if receipt.RequestID != "" && !common.RedisEnabled && !common.BatchUpdateEnabled {
		return model.ApplySQLQuotaRefund(receipt)
	}
	_, err := model.RefundTokenQuota(tokenID, key, quota)
	return model.QuotaRefundResult(err, true), err
}

func observeRefundFailure(receipt model.QuotaRefundReceipt, status string) {
	if receipt.RequestID != "" && status == model.RefundFailed {
		if err := model.RecordQuotaRefundFailure(receipt); err != nil {
			common.SysError("failed to record quota refund failure: " + err.Error())
		}
	}
}
