package metrics

import "github.com/prometheus/client_golang/prometheus"

const (
	MoonshotServiceOperationSearch = "search"
	MoonshotServiceOperationFetch  = "fetch"

	MoonshotServiceOutcomeSuccess            = "success"
	MoonshotServiceOutcomeEmpty              = "empty"
	MoonshotServiceOutcomeError              = "error"
	MoonshotServiceOutcomeUnsupportedChannel = "unsupported_channel"

	// MoonshotServiceCacheHit and MoonshotServiceCacheMiss tell a cached answer
	// from one this call asked the upstream for. service/websearch reports which
	// way it answered, so the metric counts both.
	MoonshotServiceCacheHit  = "hit"
	MoonshotServiceCacheMiss = "miss"
)

var moonshotServiceRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "newapi_moonshot_service_requests_total",
	Help: "Kimi Code search and fetch requests by operation, outcome and cache state.",
}, []string{"operation", "outcome", "cache"})

// ObserveMoonshotServiceRequest counts one /moonshot.service request. outcome
// separates a billed answer from a refunded one, because only a non-empty
// answer is charged.
func ObserveMoonshotServiceRequest(operation, outcome, cache string) {
	moonshotServiceRequestsTotal.WithLabelValues(operation, outcome, cache).Inc()
}
