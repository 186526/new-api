package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type QuotaBalancePublication struct {
	Kind      string `gorm:"primaryKey;size:16"`
	SubjectID int    `gorm:"primaryKey;autoIncrement:false"`
	Version   int64  `gorm:"not null"`
	Balance   int64  `gorm:"not null"`
	Used      int64  `gorm:"not null"`
	Pending   bool   `gorm:"not null;index:idx_quota_publication_pending,priority:1"`
	RetryAt   int64  `gorm:"not null;index:idx_quota_publication_pending,priority:2"`
}

func recordQuotaBalance(tx *gorm.DB, row QuotaBalancePublication) error {
	row.Version, row.Pending = 1, true
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "kind"}, {Name: "subject_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"version": gorm.Expr("quota_balance_publications.version + 1"),
			"balance": row.Balance, "used": row.Used, "pending": true, "retry_at": 0,
		}),
	}).Create(&row).Error
}

func recordWalletBalance(tx *gorm.DB, id int) error {
	var user User
	if err := tx.Select("quota").Where("id = ?", id).Take(&user).Error; err != nil {
		return err
	}
	return recordQuotaBalance(tx, QuotaBalancePublication{Kind: RefundPartWallet, SubjectID: id, Balance: int64(user.Quota)})
}

func recordTokenBalance(tx *gorm.DB, id int) error {
	var token Token
	if err := tx.Select("remain_quota", "used_quota").Where("id = ?", id).Take(&token).Error; err != nil {
		return err
	}
	return recordQuotaBalance(tx, QuotaBalancePublication{Kind: RefundPartToken, SubjectID: id, Balance: int64(token.RemainQuota), Used: int64(token.UsedQuota)})
}

var publishQuotaBalanceScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current and string.sub(current, 1, 19) > ARGV[1] then
    return 0
end
redis.call('SET', KEYS[1], ARGV[2], 'EX', ARGV[3])
return 1
`)

type quotaBalanceSnapshot struct {
	Kind      string
	SubjectID int
	Version   string
	Balance   int64
	Used      int64
	ExpiresAt int64
}

// PublishPendingQuotaBalances sends committed projections only. The SQL row is
// retained after acknowledgment so a later update cannot reuse its version.
func PublishPendingQuotaBalances(ctx context.Context) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	var pending []QuotaBalancePublication
	if err := DB.WithContext(ctx).Where("pending = ? AND retry_at <= ?", true, common.GetTimestamp()).
		Order("retry_at, kind, subject_id").Limit(32).Find(&pending).Error; err != nil {
		return err
	}
	var firstError error
	for _, row := range pending {
		publishCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := publishQuotaBalance(publishCtx, row)
		cancel()
		if err != nil {
			retryErr := DB.WithContext(ctx).Model(&QuotaBalancePublication{}).
				Where("kind = ? AND subject_id = ? AND version = ? AND pending = ?", row.Kind, row.SubjectID, row.Version, true).
				UpdateColumn("retry_at", common.GetTimestamp()+15).Error
			if firstError == nil {
				firstError = errors.Join(err, retryErr)
			}
		}
	}
	return firstError
}

func publishQuotaBalance(ctx context.Context, candidate QuotaBalancePublication) error {
	return quotaSQLTransaction(DB.WithContext(ctx), false, func(tx *gorm.DB) error {
		var row QuotaBalancePublication
		query := tx.Where("kind = ? AND subject_id = ?", candidate.Kind, candidate.SubjectID)
		if tx.Dialector.Name() == "postgres" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.Take(&row).Error; err != nil || !row.Pending {
			return err
		}
		if err := writeQuotaBalanceCache(ctx, row); err != nil {
			return err
		}
		return tx.Model(&row).UpdateColumn("pending", false).Error
	})
}

func writeQuotaBalanceCache(ctx context.Context, row QuotaBalancePublication) error {
	const ttl = 60
	version := fmt.Sprintf("%019d", row.Version)
	snapshot := quotaBalanceSnapshot{
		Kind: row.Kind, SubjectID: row.SubjectID, Version: version,
		Balance: row.Balance, Used: row.Used, ExpiresAt: common.GetTimestamp() + ttl,
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("quota:sql:v1:%s:%d", row.Kind, row.SubjectID)
	value := version + ":" + common.GenerateHMAC(key+":"+string(payload)) + ":" + string(payload)
	result, err := publishQuotaBalanceScript.Run(ctx, common.RDB, []string{key}, version, value, ttl).Int()
	if err != nil {
		return err
	}
	if result != 1 {
		return errors.New("quota cache refused publication version")
	}
	return nil
}
