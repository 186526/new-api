package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrQuotaReviewInvalid  = errors.New("invalid quota review input")
	ErrQuotaReviewPending  = errors.New("quota evidence is incomplete or has changed")
	ErrQuotaReviewRecorded = errors.New("quota evidence has already been reviewed")
)

type QuotaReviewIdentity struct {
	RequestID   string `json:"request_id" form:"request_id"`
	Region      string `json:"region" form:"region"`
	SubjectType string `json:"subject_type" form:"subject_type"`
	SubjectID   string `json:"subject_id" form:"subject_id"`
	Sequence    int64  `json:"sequence,string" form:"sequence"`
}

type QuotaReviewEvidence struct {
	Part             string `json:"part"`
	Status           string `json:"status"`
	SubjectID        int    `json:"subject_id"`
	Quota            int    `json:"quota"`
	Restored         int    `json:"restored"`
	RestoreReason    string `json:"restore_reason,omitempty"`
	Version          int    `json:"version"`
	SettlementQuota  int    `json:"settlement_quota"`
	SettlementDelta  int    `json:"settlement_delta"`
	UncollectedQuota int    `json:"uncollected_quota"`
}

type QuotaReconciliation struct {
	EvidenceKey  string `json:"-" gorm:"primaryKey;size:64"`
	EvidenceHash string `json:"-" gorm:"size:64;not null"`
	EvidenceJSON string `json:"-" gorm:"type:text;not null"`
	Reason       string `json:"reason" gorm:"type:text;not null"`
	OperatorID   int    `json:"operator_id" gorm:"not null"`
	RecordedAt   int64  `json:"recorded_at" gorm:"not null"`
}

type QuotaReview struct {
	Receipts     []QuotaReviewEvidence `json:"receipts,omitempty"`
	RegionEvent  *RegionQuotaEvent     `json:"region_event,omitempty"`
	Confirmable  bool                  `json:"confirmable"`
	EvidenceHash string                `json:"evidence_hash,omitempty"`
	Confirmation *QuotaReconciliation  `json:"confirmation,omitempty"`
	operationID  string
}

func (identity QuotaReviewIdentity) validate() error {
	if identity.RequestID != "" {
		if len(identity.RequestID) <= 64 && strings.IndexFunc(identity.RequestID, unicode.IsSpace) == -1 && identity.Region == "" && identity.SubjectType == "" && identity.SubjectID == "" && identity.Sequence == 0 {
			return nil
		}
		return ErrQuotaReviewInvalid
	}
	subjectID, err := strconv.Atoi(identity.SubjectID)
	if !validReportRegion(identity.Region) || identity.SubjectType != RegionSubjectUser || err != nil || subjectID <= 0 || strconv.Itoa(subjectID) != identity.SubjectID || identity.Sequence <= 0 {
		return ErrQuotaReviewInvalid
	}
	return nil
}

func GetQuotaReview(ctx context.Context, identity QuotaReviewIdentity) (QuotaReview, error) {
	if err := identity.validate(); err != nil {
		return QuotaReview{}, err
	}
	return readQuotaReview(DB.WithContext(ctx), identity, false)
}

func readQuotaReview(db *gorm.DB, identity QuotaReviewIdentity, locked bool) (QuotaReview, error) {
	if identity.RequestID == "" {
		var event RegionQuotaEvent
		err := db.Where("region = ? AND subject_type = ? AND subject_id = ? AND seq = ?", identity.Region, identity.SubjectType, identity.SubjectID, identity.Sequence).Take(&event).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return QuotaReview{}, nil
		}
		if err != nil {
			return QuotaReview{}, err
		}
		return QuotaReview{RegionEvent: &event}, nil
	}
	var operationIDs []string
	if err := db.Model(&QuotaRefundReceipt{}).Where("request_id = ? OR log_request_id = ?", identity.RequestID, identity.RequestID).
		Distinct("request_id").Limit(2).Pluck("request_id", &operationIDs).Error; err != nil {
		return QuotaReview{}, err
	}
	if len(operationIDs) != 1 {
		return QuotaReview{}, nil
	}
	var receipts []QuotaRefundReceipt
	query := db.Where("request_id = ?", operationIDs[0]).Order("part").Limit(16)
	if locked {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := query.Find(&receipts).Error; err != nil {
		return QuotaReview{}, err
	}
	return quotaReviewFromReceipts(db, receipts)
}

func quotaReviewFromReceipts(db *gorm.DB, receipts []QuotaRefundReceipt) (QuotaReview, error) {
	if len(receipts) == 0 || len(receipts) == 16 || receipts[0].RequestID != receipts[len(receipts)-1].RequestID {
		return QuotaReview{}, nil
	}
	review := QuotaReview{Confirmable: true, operationID: receipts[0].RequestID}
	for _, receipt := range receipts {
		review.Receipts = append(review.Receipts, QuotaReviewEvidence{Part: receipt.Part, Status: receipt.Status, SubjectID: receipt.SubjectID, Quota: receipt.Quota, Restored: receipt.Restored, RestoreReason: receipt.RestoreReason, Version: receipt.Version, SettlementQuota: receipt.SettlementQuota, SettlementDelta: receipt.SettlementDelta, UncollectedQuota: receipt.UncollectedQuota})
		if receipt.Status != RefundCompleted && receipt.Status != RefundFailed && receipt.Status != quotaDebitSettled {
			review.Confirmable = false
		}
	}
	var confirmation QuotaReconciliation
	err := db.Take(&confirmation, "evidence_key = ?", quotaReviewHash([]byte(receipts[0].RequestID))).Error
	if err == nil {
		review.Confirmation = &confirmation
		review.Confirmable = false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return QuotaReview{}, err
	}
	if review.Confirmable {
		snapshot, err := json.Marshal(review.Receipts)
		if err != nil {
			return QuotaReview{}, err
		}
		review.EvidenceHash = quotaReviewHash(append([]byte(receipts[0].RequestID+"\x00"), snapshot...))
	}
	return review, nil
}

func quotaReviewHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

type QuotaReviewSubmission struct {
	QuotaReviewIdentity
	EvidenceHash string `json:"evidence_hash"`
	Reason       string `json:"reason"`
}

func ConfirmQuotaReview(ctx context.Context, submission QuotaReviewSubmission, operatorID int) error {
	reason := strings.TrimSpace(submission.Reason)
	if err := submission.validate(); err != nil || operatorID <= 0 || reason == "" || len(reason) > 2000 || len(submission.EvidenceHash) != 64 {
		return ErrQuotaReviewInvalid
	}
	if submission.RequestID == "" {
		return ErrQuotaReviewPending
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return recordQuotaReview(tx, submission, operatorID, reason)
	})
}

func recordQuotaReview(tx *gorm.DB, submission QuotaReviewSubmission, operatorID int, reason string) error {
	review, err := readQuotaReview(tx, submission.QuotaReviewIdentity, true)
	if err != nil {
		return err
	}
	if review.Confirmation != nil {
		return ErrQuotaReviewRecorded
	}
	if !review.Confirmable || review.EvidenceHash != submission.EvidenceHash {
		return ErrQuotaReviewPending
	}
	snapshot, err := json.Marshal(review.Receipts)
	if err != nil {
		return err
	}
	record := QuotaReconciliation{EvidenceKey: quotaReviewHash([]byte(review.operationID)), EvidenceHash: review.EvidenceHash, EvidenceJSON: string(snapshot), Reason: reason, OperatorID: operatorID, RecordedAt: common.GetTimestamp()}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQuotaReviewRecorded
	}
	return nil
}
