package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Region lease gauges are refreshed from Redis by the region maintenance loop;
// counters move with the spend itself.
var (
	regionPoolAvailable = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_region_pool_available",
		Help: "Quota a region may still spend from its leases, summed over the subjects it tracks.",
	}, []string{"region", "kind"})

	regionPoolOutstanding = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_region_pool_outstanding",
		Help: "Quota the admin has leased out and no region has spent or returned yet.",
	}, []string{"region", "kind"})

	regionPoolUnreported = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_region_pool_unreported",
		Help: "Spend a region has made but not reported to the admin yet.",
	}, []string{"region", "kind"})

	regionPoolExhaustedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_region_pool_exhausted_total",
		Help: "Requests refused because a region lease had no quota left.",
	}, []string{"region", "kind"})

	regionPoolGrantsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_region_pool_grants_total",
		Help: "Quota granted to regions from the central balance.",
	}, []string{"region", "kind"})

	regionPoolReportTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "newapi_region_pool_report_total",
		Help: "Absolute spend reported by regions to the admin.",
	}, []string{"region", "kind"})

	regionPoolReportLagSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "newapi_region_pool_report_lag_seconds",
		Help: "Age of the oldest batch a region still owes the admin.",
	}, []string{"region"})
)

func init() {
	registry.MustRegister(
		regionPoolAvailable, regionPoolOutstanding, regionPoolUnreported,
		regionPoolExhaustedTotal, regionPoolGrantsTotal, regionPoolReportTotal,
		regionPoolReportLagSeconds,
	)
}

// ObserveRegionLeaseExhausted counts a request refused for lack of lease.
func ObserveRegionLeaseExhausted(region string, kind string) {
	if !Enabled() {
		return
	}
	regionPoolExhaustedTotal.WithLabelValues(region, kind).Inc()
}

// ObserveRegionLeaseGrant records quota handed to a region.
func ObserveRegionLeaseGrant(region string, kind string, quota int) {
	if !Enabled() || quota <= 0 {
		return
	}
	regionPoolGrantsTotal.WithLabelValues(region, kind).Add(float64(quota))
}

// ObserveRegionReport records a reported batch.
func ObserveRegionReport(region string, kind string, delta int64) {
	if !Enabled() || delta == 0 {
		return
	}
	value := float64(delta)
	if value < 0 {
		value = -value
	}
	regionPoolReportTotal.WithLabelValues(region, kind).Add(value)
}

// SetRegionPoolSnapshot publishes the gauges of one region.
func SetRegionPoolSnapshot(region string, kind string, available int, outstanding int, unreported int) {
	if !Enabled() {
		return
	}
	regionPoolAvailable.WithLabelValues(region, kind).Set(float64(available))
	regionPoolOutstanding.WithLabelValues(region, kind).Set(float64(outstanding))
	regionPoolUnreported.WithLabelValues(region, kind).Set(float64(unreported))
}

// SetRegionReportLag publishes how long a region has owed the admin a batch.
func SetRegionReportLag(region string, seconds float64) {
	if !Enabled() {
		return
	}
	regionPoolReportLagSeconds.WithLabelValues(region).Set(seconds)
}
