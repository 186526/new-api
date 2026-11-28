package model

import (
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMainQuotaSchemaMigrationIsAdditive(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota-migration.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	previousDB, previousType := DB, common.MainDatabaseType()
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
	})

	require.NoError(t, db.AutoMigrate(&RegionQuotaEvent{}))
	legacyEvent := RegionQuotaEvent{Region: "legacy-region", SubjectType: "user", SubjectID: "42", Seq: 7, Delta: 11, CreatedAt: 100}
	require.NoError(t, db.Create(&legacyEvent).Error)

	for pass := 1; pass <= 2; pass++ {
		require.NoError(t, migrateDB(), "migration pass %d", pass)
		for _, table := range []string{
			"quota_write_protocols",
			"quota_balance_publications",
			"non_request_quota_receipts",
			"region_quota_leases_v2",
			"region_quota_debits_v2",
			"region_quota_report_receipts_v2",
		} {
			require.True(t, db.Migrator().HasTable(table), "migration pass %d: missing %s", pass, table)
			var rows int64
			require.NoError(t, db.Table(table).Count(&rows).Error)
			require.Zero(t, rows, "migration pass %d: unexpected rows in %s", pass, table)
		}
		var events []RegionQuotaEvent
		require.NoError(t, db.Find(&events).Error)
		require.Equal(t, []RegionQuotaEvent{legacyEvent}, events)
	}
}
