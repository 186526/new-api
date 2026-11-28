package service

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func SettleSubmittedTask(ctx *gin.Context, info *relaycommon.RelayInfo, quota int, transferred bool) error {
	if transferred {
		return nil
	}
	return SettleBilling(ctx, info, quota)
}

func InsertTaskWithBilling(ctx context.Context, info *relaycommon.RelayInfo, task *model.Task, omits ...string) (bool, error) {
	if session, ok := info.Billing.(*BillingSession); ok {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.sqlDebit != nil {
			if session.settled || session.refunded {
				return false, model.ErrQuotaOperationDecided
			}
			if err := task.InsertWithWalletDebit(ctx, *session.sqlDebit, omits...); err != nil {
				return false, err
			}
			session.settled = true
			return true, nil
		}
		if session.sqlSubscription != nil {
			return false, errors.New("subscription task ledger is not available")
		}
	}
	if model.SQLQuotaLedgerWriterEnabled() && task.Quota != 0 {
		return false, errors.New("task lacks a supported debit receipt")
	}
	return false, task.InsertWithContext(ctx, omits...)
}

func persistTerminalTask(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, previous model.TaskStatus, result *relaycommon.TaskInfo) (bool, error) {
	if task.PrivateData.WalletDebit == nil {
		return task.UpdateWithStatus(previous)
	}
	actual := task.Quota
	if task.Status == model.TaskStatusFailure {
		actual = 0
	} else if result != nil {
		decision, _ := taskCompletionQuota(ctx, adaptor, task, result)
		if decision != nil {
			actual = decision.amount
		}
	}
	return task.FinishWithWalletDebit(ctx, previous, actual)
}

func applyTaskWalletRefund(ctx context.Context, task *model.Task) {
	if _, err := task.PrivateData.WalletDebit.ApplyRefund(ctx); err != nil {
		logger.LogWarn(ctx, common.LogText("task %s refund deferred to recovery: %v", task.TaskID, err))
	}
}
