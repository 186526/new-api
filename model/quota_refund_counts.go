package model

type QuotaRefundCounts struct {
	Requested int64
	Completed int64
	Failed    int64
}

func GetQuotaRefundCounts(startTs, endTs int64, groups []string) (QuotaRefundCounts, error) {
	var counts QuotaRefundCounts
	if groups != nil && len(groups) == 0 {
		return counts, nil
	}
	requests := DB.Model(&QuotaRefundReceipt{}).
		Select("request_id, MAX(CASE WHEN status = ? THEN 1 ELSE 0 END) AS failed, MIN(CASE WHEN status = ? THEN 1 ELSE 0 END) AS completed", RefundFailed, RefundCompleted).
		Where("requested_at BETWEEN ? AND ? AND status IN ?", startTs, endTs, []string{RefundRequested, RefundCompleted, RefundFailed})
	if groups != nil {
		requests = requests.Where(commonGroupCol+" IN ?", groups)
	}
	err := DB.Table("(?) AS refund_requests", requests.Group("request_id")).
		Select("COUNT(*) AS requested, COALESCE(SUM(completed), 0) AS completed, COALESCE(SUM(failed), 0) AS failed").Scan(&counts).Error
	return counts, err
}
