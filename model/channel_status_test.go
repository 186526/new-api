package model

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// TestBatchSetChannelTagDatabaseMatrix checks that BatchSetChannelTag rebuilds
// abilities from the tags its own transaction wrote. SQLite always runs; the
// MySQL and PostgreSQL cases run when their DSN environment variable is set.
func TestBatchSetChannelTagDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "channel-tags.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "channel_tag_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(2)
			previousDB, previousType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				initCol()
				assert.NoError(t, db.Migrator().DropTable(&Ability{}, &Channel{}, &SystemTask{}))
				assert.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}, &SystemTask{}))
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			oldTag, newTag := "old-tag", "new-tag"
			priority, weight := int64(7), uint(23)
			channels := []Channel{
				{Name: "enabled", Status: common.ChannelStatusEnabled},
				{Name: "disabled", Status: common.ChannelStatusAutoDisabled},
				{Name: "unselected", Status: common.ChannelStatusEnabled},
			}
			for i := range channels {
				channel := &channels[i]
				channel.Models, channel.Group = "model-a,model-b", "default,second"
				channel.Tag, channel.Priority, channel.Weight = &oldTag, &priority, &weight
				require.NoError(t, db.Create(channel).Error)
				require.NoError(t, channel.AddAbilities(db))
			}
			ids := []int{channels[0].Id, channels[1].Id}
			for _, tc := range []struct {
				name string
				tag  *string
			}{
				{name: "set", tag: &newTag},
				{name: "clear", tag: nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					require.NoError(t, BatchSetChannelTag(ids, tc.tag))
					for i, channel := range channels {
						wantTag := tc.tag
						if i == 2 {
							wantTag = &oldTag
						}
						var stored Channel
						require.NoError(t, db.First(&stored, channel.Id).Error)
						assert.Equal(t, wantTag, stored.Tag, channel.Name)
						assert.Equal(t, channel.Status, stored.Status, channel.Name)
						var abilities []Ability
						require.NoError(t, db.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
						require.Len(t, abilities, 4)
						for _, ability := range abilities {
							assert.Equal(t, wantTag, ability.Tag, channel.Name)
							assert.Equal(t, channel.Status == common.ChannelStatusEnabled, ability.Enabled, channel.Name)
							assert.Equal(t, channel.Priority, ability.Priority, channel.Name)
							assert.Equal(t, weight, ability.Weight, channel.Name)
						}
					}
				})
			}
		})
	}
}

func setupChannelStatusTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM abilities").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	memoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		common.MemoryCacheEnabled = memoryCacheEnabled
	})
}

func TestUpdateChannelStatusPersistsMultiKeyState(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:   "multi-key-status",
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         2,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, DB.Create(&channel).Error)

	changed := UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "provider rejected key")
	require.True(t, changed)

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusEnabled, stored.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, "provider rejected key", stored.ChannelInfo.MultiKeyDisabledReason[0])
	assert.NotZero(t, stored.ChannelInfo.MultiKeyDisabledTime[0])
	assert.Equal(t, 1, stored.ChannelInfo.MultiKeyPollingIndex)
}

func TestSaveStatusStateFromSingleKeySnapshotPreservesUnownedColumns(t *testing.T) {
	setupChannelStatusTest(t)

	channel := Channel{
		Name:        "single-key-status",
		Key:         "original-key",
		Status:      common.ChannelStatusEnabled,
		Models:      "original-model",
		Group:       "default",
		UsedQuota:   100,
		ChannelInfo: ChannelInfo{},
	}
	require.NoError(t, DB.Create(&channel).Error)

	stale, err := GetChannelById(channel.Id, true)
	require.NoError(t, err)

	concurrentChannelInfo := ChannelInfo{
		IsMultiKey:           true,
		MultiKeySize:         2,
		MultiKeyMode:         constant.MultiKeyModePolling,
		MultiKeyPollingIndex: 1,
	}
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
		"key":          "rotated-key",
		"used_quota":   gorm.Expr("used_quota + ?", 250),
		"models":       "concurrent-model",
		"channel_info": concurrentChannelInfo,
	}).Error)

	stale.Status = common.ChannelStatusManuallyDisabled
	stale.SetOtherInfo(map[string]any{
		"status_reason": "manual operation",
		"status_time":   int64(1234),
	})
	require.NoError(t, stale.saveStatusState())

	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, stored.Status)
	assert.Equal(t, "rotated-key", stored.Key)
	assert.Equal(t, int64(350), stored.UsedQuota)
	assert.Equal(t, "concurrent-model", stored.Models)
	assert.Equal(t, concurrentChannelInfo, stored.ChannelInfo)

	otherInfo := stored.GetOtherInfo()
	assert.Equal(t, "manual operation", otherInfo["status_reason"])
	assert.Equal(t, float64(1234), otherInfo["status_time"])
}

func TestAutoDisableKeepsManualDisable(t *testing.T) {
	for _, cacheEnabled := range []bool{false, true} {
		for _, tc := range []struct {
			name          string
			multiKey      bool
			initialStatus int
			wantChanged   bool
			wantStatus    int
		}{
			{name: "single key manual disable is kept", initialStatus: common.ChannelStatusManuallyDisabled, wantStatus: common.ChannelStatusManuallyDisabled},
			{name: "multi key manual disable is kept", multiKey: true, initialStatus: common.ChannelStatusManuallyDisabled, wantStatus: common.ChannelStatusManuallyDisabled},
			{name: "single key enabled is auto disabled", initialStatus: common.ChannelStatusEnabled, wantChanged: true, wantStatus: common.ChannelStatusAutoDisabled},
			{name: "multi key enabled is auto disabled", multiKey: true, initialStatus: common.ChannelStatusEnabled, wantChanged: true, wantStatus: common.ChannelStatusAutoDisabled},
		} {
			t.Run(fmt.Sprintf("cache=%t/%s", cacheEnabled, tc.name), func(t *testing.T) {
				setupChannelStatusTest(t)
				common.MemoryCacheEnabled = cacheEnabled

				channel := Channel{Name: t.Name(), Key: "key-a", Status: tc.initialStatus, Models: "test-model", Group: "default"}
				if tc.multiKey {
					// Key b already failed, so a failure on key a disables every key.
					channel.Key = "key-a\nkey-b"
					channel.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{1: common.ChannelStatusAutoDisabled}}
				}
				channel.SetOtherInfo(map[string]any{"status_reason": "manual operation", "status_time": int64(1234)})
				require.NoError(t, channel.Insert())
				InitChannelCache()

				changed := UpdateChannelStatus(channel.Id, "key-a", common.ChannelStatusAutoDisabled, "provider rejected key")
				assert.Equal(t, tc.wantChanged, changed)

				stored, err := GetChannelById(channel.Id, true)
				require.NoError(t, err)
				assert.Equal(t, tc.wantStatus, stored.Status)
				if !tc.wantChanged {
					assert.Equal(t, "manual operation", stored.GetOtherInfo()["status_reason"])
					assert.Equal(t, float64(1234), stored.GetOtherInfo()["status_time"])
					assert.Equal(t, channel.ChannelInfo.MultiKeyStatusList, stored.ChannelInfo.MultiKeyStatusList)
				}
				var abilities []Ability
				require.NoError(t, DB.Where("channel_id = ?", channel.Id).Find(&abilities).Error)
				require.NotEmpty(t, abilities)
				for _, ability := range abilities {
					assert.False(t, ability.Enabled)
				}
				if cacheEnabled {
					cached, err := CacheGetChannel(channel.Id)
					require.NoError(t, err)
					assert.Equal(t, tc.wantStatus, cached.Status)
					assert.Equal(t, stored.ChannelInfo.MultiKeyStatusList, cached.ChannelInfo.MultiKeyStatusList)
				}
			})
		}
	}
}
