package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

func TestMergeRefundResult(t *testing.T) {
	cases := []struct {
		name, funding, token, want string
	}{
		{"all confirmed", model.RefundCompleted, model.RefundCompleted, model.RefundCompleted},
		{"pending mirror", model.RefundCompleted, model.RefundRequested, model.RefundRequested},
		{"partial failure", model.RefundCompleted, model.RefundFailed, model.RefundFailed},
		{"known failure with unknown component", model.RefundRequested, model.RefundFailed, model.RefundFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeRefundResult(tc.funding, tc.token); got != tc.want {
				t.Fatalf("refund result = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQuotaRefundResultRequiresProof(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		deferred bool
		want     string
	}{
		{"direct write", nil, false, model.RefundCompleted},
		{"queued write", nil, true, model.RefundRequested},
		{"unknown commit", context.DeadlineExceeded, false, model.RefundRequested},
		{"missing quota row", gorm.ErrRecordNotFound, false, model.RefundFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := model.QuotaRefundResult(tc.err, tc.deferred); got != tc.want {
				t.Fatalf("refund result = %q, want %q", got, tc.want)
			}
		})
	}
}
