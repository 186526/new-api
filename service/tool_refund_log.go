package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// RefundRequestBilling also records zero-usage tools outcomes without a precharge.
func RefundRequestBilling(c *gin.Context, info *relaycommon.RelayInfo) {
	if info.ToolExecution == nil && c.Request != nil {
		if contract := webSearchGroupPolicyContract(c.Request.URL.Path); contract != "" {
			info.ToolExecution = &relaycommon.ToolExecutionMeta{Contract: contract, Outcome: "unavailable"}
		}
	}
	if info.ToolExecution != nil && info.ToolExecution.Refund == "" {
		if info.Billing == nil {
			info.ToolExecution.Refund = "not_needed"
			toolRefundLog(c, info)
		} else if _, ok := info.Billing.(*BillingSession); ok {
			if !info.Billing.NeedsRefund() {
				info.ToolExecution.Refund = "not_needed"
				toolRefundLog(c, info)
			}
		} else if info.Billing.NeedsRefund() {
			info.ToolExecution.Refund = model.RefundRequested
			toolRefundLog(c, info)
		}
	}
	if info.Billing != nil {
		info.Billing.Refund(c)
	}
}

func toolRefundLog(c *gin.Context, info *relaycommon.RelayInfo) func(string) {
	tool := info.RequestObservation().Tool
	if tool == nil || !model.ToolRefundLogAvailable() {
		return func(string) {}
	}
	entry := &model.Log{
		UserId: info.UserId, TokenId: info.TokenId, Group: info.UsingGroup,
		ModelName: info.OriginModelName, Username: c.GetString("username"),
		ChannelId: common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		RequestId: c.GetString(common.RequestIdKey), CreatedAt: common.GetTimestamp(),
		Type: model.LogTypeError,
	}
	entry.Other = toolRefundOther(*tool).JSONString()
	if err := model.CreateToolRefundLog(entry); err != nil {
		common.SysError("failed to persist tools quota observation: " + err.Error())
		return func(string) {}
	}
	return func(status string) {
		if status == tool.Refund {
			return
		}
		updated := *tool
		updated.Refund = status
		if err := model.UpdateToolRefundLog(entry, toolRefundOther(updated)); err != nil {
			common.SysError("failed to update tools quota observation: " + err.Error())
		}
	}
}

func toolRefundOther(tool relaycommon.ToolExecutionMeta) *model.LogOther {
	other := &model.LogOther{}
	other.SetAdmin("tool_execution", tool)
	tool.Provider = ""
	other.SetPublic("tool_execution", tool)
	return other
}

func mergeRefundResult(left, right string) string {
	if left == model.RefundFailed || right == model.RefundFailed {
		return model.RefundFailed
	}
	if left == model.RefundCompleted && right == model.RefundCompleted {
		return model.RefundCompleted
	}
	return model.RefundRequested
}

func refundFunding(funding FundingSource, receipt model.QuotaRefundReceipt) (string, error) {
	if wallet, ok := funding.(*WalletFunding); ok {
		if wallet.consumed <= 0 {
			return model.RefundCompleted, nil
		}
		if receipt.RequestID != "" && !model.RegionEnabled() && !common.RedisEnabled && !common.BatchUpdateEnabled {
			return model.ApplySQLQuotaRefund(receipt)
		}
		_, err := model.RefundWalletQuota(wallet.userId, wallet.consumed)
		return model.QuotaRefundResult(err, true), err
	}
	err := funding.Refund()
	// Subscription refunds can clamp to zero, and old refund markers do not
	// establish that the full delta was applied. Treat them as unconfirmed.
	return model.QuotaRefundResult(err, true), err
}
