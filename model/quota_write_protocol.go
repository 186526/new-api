package model

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	QuotaWriteLegacy          = "legacy"
	QuotaWriteDraining        = "draining"
	QuotaWriteSQL             = "sql"
	quotaWriteProtocolID      = 1
	quotaWriteProtocolVersion = 1
)

var ErrQuotaWriteProtocol = errors.New("quota write protocol does not permit this transaction")

type QuotaWriteProtocol struct {
	ID        int    `gorm:"primaryKey;autoIncrement:false"`
	Version   int    `gorm:"not null"`
	Mode      string `gorm:"size:16;not null"`
	DrainFrom string `gorm:"size:16;not null;default:''"`
}

func quotaSQLTransaction(db *gorm.DB, newOperation bool, write func(*gorm.DB) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := checkQuotaSQLWrite(tx, newOperation); err != nil {
			return err
		}
		return write(tx)
	})
}

func checkQuotaSQLWrite(tx *gorm.DB, newOperation bool) error {
	query := tx.Where("id = ?", quotaWriteProtocolID)
	switch tx.Dialector.Name() {
	case "postgres":
		// Writers can proceed together, but a state change must wait for all of them.
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	case "sqlite":
		// Acquire SQLite's write lock before reading the state or changing balances.
		if err := tx.Model(&QuotaWriteProtocol{}).Where("id = ?", quotaWriteProtocolID).
			UpdateColumn("mode", gorm.Expr("mode")).Error; err != nil {
			return fmt.Errorf("%w: %w", ErrQuotaWriteProtocol, err)
		}
	default:
		return ErrQuotaWriteProtocol
	}
	var protocol QuotaWriteProtocol
	if err := query.Take(&protocol).Error; err != nil {
		return fmt.Errorf("%w: %w", ErrQuotaWriteProtocol, err)
	}
	if protocol.Version != quotaWriteProtocolVersion || (protocol.Mode != QuotaWriteSQL && (protocol.Mode != QuotaWriteDraining || protocol.DrainFrom != QuotaWriteSQL || newOperation)) {
		return ErrQuotaWriteProtocol
	}
	return nil
}
