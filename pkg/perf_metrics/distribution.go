package perfmetrics

import (
	"strconv"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func observationDistribution(observed relaycommon.RequestObservation) []model.PerfMetricDistributionEntry {
	var entries []model.PerfMetricDistributionEntry
	if tool := observed.Tool; tool != nil {
		entry := model.PerfMetricDistributionEntry{
			PerfMetricDimension: model.PerfMetricDimension{Kind: "tool", Name: tool.Contract, Variant: observationLabel(tool.Provider), Outcome: observationLabel(tool.Outcome)},
			Count:               1,
		}
		if tool.CacheHit {
			entry.CacheHits = 1
		}
		entries = append(entries, entry)
	}
	if observed.AutoReason != "" {
		tier, effort := "unknown", observed.AutoPolicyEffort
		if observed.AutoTier != nil && effort != "" && *observed.AutoTier >= 0 && *observed.AutoTier <= 6 {
			tier = strconv.Itoa(*observed.AutoTier)
		}
		if observed.AutoReason == "applied" {
			effort = observed.AutoEffort
		}
		entries = append(entries, model.PerfMetricDistributionEntry{
			PerfMetricDimension: model.PerfMetricDimension{Kind: "reasoning", Name: tier, Variant: observationLabel(effort), Outcome: observed.AutoReason},
			Count:               1,
		})
	}
	return entries
}

func observationLabel(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func visitObservationBuckets(startTs, endTs int64, groups []string, visit func(*atomicBucket)) {
	allowed := allowedGroupSet(groups)
	hotBuckets.Range(func(key, value any) bool {
		bucket := key.(bucketKey)
		if bucket.bucketTs < startTs || bucket.bucketTs > endTs {
			return true
		}
		if _, ok := allowed[bucket.group]; allowed != nil && !ok {
			return true
		}
		visit(value.(*atomicBucket))
		return true
	})
}

func QueryObservationDistribution(startTs, endTs int64, groups []string) ([]model.PerfMetricDistributionEntry, error) {
	observationFlushMu.RLock()
	defer observationFlushMu.RUnlock()
	distribution, err := model.GetPerfMetricDistribution(startTs, endTs, groups)
	if err != nil {
		return nil, err
	}
	visitObservationBuckets(startTs, endTs, groups, func(bucket *atomicBucket) {
		bucket.observationMu.Lock()
		defer bucket.observationMu.Unlock()
		for _, entry := range bucket.distribution {
			distribution.Add(entry)
		}
	})
	return distribution.Entries(), nil
}
