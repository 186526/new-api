package model

import (
	"encoding/json"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	RefundRequested = "requested"
	RefundCompleted = "completed"
	RefundFailed    = "failed"
)

// QuotaRefundResult classifies a single compensation, not the request as a whole.
func QuotaRefundResult(err error, deferred bool) string {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound), errors.Is(err, ErrWalletQuotaLimitExceeded):
		return RefundFailed
	case err != nil, deferred:
		return RefundRequested
	default:
		return RefundCompleted
	}
}

func RefundWalletQuota(userID, quota int) (string, error) {
	err := IncreaseUserQuota(userID, quota, false)
	return QuotaRefundResult(err, RegionEnabled() || common.RedisEnabled || common.BatchUpdateEnabled), err
}

func RefundTokenQuota(tokenID int, key string, quota int) (string, error) {
	err := IncreaseTokenQuota(tokenID, key, quota)
	return QuotaRefundResult(err, common.RedisEnabled || common.BatchUpdateEnabled), err
}

func CreateToolRefundLog(entry *Log) error {
	return createLog(entry)
}

func UpdateToolRefundLog(entry *Log, other *LogOther) error {
	// ClickHouse usage logs do not have stable row IDs or synchronous row updates.
	if entry.Id == 0 || common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return nil
	}
	return LOG_DB.Model(&Log{}).Where("id = ? AND request_id = ?", entry.Id, entry.RequestId).
		Update("other", other.JSONString()).Error
}

func ToolRefundLogAvailable() bool { return LOG_DB != nil }

func OverlayToolRefundOutcomes(logs []*Log) error {
	ids := make([]string, 0, len(logs))
	for _, entry := range logs {
		if entry.RequestId != "" && entry.Other != "" {
			ids = append(ids, entry.RequestId)
		}
	}
	outcomes, err := QuotaRefundOutcomes(ids)
	if err != nil {
		return err
	}
	for _, entry := range logs {
		if status, ok := outcomes[entry.RequestId]; ok {
			entry.Other = overlayToolRefundOutcome(entry.Other, status)
		}
	}
	return nil
}

func overlayToolRefundOutcome(other, status string) string {
	var fields map[string]json.RawMessage
	if err := common.Unmarshal([]byte(other), &fields); err != nil {
		return other
	}
	updated := replaceToolRefundStatus(fields, status)
	var admin map[string]json.RawMessage
	if common.Unmarshal(fields[logOtherAdminInfoKey], &admin) == nil && replaceToolRefundStatus(admin, status) {
		fields[logOtherAdminInfoKey], _ = common.Marshal(admin)
		updated = true
	}
	if !updated {
		return other
	}
	result, err := common.Marshal(fields)
	if err != nil {
		return other
	}
	return string(result)
}

func replaceToolRefundStatus(fields map[string]json.RawMessage, status string) bool {
	var tool map[string]json.RawMessage
	if err := common.Unmarshal(fields["tool_execution"], &tool); err != nil {
		return false
	}
	var previous string
	if common.Unmarshal(tool["refund"], &previous) != nil || previous != RefundRequested {
		return false
	}
	tool["refund"], _ = common.Marshal(status)
	fields["tool_execution"], _ = common.Marshal(tool)
	return true
}
