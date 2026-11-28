package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPerfMetricDistributionAccumulatesWithObservationTotals(t *testing.T) {
	originalDB, originalGroupCol := DB, commonGroupCol
	t.Cleanup(func() { DB, commonGroupCol = originalDB, originalGroupCol })
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	connection, err := database.DB()
	require.NoError(t, err)
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	DB, commonGroupCol = database, "`group`"
	require.NoError(t, DB.AutoMigrate(&PerfMetric{}, &PerfMetricDistributionRow{}))

	entry := PerfMetricDistributionEntry{PerfMetricDimension: PerfMetricDimension{Kind: "tool", Name: "moonshot/search", Variant: "codex", Outcome: "success"}, Count: 2, CacheHits: 1}
	for _, modelName := range []string{"model-first", "model-second"} {
		require.NoError(t, UpsertPerfMetric(&PerfMetric{
			ModelName: modelName, Group: "allowed", BucketTs: 100,
			Observation:  PerfMetricObservation{ToolRequests: 2, ToolCacheHits: 1},
			Distribution: PerfMetricDistribution{entry.PerfMetricDimension: entry},
		}))
	}
	distribution, err := GetPerfMetricDistribution(0, 100, []string{"allowed"})
	require.NoError(t, err)
	require.Equal(t, []PerfMetricDistributionEntry{{PerfMetricDimension: entry.PerfMetricDimension, Count: 4, CacheHits: 2}}, distribution.Entries())

	require.NoError(t, DB.Migrator().DropTable(&PerfMetricDistributionRow{}))
	require.Error(t, UpsertPerfMetric(&PerfMetric{
		ModelName: "model-first", Group: "allowed", BucketTs: 100,
		Observation:  PerfMetricObservation{ToolRequests: 2, ToolCacheHits: 1},
		Distribution: PerfMetricDistribution{entry.PerfMetricDimension: entry},
	}))
	observation, err := GetPerfMetricObservations(0, 100, []string{"allowed"})
	require.NoError(t, err)
	require.Equal(t, int64(4), observation.ToolRequests)
	require.Equal(t, int64(2), observation.ToolCacheHits)
}
