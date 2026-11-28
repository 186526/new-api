package model

import (
	"cmp"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PerfMetricDimension struct {
	Kind    string `json:"kind" gorm:"size:16;primaryKey;not null"`
	Name    string `json:"name" gorm:"size:32;primaryKey;not null"`
	Variant string `json:"variant" gorm:"size:32;primaryKey;not null"`
	Outcome string `json:"outcome" gorm:"size:24;primaryKey;not null"`
}

type PerfMetricDistributionEntry struct {
	PerfMetricDimension `gorm:"embedded"`
	Count               int64 `json:"count" gorm:"column:sample_count;not null"`
	CacheHits           int64 `json:"cache_hits,omitempty" gorm:"not null"`
}

type PerfMetricDistributionRow struct {
	BucketTs                    int64  `gorm:"primaryKey;autoIncrement:false;not null"`
	Group                       string `gorm:"column:group;size:64;primaryKey;not null"`
	PerfMetricDistributionEntry `gorm:"embedded"`
}

func (PerfMetricDistributionRow) TableName() string { return "perf_metric_distributions" }

type PerfMetricDistribution map[PerfMetricDimension]PerfMetricDistributionEntry

func (distribution PerfMetricDistribution) Add(entry PerfMetricDistributionEntry) {
	prior := distribution[entry.PerfMetricDimension]
	entry.Count += prior.Count
	entry.CacheHits += prior.CacheHits
	distribution[entry.PerfMetricDimension] = entry
}

func (distribution PerfMetricDistribution) Entries() []PerfMetricDistributionEntry {
	entries := make([]PerfMetricDistributionEntry, 0, len(distribution))
	for _, entry := range distribution {
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left, right PerfMetricDistributionEntry) int {
		return cmp.Or(cmp.Compare(left.Kind, right.Kind), cmp.Compare(left.Name, right.Name), cmp.Compare(left.Variant, right.Variant), cmp.Compare(left.Outcome, right.Outcome))
	})
	return entries
}

func upsertPerfMetricDistribution(tx *gorm.DB, metric *PerfMetric) error {
	entries := metric.Distribution.Entries()
	if len(entries) == 0 {
		return nil
	}
	rows := make([]PerfMetricDistributionRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, PerfMetricDistributionRow{BucketTs: metric.BucketTs, Group: metric.Group, PerfMetricDistributionEntry: entry})
	}
	increment := func(column string) clause.Expr {
		if tx.Dialector.Name() == "mysql" {
			return gorm.Expr("perf_metric_distributions." + column + " + VALUES(" + column + ")")
		}
		return gorm.Expr("perf_metric_distributions." + column + " + EXCLUDED." + column)
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "bucket_ts"}, {Name: "group"}, {Name: "kind"}, {Name: "name"}, {Name: "variant"}, {Name: "outcome"}},
		DoUpdates: clause.Assignments(map[string]any{
			"sample_count": increment("sample_count"),
			"cache_hits":   increment("cache_hits"),
		}),
	}).CreateInBatches(rows, 100).Error
}

func GetPerfMetricDistribution(startTs, endTs int64, groups []string) (PerfMetricDistribution, error) {
	distribution := make(PerfMetricDistribution)
	if groups != nil && len(groups) == 0 {
		return distribution, nil
	}
	query := DB.Model(&PerfMetricDistributionRow{}).
		Select("kind, name, variant, outcome, SUM(sample_count) AS sample_count, SUM(cache_hits) AS cache_hits").
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs)
	if groups != nil {
		query = query.Where(commonGroupCol+" IN ?", groups)
	}
	var rows []PerfMetricDistributionEntry
	if err := query.Group("kind, name, variant, outcome").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		distribution.Add(row)
	}
	return distribution, nil
}
